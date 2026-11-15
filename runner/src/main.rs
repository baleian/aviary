use std::cell::Cell;
use std::env;
use std::error::Error;
use std::fmt::{self, Display};
use std::fs;
use std::path::Path;
use std::process;
use std::str::FromStr;
use std::time::Duration;

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
use serde::Serialize;
use tokio::signal::unix::{SignalKind, signal};
use tonic::Request;
use tonic::transport::Endpoint;

use proto::run_turn_request::Message as Report;
use proto::run_turn_response::Message as Order;
use proto::runner_service_client::RunnerServiceClient;
use proto::{
    Finished, MessageDelta, PlanEntry, PlanUpdate, RunTurnRequest, Started, ThoughtDelta, ToolCall,
    ToolUpdate, Usage,
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

/// How many reports may wait for the Supervisor before the harness is held
/// back.
const REPORTS_AHEAD: usize = 64;

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
            let outcome = tokio::runtime::Builder::new_current_thread()
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
fn init_home(home: &Path) -> Result<(), Box<dyn Error>> {
    fs::create_dir_all(home.join("workspace"))?;
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

    let channel = Endpoint::from_shared(variable(GATEWAY_URL)?)
        .map_err(Failure::runner)?
        .http2_keep_alive_interval(KEEPALIVE)
        .keep_alive_timeout(KEEPALIVE)
        .keep_alive_while_idle(true)
        .connect()
        .await
        .map_err(Failure::supervisor)?;
    let (reports, outgoing) = mpsc::channel(REPORTS_AHEAD);
    let mut request = Request::new(outgoing);
    request.metadata_mut().insert(
        "authorization",
        format!("Bearer {}", variable(TURN_TOKEN)?)
            .parse()
            .map_err(Failure::runner)?,
    );
    let mut orders = RunnerServiceClient::new(channel)
        .run_turn(request)
        .await
        .map_err(Failure::refused)?
        .into_inner();
    let first = orders.message().await.map_err(Failure::refused)?;
    let Some(Order::Start(start)) = first.and_then(|order| order.message) else {
        return Err(Failure::supervisor("the Supervisor did not start the turn"));
    };
    // Set when the harness could not continue its session.
    let forgotten = Cell::new(false);
    // Carries the order to cancel to where the harness is talked to. One
    // order is enough, so more are dropped.
    let (mut cancel, mut cancelled) = mpsc::channel::<()>(1);

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
            // when it was told to stop.
            let outcome = loop {
                futures::select! {
                    outcome = prompt => break outcome?,
                    () = cancelled.select_next_some() => {
                        harness.send_notification(CancelNotification::new(session_id.clone()))?;
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
    // told to stop. The Supervisor's stream ends early only if the
    // Supervisor fails the turn.
    let stop_reason = {
        let turn = turn.fuse();
        futures::pin_mut!(turn);
        loop {
            futures::select! {
                // A report that the Supervisor no longer takes also ends
                // the talk with the harness with an error.
                outcome = turn => break outcome.map_err(|error| if reports.is_closed() {
                    Failure::supervisor("the Supervisor closed the turn")
                } else if forgotten.get() {
                    Failure::forgotten(error)
                } else {
                    Failure::answered(error)
                })?,
                order = orders.message().fuse() => {
                    match order.map_err(Failure::refused)?.and_then(|order| order.message) {
                        Some(Order::Cancel(_)) => {
                            let _ = cancel.try_send(());
                        }
                        Some(Order::Start(_)) => {}
                        None => return Err(Failure::supervisor("the Supervisor ended the turn")),
                    }
                }
                // The harness is asked to stop, so that it can write down
                // how the turn ended in the time that the pod has left.
                _ = terminate.recv().fuse() => {
                    stopped.set(true);
                    let _ = cancel.try_send(());
                }
            }
        }
    };
    // A harness that stops when it is asked to ends the turn as cancelled. A
    // turn that it ended in another way had completed, and stands.
    if stop_reason != StopReason::Cancelled {
        stopped.set(false);
    }
    // Closing the reports tells the Supervisor that there are no more. The
    // turn is reported once the Supervisor has closed its side without error.
    drop(reports);
    while orders.message().await.map_err(Failure::refused)?.is_some() {}
    Ok(())
}

fn variable(name: &str) -> Result<String, Failure> {
    env::var(name).map_err(|_| Failure::runner(format!("{name} is not set")))
}

async fn report(
    reports: &mpsc::Sender<RunTurnRequest>,
    report: Report,
) -> agent_client_protocol::Result<()> {
    let request = RunTurnRequest {
        message: Some(report),
    };
    reports.clone().send(request).await.map_err(|_| {
        agent_client_protocol::Error::internal_error().data("the Supervisor closed the turn")
    })
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
