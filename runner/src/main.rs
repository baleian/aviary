//! The runner of a session. It runs in the session's sandbox, takes turns
//! over HTTP one at a time, drives the harness over the Agent Client Protocol
//! (ACP), and records what the agent does as the numbered events of each turn.
//!
//!   PUT  /turns/{turn}                       starts a turn
//!   GET  /turns/{turn}/events                streams the turn's events
//!   POST /turns/{turn}/cancel                cancels the turn
//!   PUT  /turns/{turn}/approvals/{request}   answers an approval request
//!
//! Every request is idempotent, so a caller can retry one whose outcome it
//! does not know. Requests from the runner's own pod are refused, so that the
//! agent cannot answer its own approval requests.
//!
//! The harness is started for the first turn and kept for the turns after it,
//! until a turn has another approval mode or environment: that turn gets a
//! new harness. The runner stores the harness session ID in the home, which
//! outlives the pod, so a new harness and the runner in the session's next
//! pod resume the same session.

use std::cell::Cell;
use std::collections::BTreeMap;
use std::convert::Infallible;
use std::env;
use std::error::Error;
use std::fmt::{self, Display};
use std::fs;
use std::io;
use std::path::{Path, PathBuf};
use std::process::Stdio;
use std::str::FromStr;
use std::sync::{Arc, Mutex};

use agent_client_protocol::schema::ProtocolVersion;
use agent_client_protocol::v1::ConnectionTo;
use agent_client_protocol::v1::schema::{
    CancelNotification, ContentBlock, ContentChunk, InitializeRequest, NewSessionRequest,
    PermissionOptionKind, PromptRequest, RequestPermissionOutcome, RequestPermissionRequest,
    RequestPermissionResponse, ResumeSessionRequest, SelectedPermissionOutcome, SessionId,
    SessionNotification, SessionUpdate, TextContent, ToolCallUpdate,
};
use agent_client_protocol::{AcpAgent, Agent, ByteStreams, Client, Responder, V1};
use axum::extract::connect_info::Connected;
use axum::extract::{ConnectInfo, Path as Named, Request, State};
use axum::http::{HeaderMap, StatusCode, header};
use axum::middleware;
use axum::response::sse::{self, KeepAlive, Sse};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post, put};
use axum::serve::IncomingStream;
use axum::{Json, Router};
use futures::channel::mpsc;
use futures::{FutureExt, Stream, StreamExt};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use tokio::net::TcpListener;
use tokio::process::Command;
use tokio::signal::unix::{SignalKind, signal};
use tokio::sync::watch;
use tokio_util::compat::{TokioAsyncReadCompatExt, TokioAsyncWriteCompatExt};

const HARNESS_COMMAND: &str = "AVIARY_HARNESS_COMMAND";
const MAX_TOOL_OUTPUT: &str = "AVIARY_MAX_TOOL_OUTPUT_BYTES";
const INSTRUCTIONS_FILE: &str = "AVIARY_INSTRUCTIONS_FILE";
const ADDRESS: &str = "AVIARY_RUNNER_ADDRESS";

/// The listen address if `AVIARY_RUNNER_ADDRESS` is not set.
const LISTENS: &str = "0.0.0.0:8700";

// The events that end a turn.
const FINISHED: &str = "finished";
const FAILED: &str = "failed";

const REQUESTED: &str = "approval.requested";
const RESOLVED: &str = "approval.resolved";

fn main() -> Result<(), Box<dyn Error>> {
    // Not the current-thread runtime: on it, waiting for the harness process
    // busy-polls and keeps a CPU busy for as long as the harness runs.
    tokio::runtime::Builder::new_multi_thread()
        .worker_threads(1)
        .enable_all()
        .build()?
        .block_on(serve())
}

/// Serves the HTTP API and runs turns until SIGTERM.
async fn serve() -> Result<(), Box<dyn Error>> {
    let address = env::var(ADDRESS).unwrap_or_else(|_| LISTENS.to_owned());
    let listener = TcpListener::bind(&address).await?;
    // Whoever starts the runner on port 0 reads the address from this line.
    let listens = listener.local_addr()?;
    println!("listening on {listens}");
    // As PID 1 of its container, the runner must handle SIGTERM itself to
    // stop when its pod is deleted.
    let mut ended = signal(SignalKind::terminate())?;
    let (orders, ordered) = mpsc::unbounded();
    let runner = Arc::new(Runner {
        turn: Mutex::new(None),
        changed: watch::Sender::new(()),
        orders,
    });
    let mut routes = Router::new()
        .route("/turns/{turn}", put(start))
        .route("/turns/{turn}/events", get(events))
        .route("/turns/{turn}/cancel", post(cancel))
        .route("/turns/{turn}/approvals/{request}", put(answer))
        .with_state(runner.clone());
    // On a loopback address, as in the tests, every caller is local.
    if !listens.ip().is_loopback() {
        routes = routes.layer(middleware::from_fn(from_elsewhere));
    }
    let served = routes.into_make_service_with_connect_info::<Ours>();
    tokio::spawn(async move { axum::serve(listener, served).await });
    tokio::select! {
        () = run_turns(&runner, ordered) => {}
        _ = ended.recv() => {}
    }
    Ok(())
}

