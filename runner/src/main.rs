use std::cell::Cell;
use std::collections::VecDeque;
use std::env;
use std::error::Error;
use std::fmt::{self, Display};
use std::fs;
use std::future::poll_fn;
use std::io;
use std::path::Path;
use std::process;
use std::str::FromStr;
use std::time::{Duration, Instant};

use agent_client_protocol::schema::ProtocolVersion;
use agent_client_protocol::v1::ConnectionTo;
use agent_client_protocol::v1::schema::{
    CancelNotification, ContentBlock, ContentChunk, InitializeRequest, NewSessionRequest,
    PermissionOptionKind, PromptRequest, RequestPermissionOutcome, RequestPermissionRequest,
    RequestPermissionResponse, ResumeSessionRequest, SelectedPermissionOutcome, SessionId,
    SessionNotification, SessionUpdate, StopReason, TextContent,
};
use agent_client_protocol::{AcpAgent, Agent, Client};
use futures::channel::mpsc;
use futures::{FutureExt, SinkExt, StreamExt};
use prost::Message;
use serde::Serialize;
use tokio::signal::unix::{SignalKind, signal};
use tokio::time;
use tonic::metadata::AsciiMetadataValue;
use tonic::transport::{Channel, Endpoint};
use tonic::{Code, Request, Status, Streaming};
use uuid::Uuid;

use proto::run_turn_request::Message as Report;
use proto::run_turn_response::Message as Order;
use proto::runner_service_client::RunnerServiceClient;
use proto::{
    Finished, MessageDelta, PlanEntry, PlanUpdate, RunTurnRequest, RunTurnResponse, Start, Started,
    ThoughtDelta, ToolCall, ToolUpdate, Usage,
};

/// The protocol between the runner and the Supervisor, generated from proto/.
mod proto {
    tonic::include_proto!("aviary.runner.v1");
}

const USAGE: &str = "usage: aviary-runner [init-home <directory>]";
const HARNESS_COMMAND: &str = "AVIARY_HARNESS_COMMAND";
const GATEWAY_URL: &str = "AVIARY_GATEWAY_URL";
const TURN_TOKEN: &str = "AVIARY_TURN_TOKEN";
const MAX_TOOL_OUTPUT: &str = "AVIARY_MAX_TOOL_OUTPUT_BYTES";
const MAX_RECONNECT: &str = "AVIARY_MAX_RECONNECT_SECONDS";
const INSTRUCTIONS_FILE: &str = "AVIARY_INSTRUCTIONS_FILE";

/// The most bytes of reports that may wait for a Supervisor to record them
/// before the harness is held back.
const REPORTS_HELD: usize = 1 << 20;

/// How long a Supervisor may take to answer a call. A call that hangs would
/// otherwise keep the runner from one that another Supervisor answers.
const ANSWER: Duration = Duration::from_secs(5);

/// The first and the longest pause before the runner calls again after a
/// call that failed. The pauses double from one to the other.
const FIRST_PAUSE: Duration = Duration::from_millis(200);
const LONGEST_PAUSE: Duration = Duration::from_secs(5);

/// Where the runner leaves the reason for a turn that it could not finish.
/// Kubernetes keeps what a container writes there with the pod, which is
/// where the Supervisor reads it.
const TERMINATION_LOG: &str = "/dev/termination-log";

/// How often the runner asks the Supervisor whether it is still there, and
/// how long it waits for the answer. A Supervisor whose machine is gone
/// closes no connection, so without the question the turn would run on with
/// nobody to report to.
const KEEPALIVE: Duration = Duration::from_secs(30);

/// The exit code of a process that was ended by SIGTERM, by convention.
const TERMINATED: i32 = 128 + 15;

