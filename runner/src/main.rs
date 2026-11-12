use std::env;
use std::error::Error;
use std::str::FromStr;

use agent_client_protocol::schema::ProtocolVersion;
use agent_client_protocol::v1::ConnectionTo;
use agent_client_protocol::v1::schema::{
    ContentBlock, InitializeRequest, NewSessionRequest, PermissionOptionKind, PromptRequest,
    RequestPermissionOutcome, RequestPermissionRequest, RequestPermissionResponse,
    ResumeSessionRequest, SelectedPermissionOutcome, SessionId, SessionNotification, TextContent,
};
use agent_client_protocol::{AcpAgent, Agent, Client};
use serde::Serialize;

const USAGE: &str = "usage: aviary-runner [--resume <session-id>] <prompt>";
const HARNESS_COMMAND: &str = "AVIARY_HARNESS_COMMAND";

fn main() -> Result<(), Box<dyn Error>> {
    let (resume, prompt) = parse_args().ok_or(USAGE)?;
    let command = env::var(HARNESS_COMMAND).map_err(|_| format!("{HARNESS_COMMAND} is not set"))?;
    let harness = AcpAgent::from_str(&command)?;
    let cwd = env::current_dir()?;

    futures::executor::block_on(
        Client
            .v1()
            .on_receive_notification(
                async |update: SessionNotification, _| emit(&update),
                agent_client_protocol::on_receive_notification!(),
            )
            .on_receive_request(
                async |request: RequestPermissionRequest, responder, _| {
                    emit(&request)?;
                    responder.respond(RequestPermissionResponse::new(allow_once(&request)))
                },
                agent_client_protocol::on_receive_request!(),
            )
            .connect_with(harness, async |harness: ConnectionTo<Agent>| {
                harness
                    .send_request(InitializeRequest::new(ProtocolVersion::V1))
                    .block_task()
                    .await?;
                let session_id = match resume {
                    Some(session_id) => {
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
                let outcome = harness
                    .send_request(PromptRequest::new(
                        session_id,
                        vec![ContentBlock::Text(TextContent::new(prompt))],
                    ))
                    .block_task()
                    .await?;
                emit(&outcome)
            }),
    )?;
    Ok(())
}

fn parse_args() -> Option<(Option<SessionId>, String)> {
    let mut args = env::args().skip(1).peekable();
    let resume = match args.next_if(|arg| arg == "--resume") {
        Some(_) => Some(SessionId::new(args.next()?)),
        None => None,
    };
    let prompt = args.next()?;
    args.next().is_none().then_some((resume, prompt))
}

fn emit(event: &impl Serialize) -> agent_client_protocol::Result<()> {
    println!("{}", serde_json::to_string(event)?);
    Ok(())
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