/// Whether a connection comes from the runner's own pod, that is, from a
/// loopback address or from the pod's own address.
#[derive(Clone, Copy)]
struct Ours(bool);

impl Connected<IncomingStream<'_, TcpListener>> for Ours {
    fn connect_info(stream: IncomingStream<'_, TcpListener>) -> Self {
        let remote = stream.remote_addr().ip().to_canonical();
        Ours(match stream.io().local_addr() {
            Ok(local) => remote.is_loopback() || local.ip().to_canonical() == remote,
            // Fail closed.
            Err(_) => true,
        })
    }
}

/// Refuses requests from the runner's own pod.
async fn from_elsewhere(
    ConnectInfo(Ours(ours)): ConnectInfo<Ours>,
    request: Request,
    next: middleware::Next,
) -> Response {
    if ours {
        return (
            StatusCode::FORBIDDEN,
            "requests from the runner's own pod are forbidden\n",
        )
            .into_response();
    }
    next.run(request).await
}

/// The state shared by the HTTP handlers and the harness loop.
struct Runner {
    /// The running turn, or the last one.
    turn: Mutex<Option<Turn>>,
    /// Notifies event readers that the turn has changed.
    changed: watch::Sender<()>,
    /// Sends started turns to the harness loop.
    orders: mpsc::UnboundedSender<Order>,
}

struct Turn {
    id: String,
    /// An event's number is its index plus one.
    events: Vec<Event>,
    ended: bool,
    /// Sends cancellations and answers to the harness loop, in order.
    tell: mpsc::UnboundedSender<Told>,
}

#[derive(Clone)]
struct Event {
    name: &'static str,
    /// Serialized JSON.
    data: String,
}

/// A started turn, on its way to the harness loop.
struct Order {
    start: Start,
    told: mpsc::UnboundedReceiver<Told>,
}

/// The request body that starts a turn.
#[derive(Deserialize)]
struct Start {
    /// The user's message.
    text: String,
    /// The agent's instructions, written to the instructions file before
    /// every turn.
    #[serde(default)]
    instructions: String,
    /// "ask" to pass the harness's approval requests on to a person.
    /// Otherwise each request is allowed once.
    #[serde(default)]
    approval: String,
    /// Variables added to the runner's environment for the harness, such as
    /// the agent's model. A harness keeps those that it started with, so a
    /// turn that has others gets a new harness.
    #[serde(default)]
    environment: BTreeMap<String, String>,
}

/// The request body that answers an approval request.
#[derive(Deserialize)]
struct Answer {
    option_id: String,
}

/// A message from an HTTP handler to the running turn.
enum Told {
    Stop,
    Answer { request: String, option_id: String },
}

impl Runner {
    /// Appends an event to the running turn, if there is one.
    fn emit(&self, name: &'static str, data: &Value) {
        let mut turn = self.turn.lock().expect("turn lock poisoned");
        if let Some(turn) = turn.as_mut().filter(|turn| !turn.ended) {
            turn.events.push(Event {
                name,
                data: data.to_string(),
            });
            turn.ended = name == FINISHED || name == FAILED;
        }
        drop(turn);
        self.changed.send_replace(());
    }

    /// Ends the running turn, if there is one, with a failed event.
    fn fail(&self, failure: &Failure) {
        self.emit(FAILED, &failure.to_json());
    }

    /// Returns the event of turn `id` that follows event number `after`.
    fn next(&self, id: &str, after: usize) -> Next {
        let turn = self.turn.lock().expect("turn lock poisoned");
        match turn.as_ref().filter(|turn| turn.id == id) {
            // The runner has moved on to another turn.
            None => Next::Over,
            Some(turn) => match turn.events.get(after) {
                Some(event) => Next::Event(event.clone()),
                None if turn.ended => Next::Over,
                None => Next::Later,
            },
        }
    }

    /// Sends `told` to turn `id`, or returns 404 if it is not the current turn.
    fn tell(&self, id: &str, told: Told) -> StatusCode {
        let turn = self.turn.lock().expect("turn lock poisoned");
        match turn.as_ref().filter(|turn| turn.id == id) {
            None => StatusCode::NOT_FOUND,
            Some(turn) => {
                // Fails only if the turn has ended, which is not an error.
                let _ = turn.tell.unbounded_send(told);
                StatusCode::NO_CONTENT
            }
        }
    }
}