fn main() -> Result<(), Box<dyn Error>> {
    let args: Vec<String> = env::args().skip(1).collect();
    let args: Vec<&str> = args.iter().map(String::as_str).collect();
    match args[..] {
        [] => {
            let stopped = Cell::new(false);
            // The runtime has a thread besides this one. On tokio's
            // single-threaded runtime, the wait for the harness's process
            // polls without a pause, and the runner keeps a CPU busy for as
            // long as the turn runs.
            let outcome = tokio::runtime::Builder::new_multi_thread()
                .worker_threads(1)
                .enable_all()
                .build()?
                .block_on(run_turn(&stopped));
            // A turn that was stopped with its pod did not complete, whatever
            // came of it after that. The exit code tells the Supervisor.
            if stopped.get() {
                process::exit(TERMINATED);
            }
            if let Err(failure) = &outcome {
                // Nothing can be done about a reason that cannot be written.
                let _ = fs::write(TERMINATION_LOG, failure.to_json());
            }
            Ok(outcome?)
        }
        ["init-home", home] => init_home(Path::new(home)),
        _ => Err(USAGE.into()),
    }
}

/// Why the runner could not finish the turn.
#[derive(Debug)]
struct Failure {
    /// One of harness_error, resume_failed, supervisor_lost, runner_error.
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

    /// The harness could not continue its session of an earlier turn: what
    /// it had kept of the conversation is gone or cannot be read.
    fn forgotten(error: agent_client_protocol::Error) -> Self {
        Self::new(
            "resume_failed",
            format!(
                "the harness could not continue its session: {}",
                error.message
            ),
        )
    }

    /// The Supervisor could not be reached, or the connection to it broke.
    fn supervisor(error: impl Display) -> Self {
        Self::new("supervisor_lost", error)
    }

    /// The Supervisor ended the call with an error.
    fn refused(status: tonic::Status) -> Self {
        Self::supervisor(status.message())
    }

    /// The runner is not set up as it should be.
    fn runner(error: impl Display) -> Self {
        Self::new("runner_error", error)
    }

    fn new(reason: &'static str, error: impl Display) -> Self {
        // Kubernetes keeps a few kilobytes of what the container leaves.
        let message = error.to_string().chars().take(2000).collect();
        Self { reason, message }
    }

    fn to_json(&self) -> String {
        serde_json::json!({"reason": self.reason, "message": self.message}).to_string()
    }
}

impl Display for Failure {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}: {}", self.reason, self.message)
    }
}

impl Error for Failure {}

/// Creates the home of a session, which the harness container then mounts.
///
/// The directories are made from the top down, and none is looked up before
/// it is made. A node remembers for a while that a name was not on a network
/// volume. create_dir_all tries the lowest directory first, which looks the
/// ones above it up: when a pod on another node then makes one of them, as
/// that of another session of the same agent does, this pod is told that the
/// directory exists and yet does not find it, and the turn fails.
fn init_home(home: &Path) -> Result<(), Box<dyn Error>> {
    let workspace = home.join("workspace");
    let mut directories: Vec<&Path> = workspace
        .ancestors()
        .filter(|directory| directory.parent().is_some())
        .collect();
    directories.reverse();
    for directory in directories {
        match fs::create_dir(directory) {
            Err(error) if error.kind() != io::ErrorKind::AlreadyExists => return Err(error.into()),
            _ => {}
        }
    }
    Ok(())
}

