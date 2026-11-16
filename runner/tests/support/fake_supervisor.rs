//! The Supervisor of the tests as a program. The tests of the harness images
//! run the runner in a container, where it reaches no Supervisor that is a
//! part of the test. This one runs in a container beside it.
//!
//! It gives its runner the turn that its variables describe: TURN_TEXT is
//! the message, TURN_HARNESS_SESSION the harness session to continue and
//! TURN_INSTRUCTIONS the instructions of the agent. It writes what it sees
//! of the runner to its output, a JSON object on each line: the calls of the
//! runner and the reports that it records. On SIGUSR1 it tells the runner to
//! stop the turn. It ends when the runner has ended its call.

#[path = "supervisor.rs"]
mod supervisor;

use std::env;
use std::time::Duration;

use serde_json::{Value, json};
use tokio::signal::unix::{SignalKind, signal};
use tokio::time;

use supervisor::Supervisor;
use supervisor::proto::run_turn_request::Message as Report;

fn value(value: &prost_types::Value) -> Value {
    use prost_types::value::Kind;
    match &value.kind {
        None | Some(Kind::NullValue(_)) => Value::Null,
        Some(Kind::BoolValue(value)) => json!(value),
        Some(Kind::NumberValue(value)) => json!(value),
        Some(Kind::StringValue(value)) => json!(value),
        Some(Kind::ListValue(list)) => list.values.iter().map(self::value).collect(),
        Some(Kind::StructValue(fields)) => fields
            .fields
            .iter()
            .map(|(name, value)| (name.clone(), self::value(value)))
            .collect::<serde_json::Map<_, _>>()
            .into(),
    }
}

fn line(report: &Report) -> Value {
    match report {
        Report::Started(started) => {
            json!({"report": "started", "harness_session": started.harness_session_id})
        }
        Report::MessageDelta(delta) => json!({"report": "message", "text": delta.text}),
        Report::ThoughtDelta(delta) => json!({"report": "thought", "text": delta.text}),
        Report::ToolCall(call) => json!({
            "report": "tool call", "id": call.id, "title": call.title, "status": call.status,
            "input": call.input.as_ref().map(value), "output": call.output.as_ref().map(value),
        }),
        Report::ToolUpdate(update) => json!({
            "report": "tool update", "id": update.id, "title": update.title, "status": update.status,
            "input": update.input.as_ref().map(value), "output": update.output.as_ref().map(value),
        }),
        Report::PlanUpdate(plan) => json!({"report": "plan", "entries": plan.entries.len()}),
        Report::ApprovalRequested(asked) => json!({
            "report": "approval requested", "id": asked.id,
            "options": asked.options.iter().map(|option| json!({"id": option.id, "kind": option.kind})).collect::<Vec<_>>(),
        }),
        Report::ApprovalResolved(resolved) => json!({
            "report": "approval resolved", "id": resolved.id, "outcome": resolved.outcome, "option": resolved.option_id,
        }),
        Report::Finished(finished) => json!({
            "report": "finished", "stop_reason": finished.stop_reason,
            "usage": finished.usage.as_ref().map(|usage| json!({
                "total": usage.total_tokens, "input": usage.input_tokens, "output": usage.output_tokens,
            })),
        }),
    }
}

#[tokio::main]
async fn main() {
    let text = env::var("TURN_TEXT").expect("TURN_TEXT");
    let supervisor = Supervisor::start_at("0.0.0.0:9090".parse().unwrap(), &text).await;
    supervisor.state(|state| {
        state.harness_session = env::var("TURN_HARNESS_SESSION")
            .ok()
            .filter(|session| !session.is_empty());
        state.instructions = env::var("TURN_INSTRUCTIONS").unwrap_or_default();
    });
    let mut cancel = signal(SignalKind::user_defined1()).expect("a handler for SIGUSR1");
    let (mut calls, mut reports) = (0, 0);
    loop {
        tokio::select! {
            _ = cancel.recv() => supervisor.cancel(),
            () = time::sleep(Duration::from_millis(10)) => {}
        }
        let (lines, left) = supervisor.state(|state| {
            let mut lines = Vec::new();
            for (authorization, name) in &state.calls[calls..] {
                lines.push(json!({"call": {"authorization": authorization, "runner": name}}));
            }
            lines.extend(state.reports[reports..].iter().map(line));
            (calls, reports) = (state.calls.len(), state.reports.len());
            (lines, state.left)
        });
        for line in lines {
            println!("{line}");
        }
        if left {
            println!("{}", json!({"left": true}));
            break;
        }
    }
}