enum Next {
    Event(Event),
    /// No new event yet, and the turn has not ended.
    Later,
    Over,
}

async fn start(
    State(runner): State<Arc<Runner>>,
    Named(id): Named<String>,
    Json(start): Json<Start>,
) -> StatusCode {
    let mut turn = runner.turn.lock().expect("turn lock poisoned");
    match turn.as_ref() {
        Some(turn) if turn.id == id => return StatusCode::NO_CONTENT,
        Some(turn) if !turn.ended => return StatusCode::CONFLICT,
        _ => {}
    }
    let (tell, told) = mpsc::unbounded();
    *turn = Some(Turn {
        id,
        events: Vec::new(),
        ended: false,
        tell,
    });
    let _ = runner.orders.unbounded_send(Order { start, told });
    StatusCode::NO_CONTENT
}

/// Streams the turn's events as server-sent events, with the event number as
/// the ID. A client that reconnects with Last-Event-ID gets the events after
/// that number. The stream ends when the turn does.
async fn events(
    State(runner): State<Arc<Runner>>,
    Named(id): Named<String>,
    headers: HeaderMap,
) -> Result<Sse<impl Stream<Item = Result<sse::Event, Infallible>>>, StatusCode> {
    let after = headers
        .get(header::HeaderName::from_static("last-event-id"))
        .and_then(|after| after.to_str().ok())
        .and_then(|after| after.parse::<usize>().ok())
        .unwrap_or(0);
    let known = runner
        .turn
        .lock()
        .expect("turn lock poisoned")
        .as_ref()
        .is_some_and(|turn| turn.id == id);
    if !known {
        return Err(StatusCode::NOT_FOUND);
    }
    let changed = runner.changed.subscribe();
    let events = futures::stream::unfold((changed, after), move |(mut changed, after)| {
        let runner = runner.clone();
        let id = id.clone();
        async move {
            loop {
                // Marks the current value as seen before reading, so that no
                // change is missed.
                changed.borrow_and_update();
                match runner.next(&id, after) {
                    Next::Event(event) => {
                        let sent = sse::Event::default()
                            .id((after + 1).to_string())
                            .event(event.name)
                            .data(event.data);
                        return Some((Ok(sent), (changed, after + 1)));
                    }
                    Next::Over => return None,
                    Next::Later => changed.changed().await.ok()?,
                }
            }
        }
    });
    Ok(Sse::new(events).keep_alive(KeepAlive::default()))
}

async fn cancel(State(runner): State<Arc<Runner>>, Named(id): Named<String>) -> StatusCode {
    runner.tell(&id, Told::Stop)
}

async fn answer(
    State(runner): State<Arc<Runner>>,
    Named((id, request)): Named<(String, String)>,
    Json(answer): Json<Answer>,
) -> StatusCode {
    let option_id = answer.option_id;
    runner.tell(&id, Told::Answer { request, option_id })
}

/// The reason a turn failed, as reported in its failed event.
#[derive(Debug)]
struct Failure {
    /// harness_error, resume_failed or runner_error.
    reason: &'static str,
    message: String,
}

impl Failure {
    /// The harness could not be started or returned an error, which includes
    /// errors of the model provider.
    fn harness(error: impl Display) -> Self {
        Self::new("harness_error", error)
    }

    /// The harness returned an error. Only the message is kept: the error
    /// data is meant for programs, not people.
    fn answered(error: agent_client_protocol::Error) -> Self {
        Self::harness(error.message)
    }

    /// The harness could not resume its session, for example because its
    /// stored state is gone or unreadable.
    fn forgotten(error: agent_client_protocol::Error) -> Self {
        Self::new(
            "resume_failed",
            format!("resume harness session: {}", error.message),
        )
    }

    /// An error of the runner itself, such as a missing variable.
    fn runner(error: impl Display) -> Self {
        Self::new("runner_error", error)
    }

    fn new(reason: &'static str, error: impl Display) -> Self {
        // Harness errors can be long, and are stored with the turn.
        let message = error.to_string().chars().take(2000).collect();
        Self { reason, message }
    }

    fn to_json(&self) -> Value {
        json!({"reason": self.reason, "message": self.message})
    }
}

impl Display for Failure {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}: {}", self.reason, self.message)
    }
}

impl Error for Failure {}