/// Takes the turn from the Supervisor, runs it on the harness and reports
/// what the agent does. It sets stopped if the turn is stopped because the
/// pod is.
async fn run_turn(stopped: &Cell<bool>) -> Result<(), Failure> {
    // Kubernetes sends SIGTERM to a pod that it is about to stop, and kills
    // the pod a while later. The runner is the first process of the
    // container, so without this the signal would do nothing.
    let mut terminate = signal(SignalKind::terminate()).map_err(Failure::runner)?;
    let command = variable(HARNESS_COMMAND)?;
    let harness = AcpAgent::from_str(&command).map_err(Failure::harness)?;
    let cwd = env::current_dir().map_err(Failure::runner)?;
    // The longest text of a tool call that is reported as it is. Without the
    // setting, or with zero, all are.
    let longest = env::var(MAX_TOOL_OUTPUT)
        .ok()
        .and_then(|bytes| bytes.parse::<usize>().ok())
        .filter(|bytes| *bytes > 0);

    let mut link = Link::new()?;
    let (call, start) = link.attach().await?;
    instruct(&start.instructions)?;
    // Set when the harness could not continue its session.
    let forgotten = Cell::new(false);
    // The link holds the reports until a Supervisor has recorded them, so
    // the channel to it needs no room of its own.
    let (reports, reported) = mpsc::channel(0);
    // Carries the order to cancel to where the harness is talked to. One
    // order is enough, so more are dropped.
    let (mut cancel, mut cancelled) = mpsc::channel::<()>(1);
    let passing = link.pass_on(call, reported, cancel.clone()).fuse();
    futures::pin_mut!(passing);

    let turn = Client
        .v1()
        .on_receive_notification(
            {
                let reports = reports.clone();
                async move |notification: SessionNotification, _| match event(
                    notification.update,
                    longest,
                ) {
                    Some(event) => report(&reports, event).await,
                    None => Ok(()),
                }
            },
            agent_client_protocol::on_receive_notification!(),
        )
        .on_receive_request(
            async |request: RequestPermissionRequest, responder, _| {
                responder.respond(RequestPermissionResponse::new(allow_once(&request)))
            },
            agent_client_protocol::on_receive_request!(),
        )
        .connect_with(harness, async |harness: ConnectionTo<Agent>| {
            harness
                .send_request(InitializeRequest::new(ProtocolVersion::V1))
                .block_task()
                .await?;
            let session_id = match start.harness_session_id {
                Some(session_id) => {
                    let session_id = SessionId::new(session_id);
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
            // A turn that was told to stop while the harness was made ready
            // is not given to the harness. A harness that is told to stop
            // in the moment in which it gets the message may fail.
            if cancelled.try_recv().is_ok() {
                return Ok(StopReason::Cancelled);
            }
            let started = Started {
                harness_session_id: session_id.to_string(),
            };
            report(&reports, Report::Started(started)).await?;
            let prompt = harness
                .send_request(PromptRequest::new(
                    session_id.clone(),
                    vec![ContentBlock::Text(TextContent::new(start.text))],
                ))
                .block_task()
                .fuse();
            futures::pin_mut!(prompt);
            // The harness answers the prompt when the turn has ended, also
            // when it was told to stop. It is told once: the order comes
            // again with every Supervisor that the runner calls.
            let mut told = false;
            let outcome = loop {
                futures::select! {
                    outcome = prompt => break outcome?,
                    () = cancelled.select_next_some() => {
                        if !told {
                            harness.send_notification(CancelNotification::new(session_id.clone()))?;
                            told = true;
                        }
                    }
                }
            };
            let finished = Finished {
                stop_reason: name(&outcome.stop_reason),
                usage: outcome.usage.map(|usage| Usage {
                    total_tokens: count(usage.total_tokens),
                    input_tokens: count(usage.input_tokens),
                    output_tokens: count(usage.output_tokens),
                    thought_tokens: usage.thought_tokens.map(count),
                    cached_read_tokens: usage.cached_read_tokens.map(count),
                    cached_write_tokens: usage.cached_write_tokens.map(count),
                }),
            };
            report(&reports, Report::Finished(finished)).await?;
            Ok(outcome.stop_reason)
        });

    // While the turn runs the Supervisor may cancel it, and the pod may be
    // told to stop.
    let outcome = {
        let turn = turn.fuse();
        futures::pin_mut!(turn);
        loop {
            futures::select! {
                outcome = turn => break outcome.map_err(|error| if forgotten.get() {
                    Failure::forgotten(error)
                } else {
                    Failure::answered(error)
                }),
                // The reports end with the turn, so before that the link
                // ends only when the Supervisor is lost.
                lost = passing => return lost,
                // The harness is asked to stop, so that it can write down
                // how the turn ended in the time that the pod has left.
                _ = terminate.recv().fuse() => {
                    stopped.set(true);
                    let _ = cancel.try_send(());
                }
            }
        }
    };
    // The end of the reports tells the link that there are no more. The
    // turn is reported once a Supervisor has recorded them all, which is
    // waited for also when the turn failed.
    drop(reports);
    let reported = passing.await;
    let stop_reason = outcome?;
    reported?;
    // A harness that stops when it is asked to ends the turn as cancelled. A
    // turn that it ended in another way had completed, and stands.
    if stop_reason != StopReason::Cancelled {
        stopped.set(false);
    }
    Ok(())
}

/// A call to a Supervisor.
struct Call {
    reports: mpsc::Sender<RunTurnRequest>,
    orders: Streaming<RunTurnResponse>,
    /// Set when the call takes no more reports. Its orders then tell why.
    closed: bool,
}

/// The runner's side of the talk with the Supervisors. When a call breaks,
/// the runner calls again, to whichever Supervisor answers, and sends again
/// the reports that none has recorded.
struct Link {
    client: RunnerServiceClient<Channel>,
    token: AsciiMetadataValue,
    /// The name that the runner made up for itself. A turn is given again
    /// only to the runner that took it.
    runner: AsciiMetadataValue,
    /// How long the runner goes on without a Supervisor, if that has an
    /// end.
    patience: Option<Duration>,
    /// Since when it has been without one.
    lost: Option<Instant>,
    /// How long it waits before it calls again.
    pause: Duration,
    /// Whether the Supervisor says what it has recorded. A Supervisor of an
    /// older release does not, and gives no turn again.
    answers: bool,
    /// The reports that no Supervisor has recorded, oldest first, and their
    /// size in bytes.
    held: VecDeque<RunTurnRequest>,
    bytes: usize,
    /// How many of them the call has been given.
    given: usize,
    /// The number of the last report.
    last: u64,
}

impl Link {
    fn new() -> Result<Self, Failure> {
        let channel = Endpoint::from_shared(variable(GATEWAY_URL)?)
            .map_err(Failure::runner)?
            // An attempt to connect that gets no answer is not given up
            // otherwise, and no other is made: the runner would not find
            // the Supervisors that come later.
            .connect_timeout(ANSWER)
            .http2_keep_alive_interval(KEEPALIVE)
            .keep_alive_timeout(KEEPALIVE)
            .keep_alive_while_idle(true)
            .connect_lazy();
        let patience = env::var(MAX_RECONNECT)
            .ok()
            .and_then(|seconds| seconds.parse().ok())
            .filter(|seconds| *seconds > 0)
            .map(Duration::from_secs);
        Ok(Self {
            client: RunnerServiceClient::new(channel),
            token: format!("Bearer {}", variable(TURN_TOKEN)?)
                .parse()
                .map_err(Failure::runner)?,
            runner: Uuid::new_v4()
                .to_string()
                .parse()
                .map_err(Failure::runner)?,
            patience,
            lost: None,
            pause: Duration::ZERO,
            answers: false,
            held: VecDeque::new(),
            bytes: 0,
            given: 0,
            last: 0,
        })
    }

    /// Calls a Supervisor, which answers with Start.
    async fn call(&self) -> Result<(Call, Start), Status> {
        let (reports, outgoing) = mpsc::channel(0);
        let mut request = Request::new(outgoing);
        let metadata = request.metadata_mut();
        metadata.insert("authorization", self.token.clone());
        metadata.insert("aviary-runner", self.runner.clone());
        let answer = async {
            let mut orders = self.client.clone().run_turn(request).await?.into_inner();
            match orders.message().await?.and_then(|order| order.message) {
                Some(Order::Start(start)) => Ok((
                    Call {
                        reports,
                        orders,
                        closed: false,
                    },
                    start,
                )),
                _ => Err(Status::unavailable("the Supervisor did not start the turn")),
            }
        };
        time::timeout(ANSWER, answer)
            .await
            .unwrap_or_else(|_| Err(Status::unavailable("the Supervisor did not answer")))
    }

    /// Calls until a Supervisor gives the runner its turn, for as long as
    /// the runner may go on without one.
    async fn attach(&mut self) -> Result<(Call, Start), Failure> {
        let lost = *self.lost.get_or_insert_with(Instant::now);
        let patience = self.patience;
        let mut why = String::from("the Supervisor did not answer");
        let calling = async {
            loop {
                // The pauses of the runners differ, so that those that lost
                // the same Supervisor do not all call again at once.
                time::sleep(self.pause.mul_f64(0.5 + fastrand::f64() / 2.0)).await;
                match self.call().await {
                    Ok(taken) => break Ok(taken),
                    Err(status) if is_final(&status) => break Err(Failure::refused(status)),
                    Err(status) => why = status.message().to_owned(),
                }
                self.wait_longer();
            }
        };
        let called = match patience {
            Some(patience) => time::timeout_at((lost + patience).into(), calling).await,
            None => Ok(calling.await),
        };
        let Ok(called) = called else {
            return Err(Failure::supervisor(format!(
                "no Supervisor for {} seconds: {why}",
                lost.elapsed().as_secs()
            )));
        };
        let (call, start) = called?;
        self.answers = start.received.is_some();
        self.given = 0;
        self.recorded(start.received.unwrap_or(0));
        if self.held.is_empty() {
            self.found();
        }
        Ok((call, start))
    }

    /// Passes the reports on to the Supervisor, and the Supervisor's orders
    /// to cancel on to where the harness is talked to, until the reports
    /// end and a Supervisor has recorded them all.
    async fn pass_on(
        mut self,
        call: Call,
        mut reported: mpsc::Receiver<Report>,
        mut cancel: mpsc::Sender<()>,
    ) -> Result<(), Failure> {
        let mut call = Some(call);
        let mut more = true;
        while more || !self.held.is_empty() {
            let Some(current) = &mut call else {
                call = Some(self.attach().await?.0);
                continue;
            };
            let broken = tokio::select! {
                order = current.orders.message() => match order {
                    Ok(Some(order)) => {
                        match order.message {
                            Some(Order::Received(received)) => self.recorded(received.seq),
                            Some(Order::Cancel(_)) => {
                                let _ = cancel.try_send(());
                            }
                            Some(Order::Start(_)) | None => {}
                        }
                        None
                    }
                    Ok(None) => Some(Status::unavailable("the Supervisor ended the turn")),
                    Err(status) => Some(status),
                },
                ready = poll_fn(|context| current.reports.poll_ready(context)),
                    if !current.closed && self.given < self.held.len() =>
                {
                    let report = self.held[self.given].clone();
                    match ready.and_then(|()| current.reports.start_send(report)) {
                        Ok(()) if self.answers => self.given += 1,
                        // Nothing says what this Supervisor has recorded,
                        // so what it was given is let go of.
                        Ok(()) => self.let_go(),
                        Err(_) => current.closed = true,
                    }
                    None
                }
                report = reported.next(), if more && self.bytes < REPORTS_HELD => {
                    match report {
                        Some(report) => self.hold(report),
                        None => more = false,
                    }
                    None
                }
            };
            if let Some(status) = broken {
                if !self.answers || is_final(&status) {
                    return Err(Failure::refused(status));
                }
                self.lost.get_or_insert_with(Instant::now);
                // Without the pause, a runner whose reports a Supervisor
                // cannot record would call it again without a break.
                self.wait_longer();
                call = None;
            }
        }
        // The Supervisor ends the call when the runner has ended its side.
        if let Some(Call {
            reports,
            mut orders,
            ..
        }) = call
        {
            drop(reports);
            loop {
                match orders.message().await {
                    Ok(Some(_)) => {}
                    Ok(None) => break,
                    // What a Supervisor has recorded stands.
                    Err(_) if self.answers => break,
                    Err(status) => return Err(Failure::refused(status)),
                }
            }
        }
        Ok(())
    }

    /// Takes a report to hold until a Supervisor has recorded it.
    fn hold(&mut self, report: Report) {
        self.last += 1;
        let report = RunTurnRequest {
            message: Some(report),
            seq: self.last,
        };
        self.bytes += report.encoded_len();
        self.held.push_back(report);
    }

    /// Lets go of the reports up to the one with the number, which a
    /// Supervisor has recorded.
    fn recorded(&mut self, seq: u64) {
        while self.held.front().is_some_and(|report| report.seq <= seq) {
            self.let_go();
            self.given = self.given.saturating_sub(1);
            self.found();
        }
    }

    /// Lets go of the oldest report.
    fn let_go(&mut self) {
        if let Some(report) = self.held.pop_front() {
            self.bytes -= report.encoded_len();
        }
    }

    /// Notes that a Supervisor is there again.
    fn found(&mut self) {
        self.lost = None;
        self.pause = Duration::ZERO;
    }

    /// Makes the pause before the next call longer.
    fn wait_longer(&mut self) {
        self.pause = (self.pause * 2).clamp(FIRST_PAUSE, LONGEST_PAUSE);
    }
}

/// Whether a Supervisor ended a call for a reason that another call would
/// meet again.
fn is_final(status: &Status) -> bool {
    matches!(
        status.code(),
        Code::Unauthenticated
            | Code::FailedPrecondition
            | Code::InvalidArgument
            | Code::DataLoss
            | Code::ResourceExhausted
    )
}

/// Writes the agent's instructions to the file that the harness reads them
/// from, which its image names. It is written before the harness starts, and
/// for an agent without instructions too: the file may be in the session
/// home, where it would still say what an earlier turn was told.
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

async fn report(
    reports: &mpsc::Sender<Report>,
    report: Report,
) -> agent_client_protocol::Result<()> {
    reports
        .clone()
        .send(report)
        .await
        .map_err(|_| agent_client_protocol::Error::internal_error().data("the Supervisor is lost"))
}

/// Turns an update from the harness into the event that the Supervisor
/// passes on. Updates that are not events of a turn are dropped.
fn event(update: SessionUpdate, longest: Option<usize>) -> Option<Report> {
    // What a tool was called with and what it returned can be megabytes,
    // the output of a command for example. The harness keeps all of it for
    // the model. What is reported is for a person to look at.
    let value = |json| value(cut(json, longest));
    let structure = |fields| match cut(serde_json::Value::Object(fields), longest) {
        serde_json::Value::Object(fields) => structure(fields),
        _ => unreachable!("cutting an object leaves an object"),
    };
    match update {
        SessionUpdate::AgentMessageChunk(chunk) => {
            let (text, message_id) = text(chunk)?;
            Some(Report::MessageDelta(MessageDelta { text, message_id }))
        }
        SessionUpdate::AgentThoughtChunk(chunk) => {
            let (text, message_id) = text(chunk)?;
            Some(Report::ThoughtDelta(ThoughtDelta { text, message_id }))
        }
        SessionUpdate::ToolCall(call) => Some(Report::ToolCall(ToolCall {
            id: call.tool_call_id.to_string(),
            title: call.title,
            kind: name(&call.kind),
            status: name(&call.status),
            input: call.raw_input.map(value),
            output: call.raw_output.map(value),
            meta: call.meta.map(structure),
        })),
        SessionUpdate::ToolCallUpdate(update) => Some(Report::ToolUpdate(ToolUpdate {
            id: update.tool_call_id.to_string(),
            title: update.fields.title,
            kind: update.fields.kind.as_ref().map(name),
            status: update.fields.status.as_ref().map(name),
            input: update.fields.raw_input.map(value),
            output: update.fields.raw_output.map(value),
            meta: update.meta.map(structure),
        })),
        SessionUpdate::Plan(plan) => Some(Report::PlanUpdate(PlanUpdate {
            entries: plan
                .entries
                .into_iter()
                .map(|entry| PlanEntry {
                    priority: name(&entry.priority),
                    status: name(&entry.status),
                    content: entry.content,
                })
                .collect(),
        })),
        _ => None,
    }
}

/// The text of a piece of a message. Other content, such as images, is not
/// passed on.
fn text(chunk: ContentChunk) -> Option<(String, Option<String>)> {
    match chunk.content {
        ContentBlock::Text(content) => Some((
            content.text,
            chunk.message_id.map(|message_id| message_id.to_string()),
        )),
        _ => None,
    }
}

/// The name that the harness protocol gives a value, such as "in_progress".
fn name(value: &impl Serialize) -> String {
    match serde_json::to_value(value) {
        Ok(serde_json::Value::String(name)) => name,
        _ => String::new(),
    }
}

/// A number of tokens in the form that the Supervisor takes.
fn count(tokens: u64) -> i64 {
    i64::try_from(tokens).unwrap_or(i64::MAX)
}

/// Cuts the texts in a value that are longer than longest bytes, and says
/// in each how much is missing.
fn cut(json: serde_json::Value, longest: Option<usize>) -> serde_json::Value {
    use serde_json::Value;
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

fn value(json: serde_json::Value) -> prost_types::Value {
    use prost_types::value::Kind;
    let kind = match json {
        serde_json::Value::Null => Kind::NullValue(0),
        serde_json::Value::Bool(value) => Kind::BoolValue(value),
        serde_json::Value::Number(number) => Kind::NumberValue(number.as_f64().unwrap_or(f64::NAN)),
        serde_json::Value::String(value) => Kind::StringValue(value),
        serde_json::Value::Array(values) => Kind::ListValue(prost_types::ListValue {
            values: values.into_iter().map(value).collect(),
        }),
        serde_json::Value::Object(fields) => Kind::StructValue(structure(fields)),
    };
    prost_types::Value { kind: Some(kind) }
}

fn structure(fields: serde_json::Map<String, serde_json::Value>) -> prost_types::Struct {
    prost_types::Struct {
        fields: fields
            .into_iter()
            .map(|(name, json)| (name, value(json)))
            .collect(),
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
    use serde_json::json;

    use super::*;

    /// The update that a harness sends as the JSON.
    fn update(json: serde_json::Value) -> SessionUpdate {
        serde_json::from_value(json).expect("an update of a session")
    }

    /// A value of a report as the JSON that the Supervisor makes of it.
    fn json(value: &prost_types::Value) -> serde_json::Value {
        use prost_types::value::Kind;
        match value.kind.as_ref().expect("a value") {
            Kind::NullValue(_) => serde_json::Value::Null,
            Kind::BoolValue(value) => json!(value),
            Kind::NumberValue(value) => json!(value),
            Kind::StringValue(value) => json!(value),
            Kind::ListValue(list) => list.values.iter().map(json).collect(),
            Kind::StructValue(fields) => fields
                .fields
                .iter()
                .map(|(name, value)| (name.clone(), json(value)))
                .collect::<serde_json::Map<_, _>>()
                .into(),
        }
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
                json!({"command": ["cat", "0123456789"], "exit": 0, "out": {"text": "0123456789", "ok": true}}),
                Some(4)
            ),
            json!({
                "command": ["cat", "0123... [6 more bytes cut]"],
                "exit": 0,
                "out": {"text": "0123... [6 more bytes cut]", "ok": true},
            })
        );
    }

    #[test]
    fn what_the_agent_says_and_thinks_is_reported() {
        let said = update(json!({
            "sessionUpdate": "agent_message_chunk",
            "content": {"type": "text", "text": "Hello"},
            "messageId": "m1",
        }));
        match event(said, None) {
            Some(Report::MessageDelta(delta)) => {
                assert_eq!(delta.text, "Hello");
                assert_eq!(delta.message_id.as_deref(), Some("m1"));
            }
            other => panic!("the message was reported as {other:?}"),
        }
        let thought = update(json!({
            "sessionUpdate": "agent_thought_chunk",
            "content": {"type": "text", "text": "Hm"},
        }));
        match event(thought, None) {
            Some(Report::ThoughtDelta(delta)) => {
                assert_eq!(delta.text, "Hm");
                assert_eq!(delta.message_id, None);
            }
            other => panic!("the thought was reported as {other:?}"),
        }
    }

    #[test]
    fn what_is_no_event_of_a_turn_is_not_reported() {
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
            let reported = event(update(dropped.clone()), None);
            assert!(reported.is_none(), "{dropped} was reported as {reported:?}");
        }
    }

    #[test]
    fn tool_calls_are_reported_with_their_long_texts_cut() {
        let call = update(json!({
            "sessionUpdate": "tool_call",
            "toolCallId": "t1",
            "title": "cat notes.txt",
            "kind": "execute",
            "status": "in_progress",
            "rawInput": {"command": "cat notes.txt"},
            "_meta": {"terminal": "0123456789"},
        }));
        match event(call, Some(15)) {
            Some(Report::ToolCall(call)) => {
                assert_eq!(
                    (
                        call.id.as_str(),
                        call.title.as_str(),
                        call.kind.as_str(),
                        call.status.as_str()
                    ),
                    ("t1", "cat notes.txt", "execute", "in_progress")
                );
                assert_eq!(
                    call.input.as_ref().map(json),
                    Some(json!({"command": "cat notes.txt"}))
                );
                assert_eq!(call.output, None);
                let meta = prost_types::Value {
                    kind: call.meta.map(prost_types::value::Kind::StructValue),
                };
                assert_eq!(json(&meta), json!({"terminal": "0123456789"}));
            }
            other => panic!("the tool call was reported as {other:?}"),
        }

        let output = "0123456789".repeat(10);
        let update = update(json!({
            "sessionUpdate": "tool_call_update",
            "toolCallId": "t1",
            "status": "completed",
            "rawOutput": {"output": output, "exit_code": 0},
        }));
        match event(update, Some(15)) {
            Some(Report::ToolUpdate(update)) => {
                assert_eq!(update.id, "t1");
                assert_eq!(update.status.as_deref(), Some("completed"));
                // An update tells only what has changed.
                assert_eq!(
                    (update.title, update.kind, update.input),
                    (None, None, None)
                );
                assert_eq!(
                    update.output.as_ref().map(json),
                    Some(
                        json!({"output": "012345678901234... [85 more bytes cut]", "exit_code": 0.0})
                    )
                );
            }
            other => panic!("the update of the tool call was reported as {other:?}"),
        }
    }

    #[test]
    fn the_plan_is_reported() {
        let plan = update(json!({
            "sessionUpdate": "plan",
            "entries": [
                {"content": "read the notes", "priority": "high", "status": "completed"},
                {"content": "answer", "priority": "medium", "status": "pending"},
            ],
        }));
        match event(plan, None) {
            Some(Report::PlanUpdate(plan)) => {
                let entries: Vec<_> = plan
                    .entries
                    .iter()
                    .map(|entry| {
                        (
                            entry.content.as_str(),
                            entry.priority.as_str(),
                            entry.status.as_str(),
                        )
                    })
                    .collect();
                assert_eq!(
                    entries,
                    [
                        ("read the notes", "high", "completed"),
                        ("answer", "medium", "pending")
                    ]
                );
            }
            other => panic!("the plan was reported as {other:?}"),
        }
    }

    #[test]
    fn a_permission_is_given_for_once() {
        let ask = |options: serde_json::Value| -> RequestPermissionRequest {
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
    fn the_runner_calls_again_unless_the_supervisor_refused_it_for_good() {
        for (code, is) in [
            (Code::Unauthenticated, true),
            (Code::FailedPrecondition, true),
            (Code::InvalidArgument, true),
            (Code::DataLoss, true),
            (Code::ResourceExhausted, true),
            (Code::Unavailable, false),
            (Code::Internal, false),
            (Code::Cancelled, false),
            (Code::Unknown, false),
            (Code::DeadlineExceeded, false),
        ] {
            assert_eq!(is_final(&Status::new(code, "")), is, "{code:?}");
        }
    }

    #[test]
    fn a_failure_is_left_as_its_reason_and_what_is_known_of_it() {
        let left: serde_json::Value =
            serde_json::from_str(&Failure::harness("the model is out of order").to_json())
                .expect("JSON");
        assert_eq!(
            left,
            json!({"reason": "harness_error", "message": "the model is out of order"})
        );
        // Kubernetes keeps only a few kilobytes of it.
        let long = Failure::runner("가".repeat(5000));
        assert_eq!(long.message.chars().count(), 2000);
        assert!(long.to_json().len() < 4096 * 2);
    }
}
