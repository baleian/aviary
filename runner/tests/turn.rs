//! The runner takes the turns of a session over HTTP, runs each on the
//! harness and keeps what the agent does as the events of the turn.

mod support;

use std::fs;
use std::time::Duration;

use reqwest::StatusCode;
use serde_json::json;
use tokio::time;

use support::{Home, ending, names, runner, said, start};

#[tokio::test(flavor = "multi_thread")]
async fn a_turn_is_told_as_it_goes() {
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
    // The events are numbered from 1, without a gap.
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
async fn the_harness_stays_for_the_turns_of_a_session() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    let first = runner.run("t1", "say one").await;
    let second = runner.run("t2", "recall").await;
    let third = runner.run("t3", "recall").await;

    assert_eq!(said(&second), "say one");
    assert_eq!(said(&third), "say one\nrecall");
    // It is one session of the harness, in one process: a harness that was
    // started again would have another name for its first tool call.
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
async fn a_turn_is_started_once_however_often_it_is_sent() {
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
    // The runner runs one turn at a time.
    assert_eq!(runner.turn("t2", "say two").await, StatusCode::CONFLICT);
    let events = runner.events("t1").await;
    assert_eq!(names(&events), ["started", "message.delta", "finished"]);
    // A turn that has ended is not run again either.
    assert_eq!(runner.turn("t1", "say once").await, StatusCode::NO_CONTENT);
    assert_eq!(runner.events("t1").await, events);

    assert_eq!(
        said(&runner.run("t2", "recall").await),
        "say once ; sleep 300"
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn a_turn_that_the_runner_does_not_have_is_not_found() {
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
    // The runner keeps the events of a turn until it has the next one.
    assert_eq!(runner.follow("t1", 0).await.status, StatusCode::NOT_FOUND);
    assert_eq!(runner.cancel("t1").await, StatusCode::NOT_FOUND);
    // A turn that has ended is told to stop too late, which is no error.
    assert_eq!(runner.cancel("t2").await, StatusCode::NO_CONTENT);
}

#[tokio::test(flavor = "multi_thread")]
async fn the_events_of_a_turn_are_read_again_from_where_a_reader_left() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    assert_eq!(
        runner.turn("t1", "say a b ; sleep 300 ; say c d").await,
        StatusCode::NO_CONTENT
    );

    // A reader is cut off in the middle of the turn.
    let mut reader = runner.follow("t1", 0).await;
    let mut read = Vec::new();
    for _ in 0..3 {
        read.push(reader.next().await.unwrap());
    }
    drop(reader);
    assert_eq!(names(&read), ["started", "message.delta", "message.delta"]);

    // Another reads on from there, while a third reads all of it.
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

    // The turn has ended. Its events are there until the next turn, and a
    // reader that has them all is sent nothing more.
    assert_eq!(runner.events("t1").await, whole);
    assert!(runner.follow("t1", 6).await.next().await.is_none());
}

#[tokio::test(flavor = "multi_thread")]
async fn a_turn_that_the_harness_fails_says_why() {
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

    // The next turn has a harness again, which continues the session.
    let next = runner.run("t2", "recall").await;
    assert_eq!(said(&next), "say I try ; fail the model is out of order");
    assert_eq!(
        next[0].data["harness_session_id"],
        events[0].data["harness_session_id"]
    );
    assert_eq!(ending(&next).name, "finished");
}

#[tokio::test(flavor = "multi_thread")]
async fn a_harness_that_is_gone_is_started_again() {
    let home = Home::new();
    let runner = start(runner(&home)).await;

    // The harness ends in the middle of a turn.
    let died = runner.run("t1", "say going ; die").await;
    assert_eq!(ending(&died).name, "failed");
    assert_eq!(ending(&died).data["reason"], "harness_error");
    let next = runner.run("t2", "recall").await;
    assert_eq!(said(&next), "say going ; die");
    assert_eq!(ending(&next).name, "finished");

    // The harness ends between two turns.
    let left = runner.run("t3", "say bye ; leave").await;
    assert_eq!(ending(&left).name, "finished");
    time::sleep(Duration::from_millis(300)).await;
    let back = runner.run("t4", "say back").await;
    assert_eq!(said(&back), "back", "{back:?}");
    assert_eq!(ending(&back).name, "finished");
    assert_eq!(
        back[0].data["harness_session_id"],
        died[0].data["harness_session_id"]
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn a_session_that_the_harness_has_lost_fails_its_turns() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    runner.run("t1", "say one ; die").await;
    // What the harness kept of the conversation is gone.
    fs::remove_dir_all(home.path().join("workspace/.fake-harness")).unwrap();
    for turn in ["t2", "t3"] {
        let events = runner.run(turn, "say two").await;
        assert_eq!(names(&events), ["failed"]);
        assert_eq!(ending(&events).data["reason"], "resume_failed");
        let message = ending(&events).data["message"].as_str().unwrap();
        assert!(message.contains("the session is not known"), "{message}");
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn a_runner_that_is_not_set_up_says_so() {
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
async fn the_instructions_are_where_the_harness_reads_them() {
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
    // An agent without instructions has a file too, which is empty.
    assert_eq!(
        said(&runner.run("t2", "instructions").await),
        "no instructions"
    );

    // The image of a harness that takes no instructions names no file.
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

#[tokio::test(flavor = "multi_thread")]
async fn the_harness_is_started_with_what_the_first_turn_tells() {
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
async fn the_long_texts_of_a_tool_call_are_cut_as_the_installation_says() {
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

    // Without the setting they are told as they are.
    let runner = start(support::runner(&home)).await;
    let events = runner.run("t1", "big 100000").await;
    let output = events[2].data["output"]["output"].as_str().unwrap();
    assert_eq!(output.len(), 100_000);
}

#[tokio::test(flavor = "multi_thread")]
async fn a_harness_that_asks_is_allowed_once() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    let events = runner.run("t1", "ask").await;
    // Nobody is asked.
    assert_eq!(names(&events), ["started", "message.delta", "finished"]);
    assert_eq!(said(&events), "once");
}

#[tokio::test(flavor = "multi_thread")]
async fn a_harness_that_asks_is_answered_by_a_person() {
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

    // An answer that the harness does not take, and one to a request that
    // does not wait, are dropped.
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
    // An answer that comes twice is given once.
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

    // The requests of a turn are numbered as they come.
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

// A session is at work while a turn of it runs and waits for no person.
// What runs the sandbox asks, and puts a session to sleep that is not.
#[tokio::test(flavor = "multi_thread")]
async fn a_session_is_at_work_while_its_turn_waits_for_no_person() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    assert!(!runner.active().await);

    runner
        .turn_with("t1", json!({"text": "ask ; ask", "approval": "ask"}))
        .await;
    assert!(runner.active().await);
    let mut events = runner.follow("t1", 0).await;
    assert_eq!(events.next().await.unwrap().name, "started");
    assert_eq!(events.next().await.unwrap().name, "approval.requested");
    assert!(!runner.active().await);
    runner.answer("t1", "1", "once").await;
    assert_eq!(events.next().await.unwrap().name, "approval.resolved");
    // The harness goes on to its next request.
    assert_eq!(events.next().await.unwrap().name, "message.delta");
    assert_eq!(events.next().await.unwrap().name, "approval.requested");
    assert!(!runner.active().await);
    // A turn that is told to stop waits for no answer any more.
    runner.cancel("t1").await;
    assert_eq!(events.next().await.unwrap().name, "approval.resolved");
    while events.next().await.is_some() {}
    assert!(!runner.active().await);
}

#[tokio::test(flavor = "multi_thread")]
async fn requests_that_wait_at_once_are_each_given_their_own_answer() {
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
    // They are answered in another order than they were asked in.
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
async fn a_turn_that_is_cancelled_while_its_harness_asks_is_stopped() {
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
    // The harness is given the answer that the turn was cancelled to each
    // request, and nobody can answer them any more.
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
async fn a_turn_that_is_cancelled_is_stopped_by_the_harness() {
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
    // The harness is told once, however often the turn is.
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

    // The next turn is not stopped by what the one before was told.
    let next = runner.run("t2", "sleep 100 ; say on").await;
    assert_eq!(said(&next), "on");
    assert_eq!(ending(&next).data["stop_reason"], "end_turn");
}

#[tokio::test(flavor = "multi_thread")]
async fn a_turn_that_is_cancelled_before_the_harness_is_ready_is_not_given_to_it() {
    let home = Home::new();
    let mut command = runner(&home);
    command.env("FAKE_HARNESS_START_MS", "500");
    let runner = start(command).await;
    runner.turn("t1", "say Hello").await;
    assert_eq!(runner.cancel("t1").await, StatusCode::NO_CONTENT);
    let events = runner.events("t1").await;
    assert_eq!(names(&events), ["started", "finished"]);
    assert_eq!(ending(&events).data, json!({"stop_reason": "cancelled"}));
    // The harness never had the message.
    assert_eq!(said(&runner.run("t2", "recall").await), "nothing");
}

#[tokio::test(flavor = "multi_thread")]
async fn a_runner_that_waits_takes_no_cpu() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    runner.run("t1", "say Hello").await;
    // The harness is there and nobody sends a turn, as in a session that
    // rests. Then a turn waits for its harness.
    let before = cpu(runner.child.id().unwrap());
    time::sleep(Duration::from_secs(1)).await;
    runner.turn("t2", "sleep 1000").await;
    runner.events("t2").await;
    let used = cpu(runner.child.id().unwrap()) - before;
    assert!(used < 0.2, "the runner used {used} seconds of CPU in two");
}

/// The seconds of CPU that the process has used.
fn cpu(process: u32) -> f64 {
    let stat = fs::read_to_string(format!("/proc/{process}/stat")).unwrap();
    // The fields after the name of the program, which may have spaces.
    let fields: Vec<&str> = stat[stat.rfind(')').unwrap() + 2..].split(' ').collect();
    let ticks: f64 = fields[11].parse::<f64>().unwrap() + fields[12].parse::<f64>().unwrap();
    ticks / 100.0
}