/// Runs turns as they arrive. When the harness exits or fails, the turn it
/// was running fails, and the next turn starts a new harness that resumes the
/// session.
async fn run_turns(runner: &Arc<Runner>, mut orders: mpsc::UnboundedReceiver<Order>) {
    let mut session = None;
    // A turn that the last harness left to a new one.
    let mut left = None;
    loop {
        let order = match left.take() {
            Some(order) => order,
            None => match orders.next().await {
                Some(order) => order,
                None => return,
            },
        };
        match run_harness(runner, order, &mut orders, &mut session).await {
            Ok(next) => left = next,
            Err(failure) => {
                eprintln!("{failure}");
                runner.fail(&failure);
            }
        }
    }
}

/// Starts a harness and runs turns on it, beginning with `first`, until the
/// harness exits or a turn fails. A turn whose approval mode or environment
/// differ from those of `first` is returned instead of run, since the harness
/// has them from its start.
async fn run_harness(
    runner: &Arc<Runner>,
    first: Order,
    orders: &mut mpsc::UnboundedReceiver<Order>,
    session: &mut Option<SessionId>,
) -> Result<Option<Order>, Failure> {
    // The harness session of an earlier pod, if any.
    if session.is_none() {
        *session = remembered()?;
    }
    // Spawned here rather than by AcpAgent, to notice a harness that exits
    // between turns.
    let command = variable(HARNESS_COMMAND)?;
    let command = AcpAgent::from_str(&command)
        .map_err(Failure::harness)?
        .into_config();
    let mut process = Command::new(command.command())
        .args(command.arguments())
        .envs(command.environment())
        .envs(&first.start.environment)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .kill_on_drop(true)
        .spawn()
        .map_err(|error| {
            Failure::harness(format!("start {}: {error}", command.command().display()))
        })?;
    let harness = ByteStreams::new(
        process.stdin.take().expect("no stdin").compat_write(),
        process.stdout.take().expect("no stdout").compat(),
    );
    let ended = process.wait().fuse();
    futures::pin_mut!(ended);
    let cwd = env::current_dir().map_err(Failure::runner)?;
    // Unset or 0 means no limit.
    let longest = env::var(MAX_TOOL_OUTPUT)
        .ok()
        .and_then(|bytes| bytes.parse::<usize>().ok())
        .filter(|bytes| *bytes > 0);
    let asks = first.start.approval == "ask";
    let environment = first.start.environment.clone();
    // The turn that needs a new harness.
    let mut left = None;
    // Set when session/resume fails.
    let forgotten = Cell::new(false);
    // The runner's own failure, if it ends the connection.
    let unready = Cell::new(None);
    // Approval requests to pass on to a person.
    let (asked, mut asking) = mpsc::unbounded();
    let mut next = Some(first);

    Client
        .v1()
        .on_receive_notification(
            {
                let runner = runner.clone();
                async move |notification: SessionNotification, _| {
                    if let Some((name, data)) = event(notification.update, longest) {
                        runner.emit(name, &data);
                    }
                    Ok(())
                }
            },
            agent_client_protocol::on_receive_notification!(),
        )
        .on_receive_request(
            async move |request: RequestPermissionRequest, responder, _| {
                if !asks {
                    return responder.respond(RequestPermissionResponse::new(allow_once(&request)));
                }
                // Answered later, by the turn loop.
                asked.unbounded_send((request, responder)).map_err(|_| {
                    agent_client_protocol::Error::internal_error().data("approval channel closed")
                })
            },
            agent_client_protocol::on_receive_request!(),
        )
        .connect_with(harness, async |harness: ConnectionTo<Agent>| {
            harness
                .send_request(InitializeRequest::new(ProtocolVersion::V1))
                .block_task()
                .await?;
            let session_id = match session.clone() {
                Some(session_id) => {
                    harness
                        .send_request(ResumeSessionRequest::new(session_id.clone(), cwd))
                        .block_task()
                        .await
                        .inspect_err(|_| forgotten.set(true))?;
                    session_id
                }
                None => {
                    let session_id = harness
                        .send_request(NewSessionRequest::new(cwd))
                        .block_task()
                        .await?
                        .session_id;
                    if let Err(failure) = remember(&session_id) {
                        unready.set(Some(failure));
                        return Err(agent_client_protocol::Error::internal_error());
                    }
                    session_id
                }
            };
            *session = Some(session_id.clone());
            loop {
                let Order { start, mut told } = match next.take() {
                    Some(order) => order,
                    None => futures::select! {
                        order = orders.next() => match order {
                            Some(order) => order,
                            None => return Ok(()),
                        },
                        status = ended => {
                            unready.set(Some(Failure::harness(match status {
                                Ok(status) => format!("harness exited ({status})"),
                                Err(error) => format!("wait for harness: {error}"),
                            })));
                            return Err(agent_client_protocol::Error::internal_error());
                        }
                    },
                };
                if (start.approval == "ask") != asks || start.environment != environment {
                    left = Some(Order { start, told });
                    return Ok(());
                }
                if let Err(failure) = instruct(&start.instructions) {
                    unready.set(Some(failure));
                    return Err(agent_client_protocol::Error::internal_error());
                }
                runner.emit(
                    "started",
                    &json!({"harness_session_id": session_id.to_string()}),
                );
                // A turn cancelled before its prompt is sent is never sent: a
                // harness may fail if it is cancelled just as it receives the
                // prompt.
                let mut stopping = false;
                while let Ok(told) = told.try_recv() {
                    stopping |= matches!(told, Told::Stop);
                }
                if stopping {
                    runner.emit(FINISHED, &json!({"stop_reason": "cancelled"}));
                    continue;
                }
                let prompt = harness
                    .send_request(PromptRequest::new(
                        session_id.clone(),
                        vec![ContentBlock::Text(TextContent::new(start.text))],
                    ))
                    .block_task()
                    .fuse();
                futures::pin_mut!(prompt);
                // Approval requests wait for an answer for as long as the
                // turn runs.
                let mut waiting: Vec<Asked> = Vec::new();
                let mut requests = 0;
                // The prompt returns when the turn ends, also after a
                // cancellation.
                let outcome = loop {
                    futures::select! {
                        // Fails if the harness exits mid-turn.
                        outcome = prompt => break outcome?,
                        told = told.select_next_some() => match told {
                            Told::Stop => {
                                // A turn may be cancelled more than once.
                                if !stopping {
                                    harness.send_notification(CancelNotification::new(session_id.clone()))?;
                                    stopping = true;
                                }
                                // ACP requires pending permission requests
                                // to be answered as cancelled.
                                for asked in waiting.drain(..) {
                                    asked.responder.respond(RequestPermissionResponse::new(
                                        RequestPermissionOutcome::Cancelled,
                                    ))?;
                                    runner.emit(RESOLVED, &resolved(&asked.id, "cancelled", None));
                                }
                            }
                            // Answers that are repeated, late, or not among
                            // the request's options are ignored.
                            Told::Answer { request, option_id } => {
                                let answered = waiting.iter().position(|asked| {
                                    asked.id == request && asked.option(&option_id).is_some()
                                });
                                if let Some(at) = answered {
                                    let asked = waiting.remove(at);
                                    let option = asked.option(&option_id).expect("found above");
                                    asked.responder.respond(RequestPermissionResponse::new(option))?;
                                    runner.emit(
                                        RESOLVED,
                                        &resolved(&asked.id, "selected", Some(&option_id)),
                                    );
                                }
                            }
                        },
                        (request, responder) = asking.select_next_some() => {
                            if stopping {
                                responder.respond(RequestPermissionResponse::new(
                                    RequestPermissionOutcome::Cancelled,
                                ))?;
                                continue;
                            }
                            requests += 1;
                            let id = requests.to_string();
                            runner.emit(REQUESTED, &requested(&id, &request, longest));
                            waiting.push(Asked { id, request, responder });
                        }
                    }
                };
                let usage = outcome.usage.map(|usage| {
                    object([
                        ("total_tokens", Some(json!(usage.total_tokens))),
                        ("input_tokens", Some(json!(usage.input_tokens))),
                        ("output_tokens", Some(json!(usage.output_tokens))),
                        ("thought_tokens", usage.thought_tokens.map(Value::from)),
                        (
                            "cached_read_tokens",
                            usage.cached_read_tokens.map(Value::from),
                        ),
                        (
                            "cached_write_tokens",
                            usage.cached_write_tokens.map(Value::from),
                        ),
                    ])
                });
                runner.emit(
                    FINISHED,
                    &object([
                        ("stop_reason", Some(json!(name(&outcome.stop_reason)))),
                        ("usage", usage),
                    ]),
                );
            }
        })
        .await
        .map_err(|error| match unready.take() {
            Some(failure) => failure,
            None if forgotten.get() => Failure::forgotten(error),
            None => Failure::answered(error),
        })?;
    Ok(left)
}

