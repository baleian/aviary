//! Tests of the runner's HTTP API, with the fake harness.

mod support;

use std::fs;
use std::process::Stdio;
use std::time::Duration;

use reqwest::StatusCode;
use serde_json::{Value, json};
use tokio::io::{AsyncBufReadExt, BufReader};
use tokio::time;

use support::{Event, Home, PATIENCE, Session, ending, names, runner, said, start};

#[tokio::test(flavor = "multi_thread")]
async fn turn_events_are_streamed_in_order() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    let events = runner
        .run(
            "t1",
            "think so ; say Hello there ; tool ls -a ; plan answer ; say . Done",
        )
        .await;

    assert_eq!(
        names(&events),
        [
            "started",
            "thought.delta",
            "message.delta",
            "message.delta",
            "tool.call",
            "tool.update",
            "plan.update",
            "message.delta",
            "message.delta",
            "finished"
        ]
    );
    let numbers: Vec<u64> = events.iter().map(|event| event.seq).collect();
    assert_eq!(numbers, (1..=10).collect::<Vec<u64>>());
    assert_eq!(said(&events), "Hello there. Done");
    let session = events[0].data["harness_session_id"].as_str().unwrap();
    assert!(session.starts_with("session-"), "{session}");
    assert_eq!(events[1].data, json!({"text": "so"}));
    assert_eq!(
        events[4].data,
        json!({
            "id": "call-1", "title": "ls -a", "kind": "execute", "status": "in_progress",
            "input": {"command": "ls -a"},
        })
    );
    assert_eq!(
        events[5].data,
        json!({"id": "call-1", "status": "completed", "output": {"output": "ran ls -a"}})
    );
    assert_eq!(
        events[6].data,
        json!({"entries": [{"content": "answer", "priority": "medium", "status": "pending"}]})
    );
    assert_eq!(
        ending(&events).data,
        json!({
            "stop_reason": "end_turn",
            "usage": {"total_tokens": 18, "input_tokens": 11, "output_tokens": 7},
        })
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn session_title_of_the_harness_is_reported() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    // Session information without a title is not reported.
    let events = runner
        .run("t1", "title ; title Fix the build ; say done")
        .await;
    assert_eq!(
        names(&events),
        ["started", "session.update", "message.delta", "finished"]
    );
    assert_eq!(events[1].data, json!({"title": "Fix the build"}));
}

#[tokio::test(flavor = "multi_thread")]
async fn notices_of_the_harness_are_not_in_the_turn() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    let events = runner.run("t1", "notice Unknown model ; say done").await;
    assert_eq!(names(&events), ["started", "message.delta", "finished"]);
    assert_eq!(said(&events), "done");
}

#[tokio::test(flavor = "multi_thread")]
async fn harness_is_reused_across_turns() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    let first = runner.run("t1", "say one").await;
    let second = runner.run("t2", "recall").await;
    let third = runner.run("t3", "recall").await;

    assert_eq!(said(&second), "say one");
    assert_eq!(said(&third), "say one\nrecall");
    // The same harness session, in the same process: the fake harness numbers
    // tool calls per process.
    for later in [&second, &third] {
        assert_eq!(
            later[0].data["harness_session_id"],
            first[0].data["harness_session_id"]
        );
    }
    let calls = runner.run("t4", "tool a").await;
    let more = runner.run("t5", "tool b").await;
    assert_eq!(calls[1].data["id"], "call-1");
    assert_eq!(more[1].data["id"], "call-2");
}

#[tokio::test(flavor = "multi_thread")]
async fn starting_a_turn_is_idempotent() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    assert_eq!(
        runner.turn("t1", "say once ; sleep 300").await,
        StatusCode::NO_CONTENT
    );
    assert_eq!(
        runner.turn("t1", "say once ; sleep 300").await,
        StatusCode::NO_CONTENT
    );
    let events = runner.events("t1").await;
    assert_eq!(names(&events), ["started", "message.delta", "finished"]);
    assert_eq!(runner.turn("t1", "say once").await, StatusCode::NO_CONTENT);
    assert_eq!(runner.events("t1").await, events);

    assert_eq!(
        said(&runner.run("t2", "recall").await),
        "say once ; sleep 300"
    );
}

