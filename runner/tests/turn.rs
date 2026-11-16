//! The runner takes a turn from its Supervisor, runs it on the harness and
//! reports what the agent does.

mod support;

use std::fs;
use std::time::Duration;

use tokio::process::Command;
use tokio::time;
use tonic::Code;

use support::proto::run_turn_request::Message as Report;
use support::{Home, Supervisor, TOKEN, run, runner, start};

#[tokio::test(flavor = "multi_thread")]
async fn a_turn_is_reported_as_it_goes() {
    let home = Home::new();
    let supervisor =
        Supervisor::start("think so ; say Hello there ; tool ls -a ; plan answer ; say . Done")
            .await;
    let ended = run(&supervisor, &home).await;
    assert!(ended.status.success(), "{}", ended.stderr);

    supervisor.state(|state| {
        assert_eq!(
            state.kinds(),
            [
                "started",
                "thought",
                "message",
                "message",
                "tool call",
                "tool update",
                "plan",
                "message",
                "message",
                "finished"
            ]
        );
        assert_eq!(state.said(), "Hello there. Done");
        assert_eq!(state.last, 10);
        for report in &state.reports {
            match report {
                Report::Started(started) => {
                    assert!(started.harness_session_id.starts_with("session-"));
                }
                Report::ToolCall(call) => {
                    assert_eq!(
                        (call.title.as_str(), call.status.as_str()),
                        ("ls -a", "in_progress")
                    );
                }
                Report::ToolUpdate(update) => {
                    assert_eq!(update.status.as_deref(), Some("completed"));
                    assert!(update.output.is_some());
                }
                Report::Finished(finished) => {
                    assert_eq!(finished.stop_reason, "end_turn");
                    let usage = finished.usage.as_ref().expect("the usage of the turn");
                    assert_eq!(
                        (usage.total_tokens, usage.input_tokens, usage.output_tokens),
                        (18, 11, 7)
                    );
                    assert_eq!(usage.thought_tokens, None);
                }
                _ => {}
            }
        }
        // The runner says whose turn it wants and who it is, and ends the
        // call when it has done.
        assert_eq!(state.calls.len(), 1);
        let (authorization, name) = &state.calls[0];
        assert_eq!(authorization, &format!("Bearer {TOKEN}"));
        assert_eq!(name.len(), 36, "the runner calls itself {name}");
        assert!(state.left);
    });
}

#[tokio::test(flavor = "multi_thread")]
async fn a_later_turn_continues_the_session_of_the_harness() {
    let home = Home::new();
    let first = Supervisor::start("say one").await;
    assert!(run(&first, &home).await.status.success());
    let session = first.state(|state| match &state.reports[0] {
        Report::Started(started) => started.harness_session_id.clone(),
        other => panic!("the first report is {other:?}"),
    });

    let second = Supervisor::start("recall").await;
    second.state(|state| state.harness_session = Some(session.clone()));
    assert!(run(&second, &home).await.status.success());
    second.state(|state| {
        assert_eq!(state.said(), "say one");
        match &state.reports[0] {
            Report::Started(started) => assert_eq!(started.harness_session_id, session),
            other => panic!("the first report is {other:?}"),
        }
    });

    // In another home the harness does not know the session. The turn fails
    // for that reason, before the harness is given the message.
    let elsewhere = Supervisor::start("recall").await;
    elsewhere.state(|state| state.harness_session = Some(session.clone()));
    let ended = run(&elsewhere, &Home::new()).await;
    assert!(
        ended.failed("resume_failed", "the session is not known"),
        "{}",
        ended.stderr
    );
    assert!(elsewhere.state(|state| state.reports.is_empty()));
}

#[tokio::test(flavor = "multi_thread")]
async fn a_turn_that_the_harness_fails_says_why() {
    let home = Home::new();
    let supervisor = Supervisor::start("say Hello ; fail the model is out of order").await;
    let ended = run(&supervisor, &home).await;
    assert!(
        ended.failed("harness_error", "the model is out of order"),
        "{}",
        ended.stderr
    );
    // What the agent did before that is reported all the same.
    supervisor.state(|state| {
        assert_eq!(state.kinds(), ["started", "message"]);
        assert!(state.left);
    });
}

#[tokio::test(flavor = "multi_thread")]
async fn a_runner_that_is_not_set_up_says_so() {
    let supervisor = Supervisor::start("say Hello").await;
    let home = Home::new();
    for (variable, value, reason, message) in [
        (
            "AVIARY_HARNESS_COMMAND",
            None,
            "runner_error",
            "AVIARY_HARNESS_COMMAND is not set",
        ),
        (
            "AVIARY_HARNESS_COMMAND",
            Some("/no/such/harness"),
            "harness_error",
            "",
        ),
        (
            "AVIARY_TURN_TOKEN",
            None,
            "runner_error",
            "AVIARY_TURN_TOKEN is not set",
        ),
        (
            "AVIARY_GATEWAY_URL",
            None,
            "runner_error",
            "AVIARY_GATEWAY_URL is not set",
        ),
    ] {
        let mut command = runner(&supervisor, &home);
        match value {
            Some(value) => command.env(variable, value),
            None => command.env_remove(variable),
        };
        let ended = start(command).end().await;
        assert!(
            ended.failed(reason, message),
            "with {variable} as {value:?}: {}",
            ended.stderr
        );
    }
    assert!(supervisor.state(|state| state.reports.is_empty()));
}