/// Writes the agent's instructions, even empty ones, to the file that the
/// image names in `AVIARY_INSTRUCTIONS_FILE`.
fn instruct(instructions: &str) -> Result<(), Failure> {
    let Ok(file) = env::var(INSTRUCTIONS_FILE) else {
        if instructions.is_empty() {
            return Ok(());
        }
        return Err(Failure::runner(format!(
            "agent has instructions but the harness image does not set {INSTRUCTIONS_FILE}"
        )));
    };
    let file = Path::new(&file);
    if let Some(directory) = file.parent() {
        fs::create_dir_all(directory).map_err(Failure::runner)?;
    }
    fs::write(file, instructions).map_err(Failure::runner)
}

fn session_file() -> Result<PathBuf, Failure> {
    Ok(Path::new(&variable("HOME")?).join(".aviary/harness-session"))
}

/// Returns the stored harness session ID, if any.
fn remembered() -> Result<Option<SessionId>, Failure> {
    match fs::read_to_string(session_file()?) {
        Ok(id) => Ok(Some(SessionId::new(id))),
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(None),
        Err(error) => Err(Failure::runner(error)),
    }
}

/// Stores the harness session ID atomically, by writing a temporary file and
/// renaming it.
fn remember(session: &SessionId) -> Result<(), Failure> {
    let file = session_file()?;
    if let Some(directory) = file.parent() {
        fs::create_dir_all(directory).map_err(Failure::runner)?;
    }
    let written = file.with_extension("new");
    fs::write(&written, session.to_string()).map_err(Failure::runner)?;
    fs::rename(&written, &file).map_err(Failure::runner)
}

