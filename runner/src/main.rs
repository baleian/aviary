//! The runner of a session. It runs in the sandbox of the session, where it
//! takes the turns of the session over HTTP, one at a time, runs each on the
//! harness and keeps what the agent does as the numbered events of the turn.
//!
//!   PUT  /turns/{turn}                       starts the turn
//!   GET  /turns/{turn}/events                the events of the turn, as they come
//!   POST /turns/{turn}/cancel                tells the turn to stop
//!   PUT  /turns/{turn}/approvals/{request}   answers a request for approval
//!   GET  /active                             whether the session is at work
//!
//! A request may come twice: its sender sends it again when it does not know
//! what came of it. A turn that the runner has is not started again, and a
//! turn is told once to stop however often it is told.
//!
//! The harness is started for the first turn and stays when a turn has ended,
//! with what it knows of the conversation.
//!
//! A session that is not at work may be put to sleep, sandbox and all: one
//! that runs no turn, or whose turn waits for a person. It goes on from where
//! it was when it is woken.

use std::cell::Cell;
use std::collections::BTreeMap;
use std::convert::Infallible;
use std::env;
use std::error::Error;
use std::fmt::{self, Display};
use std::fs;
use std::path::Path;
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
use axum::extract::{Path as Named, State};
use axum::http::{HeaderMap, StatusCode, header};
use axum::response::sse::{self, KeepAlive, Sse};
use axum::routing::{get, post, put};
use axum::{Json, Router};
use futures::channel::mpsc;
use futures::{FutureExt, Stream, StreamExt};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use tokio::net::TcpListener;
use tokio::process::Command;
use tokio::sync::watch;
use tokio_util::compat::{TokioAsyncReadCompatExt, TokioAsyncWriteCompatExt};

const HARNESS_COMMAND: &str = "AVIARY_HARNESS_COMMAND";
const MAX_TOOL_OUTPUT: &str = "AVIARY_MAX_TOOL_OUTPUT_BYTES";
const INSTRUCTIONS_FILE: &str = "AVIARY_INSTRUCTIONS_FILE";
const ADDRESS: &str = "AVIARY_RUNNER_ADDRESS";

/// Where the runner listens when it is not told where.
const LISTENS: &str = "0.0.0.0:8700";

/// The events with which a turn ends.
const FINISHED: &str = "finished";
const FAILED: &str = "failed";
/// The events of a request for approval: that the harness asks, and that it
/// has its answer.
const REQUESTED: &str = "approval.requested";
const RESOLVED: &str = "approval.resolved";

fn main() -> Result<(), Box<dyn Error>> {
    // The runtime has a thread besides this one. On tokio's single-threaded
    // runtime, the wait for the harness's process polls without a pause, and
    // the runner keeps a CPU busy for as long as the harness is there.
    tokio::runtime::Builder::new_multi_thread()
        .worker_threads(1)
        .enable_all()
        .build()?
        .block_on(serve())
}

/// Listens for the turns and runs them until the runner is ended.
async fn serve() -> Result<(), Box<dyn Error>> {
    let address = env::var(ADDRESS).unwrap_or_else(|_| LISTENS.to_owned());
    let listener = TcpListener::bind(&address).await?;
    // Whoever started the runner on a port that it was left to choose reads
    // here which one it is.
    println!("listening on {}", listener.local_addr()?);
    let (orders, ordered) = mpsc::unbounded();
    let runner = Arc::new(Runner {
        turn: Mutex::new(None),
        changed: watch::Sender::new(()),
        orders,
    });
    let routes = Router::new()
        .route("/turns/{turn}", put(start))
        .route("/turns/{turn}/events", get(events))
        .route("/turns/{turn}/cancel", post(cancel))
        .route("/turns/{turn}/approvals/{request}", put(answer))
        .route("/active", get(active))
        .with_state(runner.clone());
    tokio::spawn(async move { axum::serve(listener, routes).await });
    run_turns(&runner, ordered).await;
    Ok(())
}

/// What the runner has of the session.
struct Runner {
    /// The turn that runs, or the last one that ran.
    turn: Mutex<Option<Turn>>,
    /// Tells those who read the events of the turn that there is more.
    changed: watch::Sender<()>,
    /// Gives the turns to where the harness is talked to.
    orders: mpsc::UnboundedSender<Order>,
}

struct Turn {
    id: String,
    /// The events of the turn. The number of an event is its place, from 1.
    events: Vec<Event>,
    ended: bool,
    /// How many requests of the turn for approval wait for their answers.
    asking: usize,
    /// Carries what the turn is told to where the harness is talked to, in
    /// the order in which it was told.
    tell: mpsc::UnboundedSender<Told>,
}