#[tokio::test(flavor = "multi_thread")]
async fn the_instructions_are_where_the_harness_reads_them() {
    let home = Home::new();
    let told = Supervisor::start("instructions").await;
    told.state(|state| state.instructions = "Be brief.".to_owned());
    let mut command = runner(&told, &home);
    command.env("AVIARY_INSTRUCTIONS_FILE", home.instructions());
    assert!(start(command).end().await.status.success());
    assert_eq!(told.state(|state| state.said()), "Be brief.");

    // The file may be in the home of the session, where it would still say
    // what an earlier turn was told.
    let untold = Supervisor::start("instructions").await;
    let mut command = runner(&untold, &home);
    command.env("AVIARY_INSTRUCTIONS_FILE", home.instructions());
    assert!(start(command).end().await.status.success());
    assert_eq!(untold.state(|state| state.said()), "no instructions");

    // A harness image that names no file runs agents without instructions,
    // and no others.
    let plain = Supervisor::start("say Hello").await;
    assert!(run(&plain, &home).await.status.success());
    let nowhere = Supervisor::start("say Hello").await;
    nowhere.state(|state| state.instructions = "Be brief.".to_owned());
    let ended = run(&nowhere, &home).await;
    assert!(
        ended.failed("runner_error", "AVIARY_INSTRUCTIONS_FILE"),
        "{}",
        ended.stderr
    );
    assert!(nowhere.state(|state| state.reports.is_empty()));
}