fn variable(name: &str) -> Result<String, Failure> {
    env::var(name).map_err(|_| Failure::runner(format!("{name} is not set")))
}

/// Builds a JSON object, leaving out the fields whose value is `None`.
fn object<const N: usize>(fields: [(&str, Option<Value>); N]) -> Value {
    Value::Object(
        fields
            .into_iter()
            .filter_map(|(name, value)| Some((name.to_owned(), value?)))
            .collect(),
    )
}

/// Converts a session update of the harness into the name and data of a turn
/// event. Returns `None` for updates that are not reported.
fn event(update: SessionUpdate, longest: Option<usize>) -> Option<(&'static str, Value)> {
    // Tool input and output can be megabytes, such as a command's output.
    // The events are for people to read; the harness keeps the full text for
    // the model.
    let cut = |json| cut(json, longest);
    match update {
        SessionUpdate::AgentMessageChunk(chunk) => Some(("message.delta", delta(chunk)?)),
        SessionUpdate::AgentThoughtChunk(chunk) => Some(("thought.delta", delta(chunk)?)),
        SessionUpdate::ToolCall(call) => Some((
            "tool.call",
            object([
                ("id", Some(json!(call.tool_call_id.to_string()))),
                ("title", Some(json!(call.title))),
                ("kind", Some(json!(name(&call.kind)))),
                ("status", Some(json!(name(&call.status)))),
                ("input", call.raw_input.map(cut)),
                ("output", call.raw_output.map(cut)),
                ("meta", call.meta.map(Value::Object).map(cut)),
            ]),
        )),
        SessionUpdate::ToolCallUpdate(update) => {
            Some(("tool.update", tool_update(update, longest)))
        }
        SessionUpdate::Plan(plan) => Some((
            "plan.update",
            json!({
                "entries": plan
                    .entries
                    .into_iter()
                    .map(|entry| json!({
                        "content": entry.content,
                        "priority": name(&entry.priority),
                        "status": name(&entry.status),
                    }))
                    .collect::<Vec<_>>(),
            }),
        )),
        _ => None,
    }
}

/// Returns the data of a tool call update, which also describes the tool call
/// of an approval request. Only the fields that the harness sets are included.
fn tool_update(update: ToolCallUpdate, longest: Option<usize>) -> Value {
    let cut = |json| cut(json, longest);
    object([
        ("id", Some(json!(update.tool_call_id.to_string()))),
        ("title", update.fields.title.map(Value::from)),
        (
            "kind",
            update.fields.kind.as_ref().map(name).map(Value::from),
        ),
        (
            "status",
            update.fields.status.as_ref().map(name).map(Value::from),
        ),
        ("input", update.fields.raw_input.map(cut)),
        ("output", update.fields.raw_output.map(cut)),
        ("meta", update.meta.map(Value::Object).map(cut)),
    ])
}

/// An approval request that waits for an answer.
struct Asked {
    /// Numbered from 1 within the turn.
    id: String,
    request: RequestPermissionRequest,
    responder: Responder<RequestPermissionResponse, V1>,
}

impl Asked {
    /// Returns the outcome that selects option `id`, or `None` if the request
    /// does not offer it.
    fn option(&self, id: &str) -> Option<RequestPermissionOutcome> {
        let option = self
            .request
            .options
            .iter()
            .find(|option| option.option_id.to_string() == id)?;
        Some(RequestPermissionOutcome::Selected(
            SelectedPermissionOutcome::new(option.option_id.clone()),
        ))
    }
}

/// Returns the data of an approval.requested event.
fn requested(id: &str, request: &RequestPermissionRequest, longest: Option<usize>) -> Value {
    json!({
        "id": id,
        "tool_call": tool_update(request.tool_call.clone(), longest),
        "options": request
            .options
            .iter()
            .map(|option| json!({
                "id": option.option_id.to_string(),
                "kind": name(&option.kind),
                "name": option.name,
            }))
            .collect::<Vec<_>>(),
    })
}