/// A turn that runs when the next one is given was left behind, as by a
/// snapshot taken while it ran: it is stopped, and the next turn starts once
/// it has.
#[tokio::test(flavor = "multi_thread")]
async fn the_next_turn_stops_one_left_behind() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    assert_eq!(
        runner.turn("t1", "say once ; sleep 30000").await,
        StatusCode::NO_CONTENT
    );
    assert_eq!(runner.turn("t2", "say two").await, StatusCode::CONFLICT);
    let events = runner.events("t1").await;
    assert_eq!(ending(&events).data["stop_reason"], "cancelled");
    assert_eq!(said(&runner.run("t2", "say two").await), "two");
}

#[tokio::test(flavor = "multi_thread")]
async fn unknown_turn_is_not_found() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    assert_eq!(runner.follow("t1", 0).await.status, StatusCode::NOT_FOUND);
    assert_eq!(runner.cancel("t1").await, StatusCode::NOT_FOUND);
    assert_eq!(
        runner.answer("t1", "1", "once").await,
        StatusCode::NOT_FOUND
    );

    runner.run("t1", "say one").await;
    runner.run("t2", "say two").await;
    // Only the latest turn is kept.
    assert_eq!(runner.follow("t1", 0).await.status, StatusCode::NOT_FOUND);
    assert_eq!(runner.cancel("t1").await, StatusCode::NOT_FOUND);
    // Cancelling a turn that has ended is not an error.
    assert_eq!(runner.cancel("t2").await, StatusCode::NO_CONTENT);
}

#[tokio::test(flavor = "multi_thread")]
async fn events_resume_after_last_event_id() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    assert_eq!(
        runner.turn("t1", "say a b ; sleep 300 ; say c d").await,
        StatusCode::NO_CONTENT
    );

    let mut reader = runner.follow("t1", 0).await;
    let mut read = Vec::new();
    for _ in 0..3 {
        read.push(reader.next().await.unwrap());
    }
    drop(reader);
    assert_eq!(names(&read), ["started", "message.delta", "message.delta"]);

    let mut rest = runner.follow("t1", 3).await;
    let mut all = runner.follow("t1", 0).await;
    while let Some(event) = rest.next().await {
        read.push(event);
    }
    let mut whole = Vec::new();
    while let Some(event) = all.next().await {
        whole.push(event);
    }
    assert_eq!(read, whole);
    assert_eq!(said(&read), "a bc d");
    assert_eq!(ending(&read).name, "finished");
    assert_eq!(ending(&read).seq, 6);

    assert_eq!(runner.events("t1").await, whole);
    assert!(runner.follow("t1", 6).await.next().await.is_none());
}

#[tokio::test(flavor = "multi_thread")]
async fn harness_error_fails_the_turn() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    let events = runner
        .run("t1", "say I try ; fail the model is out of order")
        .await;
    assert_eq!(
        names(&events),
        ["started", "message.delta", "message.delta", "failed"]
    );
    assert_eq!(
        ending(&events).data,
        json!({"reason": "harness_error", "message": "the model is out of order"})
    );

    let next = runner.run("t2", "recall").await;
    assert_eq!(said(&next), "say I try ; fail the model is out of order");
    assert_eq!(
        next[0].data["harness_session_id"],
        events[0].data["harness_session_id"]
    );
    assert_eq!(ending(&next).name, "finished");
}

