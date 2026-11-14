use std::env;
use std::error::Error;
use std::fs;
use std::path::Path;
use std::str::FromStr;

use agent_client_protocol::schema::ProtocolVersion;
use agent_client_protocol::v1::ConnectionTo;
use agent_client_protocol::v1::schema::{
    ContentBlock, ContentChunk, InitializeRequest, NewSessionRequest, PermissionOptionKind,
    PromptRequest, RequestPermissionOutcome, RequestPermissionRequest, RequestPermissionResponse,
    ResumeSessionRequest, SelectedPermissionOutcome, SessionId, SessionNotification, SessionUpdate,
    TextContent,
};
use agent_client_protocol::{AcpAgent, Agent, Client};
use futures::channel::mpsc;
use futures::{FutureExt, SinkExt};
use serde::Serialize;
use tonic::Request;
use tonic::transport::Endpoint;

use proto::run_turn_request::Message as Report;
use proto::run_turn_response::Message as Order;
use proto::runner_service_client::RunnerServiceClient;
use proto::{
    Finished, MessageDelta, PlanEntry, PlanUpdate, RunTurnRequest, Started, ThoughtDelta, ToolCall,
    ToolUpdate,
};

/// The protocol between the runner and the Supervisor, generated from proto/.
mod proto {
    tonic::include_proto!("aviary.runner.v1");
}

const USAGE: &str = "usage: aviary-runner [init-home <directory>]";
const HARNESS_COMMAND: &str = "AVIARY_HARNESS_COMMAND";
const GATEWAY_URL: &str = "AVIARY_GATEWAY_URL";
const TURN_TOKEN: &str = "AVIARY_TURN_TOKEN";

/// How many reports may wait for the Supervisor before the harness is held
/// back.
const REPORTS_AHEAD: usize = 64;

fn main() -> Result<(), Box<dyn Error>> {
    let args: Vec<String> = env::args().skip(1).collect();
    let args: Vec<&str> = args.iter().map(String::as_str).collect();
    match args[..] {
        [] => tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()?
            .block_on(run_turn()),
        ["init-home", home] => init_home(Path::new(home)),
        _ => Err(USAGE.into()),
    }
}

/// Creates the home of a session, which the harness container then mounts.
fn init_home(home: &Path) -> Result<(), Box<dyn Error>> {
    fs::create_dir_all(home.join("workspace"))?;
    Ok(())
}

/// Takes the turn from the Supervisor, runs it on the harness and reports
/// what the agent does.
async fn run_turn() -> Result<(), Box<dyn Error>> {
    let command = variable(HARNESS_COMMAND)?;
    let harness = AcpAgent::from_str(&command)?;
    let cwd = env::current_dir()?;

    let channel = Endpoint::from_shared(variable(GATEWAY_URL)?)?
        .connect()
        .await?;
    let (reports, outgoing) = mpsc::channel(REPORTS_AHEAD);
    let mut request = Request::new(outgoing);
    request.metadata_mut().insert(
        "authorization",
        format!("Bearer {}", variable(TURN_TOKEN)?).parse()?,
    );
    let mut orders = RunnerServiceClient::new(channel)
        .run_turn(request)
        .await?
        .into_inner();
    let Some(Order::Start(start)) = orders.message().await?.and_then(|order| order.message) else {
        return Err("the Supervisor did not start the turn".into());
    };

    let turn = Client
        .v1()
        .on_receive_notification(
            {
                let reports = reports.clone();
                async move |notification: SessionNotification, _| match event(notification.update) {
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
                        .await?;
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
            let started = Started {
                harness_session_id: session_id.to_string(),
            };
            report(&reports, Report::Started(started)).await?;
            let outcome = harness
                .send_request(PromptRequest::new(
                    session_id,
                    vec![ContentBlock::Text(TextContent::new(start.text))],
                ))
                .block_task()
                .await?;
            let finished = Finished {
                stop_reason: name(&outcome.stop_reason),
            };
            report(&reports, Report::Finished(finished)).await
        });

    // The Supervisor sends nothing more while the turn runs, so its stream
    // ends early only if the Supervisor fails the turn.
    futures::select! {
        outcome = turn.fuse() => outcome?,
        order = orders.message().fuse() => {
            order?;
            return Err("the Supervisor ended the turn".into());
        }
    }
    // Closing the reports tells the Supervisor that there are no more. The
    // turn is reported once the Supervisor has closed its side without error.
    drop(reports);
    while orders.message().await?.is_some() {}
    Ok(())
}

fn variable(name: &str) -> Result<String, Box<dyn Error>> {
    env::var(name).map_err(|_| format!("{name} is not set").into())
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
fn event(update: SessionUpdate) -> Option<Report> {
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