/// Returns the data of an approval.resolved event. The outcome is "selected",
/// with the option that a person chose, or "cancelled" if the turn was
/// cancelled before anyone answered.
fn resolved(id: &str, outcome: &str, option_id: Option<&str>) -> Value {
    object([
        ("id", Some(json!(id))),
        ("outcome", Some(json!(outcome))),
        ("option_id", option_id.map(Value::from)),
    ])
}

/// Returns the data of a message or thought chunk. Only text is reported:
/// other content, such as images, returns `None`.
fn delta(chunk: ContentChunk) -> Option<Value> {
    match chunk.content {
        ContentBlock::Text(content) => Some(object([
            ("text", Some(json!(content.text))),
            (
                "message_id",
                chunk
                    .message_id
                    .map(|message_id| json!(message_id.to_string())),
            ),
        ])),
        _ => None,
    }
}

/// Returns the ACP name of a value, such as "in_progress".
fn name(value: &impl Serialize) -> String {
    match serde_json::to_value(value) {
        Ok(Value::String(name)) => name,
        _ => String::new(),
    }
}

/// Truncates the strings in `json` that are longer than `longest` bytes, and
/// appends to each how many bytes were cut. `None` means no limit.
fn cut(json: Value, longest: Option<usize>) -> Value {
    let Some(longest) = longest else {
        return json;
    };
    match json {
        Value::String(text) if text.len() > longest => {
            let mut end = longest;
            while !text.is_char_boundary(end) {
                end -= 1;
            }
            let cut = text.len() - end;
            Value::String(format!("{}... [{cut} more bytes cut]", &text[..end]))
        }
        Value::Array(values) => Value::Array(
            values
                .into_iter()
                .map(|value| self::cut(value, Some(longest)))
                .collect(),
        ),
        Value::Object(fields) => Value::Object(
            fields
                .into_iter()
                .map(|(name, value)| (name, self::cut(value, Some(longest))))
                .collect(),
        ),
        other => other,
    }
}