#[tokio::test(flavor = "multi_thread")]
async fn the_long_texts_of_a_tool_call_are_cut_as_the_installation_says() {
    let home = Home::new();
    let output = |state: &support::State| -> String {
        let update = state.reports.iter().find_map(|report| match report {
            Report::ToolUpdate(update) => update.output.clone(),
            _ => None,
        });
        format!("{:?}", update.expect("the update of the tool call"))
    };

    let cut = Supervisor::start("big 5000").await;
    let mut command = runner(&cut, &home);
    command.env("AVIARY_MAX_TOOL_OUTPUT_BYTES", "100");
    assert!(start(command).end().await.status.success());
    let reported = cut.state(|state| output(state));
    assert!(
        reported.contains("... [4900 more bytes cut]") && reported.len() < 400,
        "{reported}"
    );

    // Zero is no limit, and so is no setting.
    for limit in [Some("0"), None] {
        let whole = Supervisor::start("big 5000").await;
        let mut command = runner(&whole, &home);
        if let Some(limit) = limit {
            command.env("AVIARY_MAX_TOOL_OUTPUT_BYTES", limit);
        }
        assert!(start(command).end().await.status.success());
        let reported = whole.state(|state| output(state));
        assert!(
            reported.contains(&"x".repeat(5000)),
            "with the limit {limit:?}"
        );
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn a_harness_that_asks_is_allowed_once() {
    let home = Home::new();
    let supervisor = Supervisor::start("ask").await;
    assert!(run(&supervisor, &home).await.status.success());
    assert_eq!(supervisor.state(|state| state.said()), "once");
}

#[tokio::test(flavor = "multi_thread")]
async fn a_turn_that_is_cancelled_is_stopped_by_the_harness() {
    let home = Home::new();
    let supervisor =
        Supervisor::start("drawl one two three four five six seven eight nine ten").await;
    let running = start(runner(&supervisor, &home));
    supervisor
        .until("the agent has not begun its answer", |state| {
            state.said().contains("two")
        })
        .await;
    supervisor.cancel();
    // The harness is told once, however often the runner is.
    supervisor.cancel();
    let ended = running.end().await;
    assert!(ended.status.success(), "{}", ended.stderr);
    supervisor.state(|state| {
        assert_eq!(state.stop_reason(), Some("cancelled"));
        let said = state.said();
        assert!(
            said.starts_with("one two ") && !said.contains("ten"),
            "{said}"
        );
        assert!(said.ends_with("[told to stop 1 times]"), "{said}");
    });
}

#[tokio::test(flavor = "multi_thread")]
async fn a_turn_that_is_cancelled_before_the_harness_is_ready_is_not_given_to_it() {
    let home = Home::new();
    let supervisor = Supervisor::start("say Hello").await;
    let mut command = runner(&supervisor, &home);
    command.env("FAKE_HARNESS_START_MS", "500");
    let running = start(command);
    supervisor
        .until("the runner has not called", |state| !state.calls.is_empty())
        .await;
    supervisor.cancel();
    let ended = running.end().await;
    assert!(ended.status.success(), "{}", ended.stderr);
    supervisor.state(|state| {
        assert!(state.reports.is_empty(), "{:?}", state.kinds());
        assert!(state.left);
    });
}

/// Kubernetes tells a pod to stop before it kills it. The runner has the
/// harness end the turn in that time, and its exit code tells that the turn
/// did not complete.
#[tokio::test(flavor = "multi_thread")]
async fn a_runner_that_is_told_to_stop_has_the_harness_end_the_turn() {
    let home = Home::new();
    let supervisor = Supervisor::start("tool sleep 30 ; sleep 30000").await;
    let running = start(runner(&supervisor, &home));
    supervisor
        .until("the agent has not called its tool", |state| {
            state.kinds().contains(&"tool update")
        })
        .await;
    running.terminate().await;
    let ended = running.end().await;
    assert_eq!(ended.status.code(), Some(143), "{}", ended.stderr);
    supervisor.state(|state| {
        assert_eq!(state.stop_reason(), Some("cancelled"));
        assert!(state.left);
    });

    // A turn that the harness completes all the same has completed.
    let deaf = Supervisor::start("deaf ; drawl one two three four five").await;
    let running = start(runner(&deaf, &home));
    deaf.until("the agent has not begun its answer", |state| {
        state.said().contains("one")
    })
    .await;
    running.terminate().await;
    let ended = running.end().await;
    assert!(ended.status.success(), "{}", ended.stderr);
    deaf.state(|state| {
        assert_eq!(state.stop_reason(), Some("end_turn"));
        assert_eq!(state.said(), "one two three four five");
    });
}

/// A turn is mostly waiting: for the model, for a tool. The runner must not
/// keep a CPU busy with it.
#[tokio::test(flavor = "multi_thread")]
async fn a_runner_that_waits_takes_no_cpu() {
    let home = Home::new();
    let supervisor = Supervisor::start("say Hello ; sleep 2000").await;
    let running = start(runner(&supervisor, &home));
    supervisor
        .until("the agent has not begun", |state| !state.said().is_empty())
        .await;
    let before = running.cpu();
    time::sleep(Duration::from_millis(1500)).await;
    let taken = running.cpu() - before;
    assert!(
        taken < Duration::from_millis(300),
        "the runner took {taken:?} of a CPU in a second and a half of waiting"
    );
    assert!(running.end().await.status.success());
}

#[tokio::test(flavor = "multi_thread")]
async fn a_supervisor_that_refuses_the_runner_ends_its_turn() {
    let home = Home::new();
    for code in [Code::Unauthenticated, Code::FailedPrecondition] {
        let supervisor = Supervisor::start("say Hello").await;
        supervisor.state(|state| state.refusal = Some((code, "the turn is not yours")));
        let ended = run(&supervisor, &home).await;
        assert!(
            ended.failed("supervisor_lost", "the turn is not yours"),
            "{}",
            ended.stderr
        );
        // The runner does not ask again.
        assert_eq!(supervisor.state(|state| state.calls.len()), 1);
    }
}

#[tokio::test]
async fn the_runner_makes_the_home_of_a_session() {
    let store = tempfile::tempdir().unwrap();
    let home = store.path().join("projects/p/agents/a/sessions/s/home");
    let made = Command::new(env!("CARGO_BIN_EXE_aviary-runner"))
        .arg("init-home")
        .arg(&home)
        .status()
        .await
        .unwrap();
    assert!(made.success());
    assert!(fs::metadata(home.join("workspace")).unwrap().is_dir());

    // Every turn of the session makes it, and so finds it there but for the
    // first.
    fs::write(home.join("workspace/kept"), "").unwrap();
    let again = Command::new(env!("CARGO_BIN_EXE_aviary-runner"))
        .arg("init-home")
        .arg(&home)
        .status()
        .await
        .unwrap();
    assert!(again.success());
    assert!(home.join("workspace/kept").exists());

    // A home that cannot be made fails the pod before the agent starts, and
    // the runner leaves the reason as it does for a turn.
    fs::write(store.path().join("file"), "").unwrap();
    let under_a_file = Command::new(env!("CARGO_BIN_EXE_aviary-runner"))
        .arg("init-home")
        .arg(store.path().join("file/home"))
        .output()
        .await
        .unwrap();
    let said = String::from_utf8_lossy(&under_a_file.stderr);
    assert!(!under_a_file.status.success());
    assert!(
        said.contains("reason: \"runner_error\"")
            && said.contains("the home of the session could not be made")
            && said.contains("file/home: Not a directory"),
        "{said}"
    );

    let wrong = Command::new(env!("CARGO_BIN_EXE_aviary-runner"))
        .arg("init")
        .output()
        .await
        .unwrap();
    assert!(!wrong.status.success());
    assert!(String::from_utf8_lossy(&wrong.stderr).contains("usage: aviary-runner"));
}