#[derive(Clone)]
struct Event {
    name: &'static str,
    /// The JSON of what the event tells.
    data: String,
}

/// A turn on its way to the harness.
struct Order {
    start: Start,
    told: mpsc::UnboundedReceiver<Told>,
}

/// What a turn is started with.
#[derive(Deserialize)]
struct Start {
    /// The message that the user sent.
    text: String,
    /// What the agent is told before every turn, in the words of whoever set
    /// the agent up.
    #[serde(default)]
    instructions: String,
    /// "ask" for an agent whose harness is set up to ask before it acts: a
    /// person answers it. Any other harness is allowed what it asks for,
    /// once.
    #[serde(default)]
    approval: String,
    /// What the harness is started with besides the environment of the
    /// runner: the model of the agent, for example. The harness is started
    /// for the first turn of a session, and the turns after it tell the same.
    #[serde(default)]
    environment: BTreeMap<String, String>,
}

/// The answer of a person to a request for approval.
#[derive(Deserialize)]
struct Answer {
    /// The ID of one of the options of the request.
    option_id: String,
}

/// What the turn is told while it runs.
enum Told {
    Stop,
    Answer { request: String, option_id: String },
}

impl Runner {
    /// Adds an event to the turn that runs.
    fn emit(&self, name: &'static str, data: &Value) {
        let mut turn = self.turn.lock().expect("the turn");
        if let Some(turn) = turn.as_mut().filter(|turn| !turn.ended) {
            turn.events.push(Event {
                name,
                data: data.to_string(),
            });
            match name {
                REQUESTED => turn.asking += 1,
                RESOLVED => turn.asking = turn.asking.saturating_sub(1),
                _ => {}
            }
            turn.ended = name == FINISHED || name == FAILED;
        }
        drop(turn);
        self.changed.send_replace(());
    }

    /// Ends the turn that runs, if one does, as failed.
    fn fail(&self, failure: &Failure) {
        self.emit(FAILED, &failure.to_json());
    }

    /// The event of the turn that follows the one with the number.
    fn next(&self, id: &str, after: usize) -> Next {
        let turn = self.turn.lock().expect("the turn");
        match turn.as_ref().filter(|turn| turn.id == id) {
            // The runner has another turn by now, and no longer this one.
            None => Next::Over,
            Some(turn) => match turn.events.get(after) {
                Some(event) => Next::Event(event.clone()),
                None if turn.ended => Next::Over,
                None => Next::Later,
            },
        }
    }

    /// Tells the turn, if it is the one that runs.
    fn tell(&self, id: &str, told: Told) -> StatusCode {
        let turn = self.turn.lock().expect("the turn");
        match turn.as_ref().filter(|turn| turn.id == id) {
            None => StatusCode::NOT_FOUND,
            Some(turn) => {
                // A turn that has ended takes nothing more, which is fine:
                // it was told too late.
                let _ = turn.tell.unbounded_send(told);
                StatusCode::NO_CONTENT
            }
        }
    }
}

enum Next {
    Event(Event),
    /// The turn has not ended, and has no more events for now.
    Later,
    Over,
}

async fn start(
    State(runner): State<Arc<Runner>>,
    Named(id): Named<String>,
    Json(start): Json<Start>,
) -> StatusCode {
    let mut turn = runner.turn.lock().expect("the turn");
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
        asking: 0,
        tell,
    });
    let _ = runner.orders.unbounded_send(Order { start, told });
    StatusCode::NO_CONTENT
}

/// The events of a turn as server-sent events: the ID of each is its number.
/// A reader that was cut off asks again with the last number that it has, in
/// Last-Event-ID, and is sent the ones after it. The answer ends with the
/// turn.
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
        .expect("the turn")
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
                // What changes from here on is seen below, also when it
                // changes before the wait for it begins.
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

/// Whether the session is at work: a turn runs, and waits for no person.
async fn active(State(runner): State<Arc<Runner>>) -> Json<Value> {
    let turn = runner.turn.lock().expect("the turn");
    let active = turn
        .as_ref()
        .is_some_and(|turn| !turn.ended && turn.asking == 0);
    Json(json!({"active": active}))
}

/// Why the runner could not finish a turn.
#[derive(Debug)]
struct Failure {
    /// One of harness_error, resume_failed, runner_error.
    reason: &'static str,
    message: String,
}

impl Failure {
    /// The harness could not be started, or it answered with an error. That
    /// is also how a harness reports that the model endpoint refused it.
    fn harness(error: impl Display) -> Self {
        Self::new("harness_error", error)
    }

    /// The harness answered with an error. What it says of the error is for
    /// a person to read. The data that it adds is for a program.
    fn answered(error: agent_client_protocol::Error) -> Self {
        Self::harness(error.message)
    }