fn allow_once(request: &RequestPermissionRequest) -> RequestPermissionOutcome {
    request
        .options
        .iter()
        .find(|option| option.kind == PermissionOptionKind::AllowOnce)
        .map_or(RequestPermissionOutcome::Cancelled, |option| {
            RequestPermissionOutcome::Selected(SelectedPermissionOutcome::new(
                option.option_id.clone(),
            ))
        })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn update(json: Value) -> SessionUpdate {
        serde_json::from_value(json).expect("parse session update")
    }

    #[test]
    fn long_strings_are_cut() {
        assert_eq!(
            cut(json!("0123456789"), Some(4)),
            json!("0123... [6 more bytes cut]")
        );
        assert_eq!(cut(json!("0123"), Some(4)), json!("0123"));
        assert_eq!(cut(json!("0123456789"), None), json!("0123456789"));
        // At a character boundary.
        assert_eq!(
            cut(json!("가나다"), Some(4)),
            json!("가... [6 more bytes cut]")
        );
        assert_eq!(
            cut(
                json!({"a": ["0123456789", 7], "b": {"c": "0123456789"}}),
                Some(4)
            ),
            json!({
                "a": ["0123... [6 more bytes cut]", 7],
                "b": {"c": "0123... [6 more bytes cut]"},
            })
        );
    }

    #[test]
    fn message_and_thought_chunks_are_events() {
        let said = update(json!({
            "sessionUpdate": "agent_message_chunk",
            "content": {"type": "text", "text": "Hello"},
            "messageId": "m1",
        }));
        assert_eq!(
            event(said, None),
            Some((
                "message.delta",
                json!({"text": "Hello", "message_id": "m1"})
            ))
        );
        let thought = update(json!({
            "sessionUpdate": "agent_thought_chunk",
            "content": {"type": "text", "text": "Hm"},
        }));
        assert_eq!(
            event(thought, None),
            Some(("thought.delta", json!({"text": "Hm"})))
        );
    }

    #[test]
    fn unreported_updates_are_dropped() {
        for dropped in [
            json!({
                "sessionUpdate": "agent_message_chunk",
                "content": {"type": "image", "data": "", "mimeType": "image/png"},
            }),
            json!({
                "sessionUpdate": "user_message_chunk",
                "content": {"type": "text", "text": "Hello"},
            }),
            json!({"sessionUpdate": "available_commands_update", "availableCommands": []}),
        ] {
            let made = event(update(dropped.clone()), None);
            assert!(made.is_none(), "event({dropped}) = {made:?}, want None");
        }
    }

    #[test]
    fn tool_calls_are_events_with_their_long_texts_cut() {
        let call = update(json!({
            "sessionUpdate": "tool_call",
            "toolCallId": "t1",
            "title": "cat notes.txt",
            "kind": "execute",
            "status": "in_progress",
            "rawInput": {"command": "cat notes.txt"},
            "_meta": {"terminal": "0123456789"},
        }));
        assert_eq!(
            event(call, Some(15)),
            Some((
                "tool.call",
                json!({
                    "id": "t1", "title": "cat notes.txt", "kind": "execute", "status": "in_progress",
                    "input": {"command": "cat notes.txt"},
                    "meta": {"terminal": "0123456789"},
                })
            ))
        );

        let output = "0123456789".repeat(10);
        let update = update(json!({
            "sessionUpdate": "tool_call_update",
            "toolCallId": "t1",
            "status": "completed",
            "rawOutput": {"output": output, "exit_code": 0},
        }));
        assert_eq!(
            event(update, Some(15)),
            Some((
                "tool.update",
                json!({
                    "id": "t1", "status": "completed",
                    "output": {"output": "012345678901234... [85 more bytes cut]", "exit_code": 0},
                })
            ))
        );
    }

    #[test]
    fn the_plan_is_an_event() {
        let plan = update(json!({
            "sessionUpdate": "plan",
            "entries": [
                {"content": "read the notes", "priority": "high", "status": "completed"},
                {"content": "answer", "priority": "medium", "status": "pending"},
            ],
        }));
        assert_eq!(
            event(plan, None),
            Some((
                "plan.update",
                json!({"entries": [
                    {"content": "read the notes", "priority": "high", "status": "completed"},
                    {"content": "answer", "priority": "medium", "status": "pending"},
                ]})
            ))
        );
    }

    #[test]
    fn approval_events_report_request_and_outcome() {
        let request: RequestPermissionRequest = serde_json::from_value(json!({
            "sessionId": "s1",
            "toolCall": {
                "toolCallId": "t1", "title": "rm -r build", "kind": "execute", "status": "pending",
                "rawInput": {"command": "rm -r build", "note": "x".repeat(200)},
            },
            "options": [
                {"optionId": "a", "name": "Yes, and do not ask again", "kind": "allow_always"},
                {"optionId": "b", "name": "No", "kind": "reject_once"},
                {"optionId": "c", "name": "Never", "kind": "reject_always"},
            ],
        }))
        .expect("parse permission request");
        let note = format!("{}... [100 more bytes cut]", "x".repeat(100));
        assert_eq!(
            requested("3", &request, Some(100)),
            json!({
                "id": "3",
                "tool_call": {
                    "id": "t1", "title": "rm -r build", "kind": "execute", "status": "pending",
                    "input": {"command": "rm -r build", "note": note},
                },
                "options": [
                    {"id": "a", "kind": "allow_always", "name": "Yes, and do not ask again"},
                    {"id": "b", "kind": "reject_once", "name": "No"},
                    {"id": "c", "kind": "reject_always", "name": "Never"},
                ],
            })
        );
        assert_eq!(
            resolved("3", "selected", Some("b")),
            json!({"id": "3", "outcome": "selected", "option_id": "b"})
        );
        assert_eq!(
            resolved("3", "cancelled", None),
            json!({"id": "3", "outcome": "cancelled"})
        );
    }

    #[test]
    fn allow_once_never_allows_always() {
        let ask = |options: Value| -> RequestPermissionRequest {
            serde_json::from_value(json!({
                "sessionId": "s1",
                "toolCall": {"toolCallId": "t1"},
                "options": options,
            }))
            .expect("parse permission request")
        };
        let offered = ask(json!([
            {"optionId": "always", "name": "Always", "kind": "allow_always"},
            {"optionId": "once", "name": "Once", "kind": "allow_once"},
            {"optionId": "no", "name": "No", "kind": "reject_once"},
        ]));
        match allow_once(&offered) {
            RequestPermissionOutcome::Selected(selected) => {
                assert_eq!(selected.option_id.to_string(), "once");
            }
            other => panic!("allow_once = {other:?}, want Selected"),
        }
        // Never allows more than once.
        let not_offered = ask(json!([
            {"optionId": "always", "name": "Always", "kind": "allow_always"},
        ]));
        assert!(matches!(
            allow_once(&not_offered),
            RequestPermissionOutcome::Cancelled
        ));
    }

    #[test]
    fn failure_reports_reason_and_truncated_message() {
        assert_eq!(
            Failure::harness("the model is out of order").to_json(),
            json!({"reason": "harness_error", "message": "the model is out of order"})
        );
        let long = Failure::runner("가".repeat(5000));
        assert_eq!(long.message.chars().count(), 2000);
    }
}