#[tokio::test(flavor = "multi_thread")]
async fn exited_harness_is_restarted() {
    let home = Home::new();
    let mut command = runner(&home);
    command.stderr(Stdio::piped());
    let mut runner = start(command).await;
    let mut logged = BufReader::new(runner.child.stderr.take().unwrap()).lines();

    // The harness exits mid-turn.
    let died = runner.run("t1", "say going ; die").await;
    assert_eq!(ending(&died).name, "failed");
    assert_eq!(ending(&died).data["reason"], "harness_error");
    let next = runner.run("t2", "recall").await;
    assert_eq!(said(&next), "say going ; die");
    assert_eq!(ending(&next).name, "finished");

    // The harness exits between turns.
    let left = runner.run("t3", "say bye ; leave").await;
    assert_eq!(ending(&left).name, "finished");
    let exited = time::timeout(PATIENCE, async {
        while let Some(line) = logged.next_line().await.unwrap() {
            if line.contains("harness exited") {
                return;
            }
        }
        panic!("runner closed stderr without logging that the harness exited");
    });
    exited
        .await
        .expect("runner did not log that the harness exited");
    let back = runner.run("t4", "say back").await;
    assert_eq!(said(&back), "back", "{back:?}");
    assert_eq!(ending(&back).name, "finished");
    assert_eq!(
        back[0].data["harness_session_id"],
        died[0].data["harness_session_id"]
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn lost_harness_session_fails_every_turn() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    runner.run("t1", "say one ; die").await;
    // Deletes the sessions that the fake harness stored.
    fs::remove_dir_all(home.path().join("workspace/.fake-harness")).unwrap();
    for turn in ["t2", "t3"] {
        let events = runner.run(turn, "say two").await;
        assert_eq!(names(&events), ["failed"]);
        assert_eq!(ending(&events).data["reason"], "resume_failed");
        let message = ending(&events).data["message"].as_str().unwrap();
        assert!(message.contains("unknown session"), "{message}");
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn new_runner_resumes_the_session() {
    let home = Home::new();
    let first = start(runner(&home)).await;
    let before = first.run("t1", "say one").await;
    // As when a sandbox sleeps: the runner stops and the home is kept.
    drop(first);
    let runner = start(runner(&home)).await;
    let after = runner.run("t2", "recall").await;
    assert_eq!(said(&after), "say one");
    assert_eq!(
        after[0].data["harness_session_id"],
        before[0].data["harness_session_id"]
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn turn_runs_on_after_sigterm() {
    let home = Home::new();
    let mut runner = start(runner(&home)).await;
    runner.turn("t1", "say on ; sleep 60000").await;
    let mut events = runner.follow("t1", 0).await;
    assert_eq!(events.next().await.unwrap().name, "started");
    // As when the worker of the sandbox goes away: the sandbox is to be
    // suspended before it does, with the turn in it.
    let pid = runner.child.id().unwrap().to_string();
    let told = std::process::Command::new("kill")
        .args(["-TERM", &pid])
        .status()
        .unwrap();
    assert!(told.success());
    // The runner would have exited by now if it took SIGTERM for its end.
    time::sleep(Duration::from_secs(1)).await;
    assert!(
        runner.child.try_wait().unwrap().is_none(),
        "runner exited on SIGTERM"
    );
    assert_eq!(runner.healthy().await, StatusCode::OK);
    assert_eq!(runner.turn("t1", "say on").await, StatusCode::NO_CONTENT);
}

#[tokio::test(flavor = "multi_thread")]
async fn requests_from_own_pod_are_forbidden() {
    let home = Home::new();
    let mut command = runner(&home);
    command.env("AVIARY_RUNNER_ADDRESS", "0.0.0.0:0");
    let runner = start(command).await;
    let port = runner.url.rsplit(':').next().unwrap().to_owned();
    for address in ["127.0.0.1".to_owned(), own_address()] {
        let ours = Session::at(format!("http://{address}:{port}"));
        assert_eq!(
            ours.turn("t1", "say hello").await,
            StatusCode::FORBIDDEN,
            "{address}"
        );
        assert_eq!(ours.cancel("t1").await, StatusCode::FORBIDDEN, "{address}");
    }
}

/// Returns the address that this machine sends from to other hosts.
fn own_address() -> String {
    let socket = std::net::UdpSocket::bind("0.0.0.0:0").unwrap();
    socket
        .connect("10.255.255.255:9")
        .expect("connect UDP socket");
    let address = socket.local_addr().unwrap().ip();
    assert!(!address.is_loopback(), "{address}");
    address.to_string()
}

#[tokio::test(flavor = "multi_thread")]
async fn misconfigured_runner_fails_the_turn() {
    let home = Home::new();
    let mut command = runner(&home);
    command.env_remove("AVIARY_HARNESS_COMMAND");
    let runner = start(command).await;
    let events = runner.run("t1", "say Hello").await;
    assert_eq!(names(&events), ["failed"]);
    assert_eq!(
        ending(&events).data,
        json!({"reason": "runner_error", "message": "AVIARY_HARNESS_COMMAND is not set"})
    );

    let mut command = support::runner(&home);
    command.env("AVIARY_HARNESS_COMMAND", "/nowhere/harness");
    let runner = start(command).await;
    let events = runner.run("t1", "say Hello").await;
    assert_eq!(names(&events), ["failed"]);
    assert_eq!(ending(&events).data["reason"], "harness_error");
}

#[tokio::test(flavor = "multi_thread")]
async fn instructions_file_is_written_every_turn() {
    let home = Home::new();
    let mut command = runner(&home);
    command.env("AVIARY_INSTRUCTIONS_FILE", home.instructions());
    let runner = start(command).await;
    assert_eq!(
        runner
            .turn_with(
                "t1",
                json!({"text": "instructions", "instructions": "Answer in French."})
            )
            .await,
        StatusCode::NO_CONTENT
    );
    assert_eq!(said(&runner.events("t1").await), "Answer in French.");
    // Without instructions, the file is emptied.
    assert_eq!(
        said(&runner.run("t2", "instructions").await),
        "no instructions"
    );

    // Without AVIARY_INSTRUCTIONS_FILE, only agents without instructions run.
    let other = Home::new();
    let runner = start(support::runner(&other)).await;
    assert_eq!(said(&runner.run("t1", "instructions").await), "no file");
    let runner = start(support::runner(&other)).await;
    runner
        .turn_with(
            "t1",
            json!({"text": "instructions", "instructions": "Answer in French."}),
        )
        .await;
    let events = runner.events("t1").await;
    assert_eq!(names(&events), ["failed"]);
    assert_eq!(ending(&events).data["reason"], "runner_error");
    let message = ending(&events).data["message"].as_str().unwrap();
    assert!(message.contains("AVIARY_INSTRUCTIONS_FILE"), "{message}");
}

/// A harness may read the instructions only when it starts.
#[tokio::test(flavor = "multi_thread")]
async fn harness_starts_with_the_instructions() {
    let home = Home::new();
    let mut command = runner(&home);
    command.env("AVIARY_INSTRUCTIONS_FILE", home.instructions());
    let runner = start(command).await;
    let run = async |id: &str, instructions: &str| {
        let start = json!({"text": "instructed ; tool a", "instructions": instructions});
        assert_eq!(runner.turn_with(id, start).await, StatusCode::NO_CONTENT);
        runner.events(id).await
    };
    // The fake harness numbers tool calls per process.
    let call = |events: &[Event]| {
        let call = events.iter().find(|event| event.name == "tool.call");
        call.unwrap().data["id"].clone()
    };
    let first = run("t1", "Answer in French.").await;
    assert_eq!(said(&first), "Answer in French.");
    let same = run("t2", "Answer in French.").await;
    assert_eq!(call(&same), "call-2");

    // Other instructions: a new harness, which has them.
    let other = run("t3", "Answer in German.").await;
    assert_eq!(said(&other), "Answer in German.");
    assert_eq!(call(&other), "call-1");
}

#[tokio::test(flavor = "multi_thread")]
async fn harness_starts_with_the_turn_environment() {
    let home = Home::new();
    let mut command = runner(&home);
    command.env("FROM_THE_IMAGE", "image");
    let runner = start(command).await;
    runner
        .turn_with(
            "t1",
            json!({
                "text": "env AVIARY_LLM_MODEL ; say , ; env FROM_THE_IMAGE",
                "environment": {"AVIARY_LLM_MODEL": "a-model"},
            }),
        )
        .await;
    assert_eq!(said(&runner.events("t1").await), "a-model,image");
}

#[tokio::test(flavor = "multi_thread")]
async fn turn_with_other_settings_gets_a_new_harness() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    // Runs a turn with the model and the approval mode.
    let run = async |id: &str, text: &str, model: &str, approval: &str| {
        let start =
            json!({"text": text, "approval": approval, "environment": {"AVIARY_LLM_MODEL": model}});
        assert_eq!(runner.turn_with(id, start).await, StatusCode::NO_CONTENT);
        runner.events(id).await
    };
    // The fake harness numbers tool calls per process.
    let first = run("t1", "env AVIARY_LLM_MODEL ; tool a", "one", "").await;
    assert_eq!(said(&first), "one");
    assert_eq!(first[2].data["id"], "call-1");
    let same = run("t2", "tool b", "one", "").await;
    assert_eq!(same[1].data["id"], "call-2");

    // Another model: a new harness, which resumes the session.
    let other = run("t3", "env AVIARY_LLM_MODEL ; tool c", "two", "").await;
    assert_eq!(said(&other), "two");
    assert_eq!(other[2].data["id"], "call-1");
    assert_eq!(
        other[0].data["harness_session_id"],
        first[0].data["harness_session_id"]
    );

    // Another approval mode: a new harness, which asks.
    let start =
        json!({"text": "ask", "approval": "ask", "environment": {"AVIARY_LLM_MODEL": "two"}});
    assert_eq!(runner.turn_with("t4", start).await, StatusCode::NO_CONTENT);
    let mut events = runner.follow("t4", 0).await;
    assert_eq!(events.next().await.unwrap().name, "started");
    assert_eq!(events.next().await.unwrap().name, "approval.requested");
    assert_eq!(
        runner.answer("t4", "1", "never").await,
        StatusCode::NO_CONTENT
    );
    assert_eq!(events.next().await.unwrap().name, "approval.resolved");
    assert_eq!(events.next().await.unwrap().data, json!({"text": "never"}));
    assert_eq!(events.next().await.unwrap().name, "finished");

    // And back: a new harness, which does not ask and has the whole session.
    let back = run("t5", "ask ; recall", "two", "").await;
    assert_eq!(
        said(&back),
        "onceenv AVIARY_LLM_MODEL ; tool a\ntool b\nenv AVIARY_LLM_MODEL ; tool c\nask"
    );
}

/// The harness reaches the MCP servers of a turn with the headers of each.
#[tokio::test(flavor = "multi_thread")]
async fn harness_starts_with_the_mcp_servers_of_the_turn() {
    let home = Home::new();
    let mut command = runner(&home);
    command.env("FAKE_HARNESS_MCP", "http,sse");
    let runner = start(command).await;
    let tracker = json!({"id": "1", "name": "tracker", "transport": "http", "url": "https://tracker.example.com/mcp",
        "headers": [{"name": "authorization", "value": "aviary"}, {"name": "x-team", "value": "a-team"}]});
    let docs = json!({"id": "2", "name": "docs", "transport": "sse", "url": "http://docs.example.com/sse"});
    let run = async |id: &str, text: &str, servers: Value| {
        let start = json!({"text": text, "mcp_servers": servers});
        assert_eq!(runner.turn_with(id, start).await, StatusCode::NO_CONTENT);
        runner.events(id).await
    };
    // The fake harness numbers tool calls per process.
    let call = |events: &[Event]| {
        let call = events.iter().find(|event| event.name == "tool.call");
        call.unwrap().data["id"].clone()
    };
    let first = run("t1", "mcp ; tool a", json!([tracker, docs])).await;
    assert_eq!(
        said(&first),
        "http tracker https://tracker.example.com/mcp authorization=aviary x-team=a-team\n\
         sse docs http://docs.example.com/sse"
    );
    let same = run("t2", "tool b", json!([tracker, docs])).await;
    assert_eq!(call(&same), "call-2");

    // Other servers: a new harness, which resumes the session with them.
    let other = run("t3", "mcp ; tool c ; say , ; recall", json!([docs])).await;
    assert_eq!(call(&other), "call-1");
    assert_eq!(
        said(&other),
        "sse docs http://docs.example.com/sse,\
         mcp ; tool a\ntool b"
    );
    let none = run("t4", "mcp", json!([])).await;
    assert_eq!(said(&none), "none");

    // The harness also has their names, from its start.
    let named = run("t5", "env AVIARY_MCP_SERVERS", json!([tracker, docs])).await;
    assert_eq!(said(&named), "tracker docs");
}

#[tokio::test(flavor = "multi_thread")]
async fn mcp_servers_that_the_harness_does_not_support_are_left_out() {
    let home = Home::new();
    let mut command = runner(&home);
    command.env("FAKE_HARNESS_MCP", "http");
    let runner = start(command).await;
    let servers = json!([
        {"id": "1", "name": "tracker", "transport": "http", "url": "https://tracker.example.com/mcp"},
        {"id": "2", "name": "docs", "transport": "sse", "url": "http://docs.example.com/sse"},
    ]);
    // Every turn of the harness says which it goes without.
    for id in ["t1", "t2"] {
        runner
            .turn_with(id, json!({"text": "mcp", "mcp_servers": servers}))
            .await;
        let events = runner.events(id).await;
        assert_eq!(names(&events)[..2], ["started", "mcp.unsupported"]);
        assert_eq!(
            events[1].data,
            json!({"servers": [{"id": "2", "name": "docs", "transport": "sse"}]})
        );
        assert_eq!(
            said(&events),
            "http tracker https://tracker.example.com/mcp"
        );
    }
}

/// The image says how the harness titles a call of a tool of an MCP server,
/// and such a call names its server and its tool.
#[tokio::test(flavor = "multi_thread")]
async fn calls_of_mcp_tools_name_their_server() {
    let home = Home::new();
    let mut command = runner(&home);
    command
        .env("FAKE_HARNESS_MCP", "http")
        .env("AVIARY_MCP_TOOL_TITLE", "{server}: {tool}");
    let runner = start(command).await;
    let server = json!({"id": "1", "name": "tracker", "transport": "http", "url": "https://tracker.example.com/mcp"});
    let turn = json!({"text": "tool tracker: search ; tool ls", "mcp_servers": [server]});
    runner.turn_with("t1", turn).await;
    let events = runner.events("t1").await;
    let calls: Vec<&Value> = events
        .iter()
        .filter(|event| event.name == "tool.call")
        .map(|event| &event.data)
        .collect();
    assert_eq!(
        calls[0]["mcp"],
        json!({"server": "1", "slug": "tracker", "tool": "search"})
    );
    assert_eq!(calls[1].get("mcp"), None, "{}", calls[1]);
}

#[tokio::test(flavor = "multi_thread")]
async fn tool_output_is_cut_at_the_configured_limit() {
    let home = Home::new();
    let mut command = runner(&home);
    command.env("AVIARY_MAX_TOOL_OUTPUT_BYTES", "100");
    let runner = start(command).await;
    let events = runner.run("t1", "big 1000").await;
    let output = events[2].data["output"]["output"].as_str().unwrap();
    assert_eq!(
        output,
        format!("{}... [900 more bytes cut]", "x".repeat(100))
    );

    // Unset means no limit.
    let runner = start(support::runner(&home)).await;
    let events = runner.run("t1", "big 100000").await;
    let output = events[2].data["output"]["output"].as_str().unwrap();
    assert_eq!(output.len(), 100_000);
}

#[tokio::test(flavor = "multi_thread")]
async fn requests_are_allowed_once_by_default() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    let events = runner.run("t1", "ask").await;
    assert_eq!(names(&events), ["started", "message.delta", "finished"]);
    assert_eq!(said(&events), "once");
}

#[tokio::test(flavor = "multi_thread")]
async fn approval_requests_wait_for_an_answer() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    runner
        .turn_with("t1", json!({"text": "ask ; ask", "approval": "ask"}))
        .await;
    let mut events = runner.follow("t1", 0).await;
    assert_eq!(events.next().await.unwrap().name, "started");
    let asked = events.next().await.unwrap();
    assert_eq!(asked.name, "approval.requested");
    assert_eq!(
        asked.data,
        json!({
            "id": "1",
            "tool_call": {
                "id": "call-0", "title": "ask", "kind": "execute", "input": {"command": "ask"},
            },
            "options": [
                {"id": "always", "kind": "allow_always", "name": "Always"},
                {"id": "once", "kind": "allow_once", "name": "Once"},
                {"id": "never", "kind": "reject_once", "name": "Never"},
            ],
        })
    );

    // Answers with an unknown option or request are accepted and ignored.
    assert_eq!(
        runner.answer("t1", "1", "perhaps").await,
        StatusCode::NO_CONTENT
    );
    assert_eq!(
        runner.answer("t1", "7", "once").await,
        StatusCode::NO_CONTENT
    );
    assert_eq!(
        runner.answer("t1", "1", "never").await,
        StatusCode::NO_CONTENT
    );
    // Only the first valid answer counts.
    assert_eq!(
        runner.answer("t1", "1", "always").await,
        StatusCode::NO_CONTENT
    );
    let resolved = events.next().await.unwrap();
    assert_eq!(resolved.name, "approval.resolved");
    assert_eq!(
        resolved.data,
        json!({"id": "1", "outcome": "selected", "option_id": "never"})
    );
    assert_eq!(events.next().await.unwrap().data, json!({"text": "never"}));

    let again = events.next().await.unwrap();
    assert_eq!(
        (again.name.as_str(), &again.data["id"]),
        ("approval.requested", &json!("2"))
    );
    runner.answer("t1", "2", "always").await;
    assert_eq!(
        events.next().await.unwrap().data,
        json!({"id": "2", "outcome": "selected", "option_id": "always"})
    );
    assert_eq!(events.next().await.unwrap().data, json!({"text": "always"}));
    assert_eq!(events.next().await.unwrap().name, "finished");
    assert!(events.next().await.is_none());
}

#[tokio::test(flavor = "multi_thread")]
async fn concurrent_approval_requests_get_their_own_answers() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    runner
        .turn_with("t1", json!({"text": "ask 3", "approval": "ask"}))
        .await;
    let mut events = runner.follow("t1", 0).await;
    assert_eq!(events.next().await.unwrap().name, "started");
    for request in ["1", "2", "3"] {
        let asked = events.next().await.unwrap();
        assert_eq!(asked.name, "approval.requested");
        assert_eq!(asked.data["id"], request);
    }
    runner.answer("t1", "2", "never").await;
    runner.answer("t1", "3", "once").await;
    runner.answer("t1", "1", "always").await;
    let mut rest = Vec::new();
    while let Some(event) = events.next().await {
        rest.push(event);
    }
    assert_eq!(said(&rest), "always never once");
    assert_eq!(ending(&rest).name, "finished");
}

#[tokio::test(flavor = "multi_thread")]
async fn cancel_resolves_pending_approval_requests() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    runner
        .turn_with("t1", json!({"text": "ask 2 ; say on", "approval": "ask"}))
        .await;
    let mut events = runner.follow("t1", 0).await;
    for name in ["started", "approval.requested", "approval.requested"] {
        assert_eq!(events.next().await.unwrap().name, name);
    }
    assert_eq!(runner.cancel("t1").await, StatusCode::NO_CONTENT);
    let mut rest = Vec::new();
    while let Some(event) = events.next().await {
        rest.push(event);
    }
    assert_eq!(rest[0].data, json!({"id": "1", "outcome": "cancelled"}));
    assert_eq!(rest[1].data, json!({"id": "2", "outcome": "cancelled"}));
    assert!(said(&rest).starts_with("cancelled cancelled"), "{rest:?}");
    assert_eq!(ending(&rest).data["stop_reason"], "cancelled");
    assert_eq!(
        runner.answer("t1", "1", "once").await,
        StatusCode::NO_CONTENT
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn cancelled_turn_is_stopped_by_the_harness() {
    let home = Home::new();
    let mut command = runner(&home);
    command.env("FAKE_HARNESS_STOP_MS", "300");
    let runner = start(command).await;
    runner
        .turn(
            "t1",
            "drawl one two three four five six seven eight nine ten",
        )
        .await;
    let mut events = runner.follow("t1", 0).await;
    for _ in 0..3 {
        events.next().await.unwrap();
    }
    // Repeated cancellations reach the harness once.
    for _ in 0..3 {
        assert_eq!(runner.cancel("t1").await, StatusCode::NO_CONTENT);
    }
    let mut rest = Vec::new();
    while let Some(event) = events.next().await {
        rest.push(event);
    }
    let text = said(&rest);
    assert!(text.ends_with("[told to stop 1 times]"), "{text}");
    assert!(!text.contains("ten"), "{text}");
    assert_eq!(ending(&rest).data["stop_reason"], "cancelled");

    // The cancellation does not carry over to the next turn.
    let next = runner.run("t2", "sleep 100 ; say on").await;
    assert_eq!(said(&next), "on");
    assert_eq!(ending(&next).data["stop_reason"], "end_turn");
}

#[tokio::test(flavor = "multi_thread")]
async fn turn_cancelled_before_prompt_is_not_sent() {
    let home = Home::new();
    let mut command = runner(&home);
    command.env("FAKE_HARNESS_START_MS", "500");
    let runner = start(command).await;
    runner.turn("t1", "say Hello").await;
    assert_eq!(runner.cancel("t1").await, StatusCode::NO_CONTENT);
    let events = runner.events("t1").await;
    assert_eq!(names(&events), ["started", "finished"]);
    assert_eq!(ending(&events).data, json!({"stop_reason": "cancelled"}));
    assert_eq!(said(&runner.run("t2", "recall").await), "nothing");
}

#[tokio::test(flavor = "multi_thread")]
async fn idle_runner_uses_no_cpu() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    runner.run("t1", "say Hello").await;
    // Measures an idle second with the harness running, then a turn that
    // waits on the harness.
    let before = cpu(runner.child.id().unwrap());
    time::sleep(Duration::from_secs(1)).await;
    runner.turn("t2", "sleep 1000").await;
    runner.events("t2").await;
    let used = cpu(runner.child.id().unwrap()) - before;
    assert!(used < 0.2, "runner CPU = {used}s in 2s, want < 0.2s");
}

/// Returns the CPU seconds, user and system, that the process has used.
fn cpu(process: u32) -> f64 {
    let stat = fs::read_to_string(format!("/proc/{process}/stat")).unwrap();
    // Skips the command name, which may contain spaces.
    let fields: Vec<&str> = stat[stat.rfind(')').unwrap() + 2..].split(' ').collect();
    let ticks: f64 = fields[11].parse::<f64>().unwrap() + fields[12].parse::<f64>().unwrap();
    ticks / 100.0
}