    /// The harness could not continue its session: what it had kept of the
    /// conversation is gone or cannot be read.
    fn forgotten(error: agent_client_protocol::Error) -> Self {
        Self::new(
            "resume_failed",
            format!(
                "the harness could not continue its session: {}",
                error.message
            ),
        )
    }

    /// The runner is not set up as it should be.
    fn runner(error: impl Display) -> Self {
        Self::new("runner_error", error)
    }

    fn new(reason: &'static str, error: impl Display) -> Self {
        // What a harness says of its failure may be long, and is kept with
        // the turn.
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

/// Runs the turns on the harness as they come. A harness that is lost takes
/// the turn that it ran with it, and the next turn starts another, which
/// continues the session of the one before.
async fn run_turns(runner: &Arc<Runner>, mut orders: mpsc::UnboundedReceiver<Order>) {
    let mut session = None;
    while let Some(order) = orders.next().await {
        if let Err(failure) = run_harness(runner, order, &mut orders, &mut session).await {
            eprintln!("{failure}");
            runner.fail(&failure);
        }
    }
}

/// Starts the harness for the turn and runs that turn and the ones after it,
/// for as long as the harness is there. It returns when the harness is lost
/// or a turn fails, with why.
async fn run_harness(
    runner: &Arc<Runner>,
    first: Order,
    orders: &mut mpsc::UnboundedReceiver<Order>,
    session: &mut Option<SessionId>,
) -> Result<(), Failure> {
    // The runner starts the harness itself, to know when it has ended: a
    // harness may end between two turns, when nothing is asked of it.
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
        .map_err(|error| Failure::harness(format!("{}: {error}", command.command().display())))?;
    let harness = ByteStreams::new(
        process.stdin.take().expect("its input").compat_write(),
        process.stdout.take().expect("its output").compat(),
    );
    let ended = process.wait().fuse();
    futures::pin_mut!(ended);
    let gone = || agent_client_protocol::Error::internal_error().data("the harness has ended");
    let cwd = env::current_dir().map_err(Failure::runner)?;
    // The longest text of a tool call that is reported as it is. Without the
    // setting, or with zero, all are.
    let longest = env::var(MAX_TOOL_OUTPUT)
        .ok()
        .and_then(|bytes| bytes.parse::<usize>().ok())
        .filter(|bytes| *bytes > 0);
    let asks = first.start.approval == "ask";
    // Set when the harness could not continue its session.
    let forgotten = Cell::new(false);
    // Set when the runner could not make a turn ready for the harness.
    let unready = Cell::new(None);
    // The requests of the harness for approval.
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
                // The harness waits for the answer, which comes later.
                asked.unbounded_send((request, responder)).map_err(|_| {
                    agent_client_protocol::Error::internal_error().data("the harness is lost")
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
                    harness
                        .send_request(NewSessionRequest::new(cwd))
                        .block_task()
                        .await?
                        .session_id
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
                        _ = ended => return Err(gone()),
                    },
                };
                if let Err(failure) = instruct(&start.instructions) {
                    unready.set(Some(failure));
                    return Err(agent_client_protocol::Error::internal_error());
                }
                runner.emit(
                    "started",
                    &json!({"harness_session_id": session_id.to_string()}),
                );
                // A turn that was told to stop while the harness was made
                // ready is not given to the harness. A harness that is told
                // to stop in the moment in which it gets the message may
                // fail.
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
                // The requests that wait for their answers. They wait for as
                // long as the turn runs.
                let mut waiting: Vec<Asked> = Vec::new();
                let mut requests = 0;
                // The harness answers the prompt when the turn has ended,
                // also when it was told to stop.
                let outcome = loop {
                    futures::select! {
                        // A harness that ends in the middle of the turn
                        // leaves the prompt without its answer, which is an
                        // error here too.
                        outcome = prompt => break outcome?,
                        told = told.select_next_some() => match told {
                            Told::Stop => {
                                // The harness is told once: the order may
                                // come again.
                                if !stopping {
                                    harness.send_notification(CancelNotification::new(session_id.clone()))?;
                                    stopping = true;
                                }
                                // A harness that is told to stop is owed the
                                // answer that the turn was cancelled to what
                                // it has asked.
                                for asked in waiting.drain(..) {
                                    asked.responder.respond(RequestPermissionResponse::new(
                                        RequestPermissionOutcome::Cancelled,
                                    ))?;
                                    runner.emit(RESOLVED, &resolved(&asked.id, "cancelled", None));
                                }
                            }
                            // An answer may come twice, or late, or be none
                            // of the answers that the harness takes.
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
        })
}

/// Writes the agent's instructions to the file that the harness reads them
/// from, which its image names. It is written before every turn, and for an
/// agent without instructions too.
fn instruct(instructions: &str) -> Result<(), Failure> {
    let Ok(file) = env::var(INSTRUCTIONS_FILE) else {
        if instructions.is_empty() {
            return Ok(());
        }
        return Err(Failure::runner(format!(
            "the agent has instructions, and the harness image names no file for them in {INSTRUCTIONS_FILE}"
        )));
    };
    let file = Path::new(&file);
    if let Some(directory) = file.parent() {
        fs::create_dir_all(directory).map_err(Failure::runner)?;
    }
    fs::write(file, instructions).map_err(Failure::runner)
}

fn variable(name: &str) -> Result<String, Failure> {
    env::var(name).map_err(|_| Failure::runner(format!("{name} is not set")))
}

/// A JSON object of the fields that have a value.
fn object<const N: usize>(fields: [(&str, Option<Value>); N]) -> Value {
    Value::Object(
        fields
            .into_iter()
            .filter_map(|(name, value)| Some((name.to_owned(), value?)))
            .collect(),
    )
}

/// Turns an update from the harness into an event of the turn: its name and
/// what it tells. Updates that are not events of a turn are dropped.
fn event(update: SessionUpdate, longest: Option<usize>) -> Option<(&'static str, Value)> {
    // What a tool was called with and what it returned can be megabytes,
    // the output of a command for example. The harness keeps all of it for
    // the model. What is reported is for a person to look at.
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

/// What the harness says of a tool call that it has told of before, or that
/// it asks about. Only what it says is there.
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

/// A request of the harness for approval that waits for its answer.
struct Asked {
    /// Its name among the requests of the turn.
    id: String,
    request: RequestPermissionRequest,
    responder: Responder<RequestPermissionResponse, V1>,
}

impl Asked {
    /// The answer that gives the harness the option, if the harness takes it.
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

/// What a harness asks approval for.
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

/// How a request for approval was answered to the harness. The outcome is
/// selected when the harness was given the option that a person chose, and
/// cancelled when the turn was told to stop before anybody answered.
fn resolved(id: &str, outcome: &str, option_id: Option<&str>) -> Value {
    object([
        ("id", Some(json!(id))),
        ("outcome", Some(json!(outcome))),
        ("option_id", option_id.map(Value::from)),
    ])
}

/// A piece of a message: its text, and the ID of the message if the harness
/// gives one. Other content, such as images, is not passed on.
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

/// The name that the harness protocol gives a value, such as "in_progress".
fn name(value: &impl Serialize) -> String {
    match serde_json::to_value(value) {
        Ok(Value::String(name)) => name,
        _ => String::new(),
    }
}

/// Cuts the texts in a value that are longer than longest bytes, and says
/// in each how much is missing.
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

    /// The update that a harness sends as the JSON.
    fn update(json: Value) -> SessionUpdate {
        serde_json::from_value(json).expect("an update of a session")
    }

    #[test]
    fn texts_that_are_too_long_are_cut() {
        assert_eq!(
            cut(json!("0123456789"), Some(4)),
            json!("0123... [6 more bytes cut]")
        );
        assert_eq!(cut(json!("0123"), Some(4)), json!("0123"));
        assert_eq!(cut(json!("0123456789"), None), json!("0123456789"));
        // A text is cut between two characters, not within one.
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
    fn what_the_agent_says_and_thinks_is_an_event() {
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
    fn what_is_no_event_of_a_turn_is_dropped() {
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
            assert!(made.is_none(), "{dropped} became {made:?}");
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
        // An update tells only what has changed.
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
    fn a_request_for_approval_is_told_as_the_harness_made_it() {
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
        .expect("a request for permission");
        // What the tool is called with is cut as in the events of the turn.
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
    fn a_permission_is_given_for_once() {
        let ask = |options: Value| -> RequestPermissionRequest {
            serde_json::from_value(json!({
                "sessionId": "s1",
                "toolCall": {"toolCallId": "t1"},
                "options": options,
            }))
            .expect("a request for permission")
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
            other => panic!("the request was answered with {other:?}"),
        }
        // A harness that does not offer to allow it once is not allowed more.
        let not_offered = ask(json!([
            {"optionId": "always", "name": "Always", "kind": "allow_always"},
        ]));
        assert!(matches!(
            allow_once(&not_offered),
            RequestPermissionOutcome::Cancelled
        ));
    }

    #[test]
    fn a_failure_is_told_as_its_reason_and_what_is_known_of_it() {
        assert_eq!(
            Failure::harness("the model is out of order").to_json(),
            json!({"reason": "harness_error", "message": "the model is out of order"})
        );
        let long = Failure::runner("가".repeat(5000));
        assert_eq!(long.message.chars().count(), 2000);
    }
}
