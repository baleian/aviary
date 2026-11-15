//! A runner that loses its Supervisor goes on with the one that it reaches
//! next, and sends again what no Supervisor has recorded.

mod support;

use std::time::{Duration, Instant};

use tokio::time;
use tonic::Code;

use support::{Home, PATIENCE, Supervisor, runner, start};

/// The message of a turn in which the agent says so many words slowly, and
/// the words.
fn drawl(words: usize) -> (String, String) {
    let words: Vec<String> = (1..=words).map(|word| format!("w{word}")).collect();
    (format!("drawl {}", words.join(" ")), words.join(" "))
}

#[tokio::test(flavor = "multi_thread")]
async fn a_turn_goes_on_when_its_supervisor_is_gone_for_a_while() {
    let home = Home::new();
    let (message, words) = drawl(20);
    let mut supervisor = Supervisor::start(&message).await;
    let running = start(runner(&supervisor, &home));
    supervisor
        .until("the agent has not begun its answer", |state| {
            state.said().contains("w3")
        })
        .await;

    supervisor.go_away().await;
    time::sleep(Duration::from_millis(700)).await;
    supervisor.come_back().await;

    let ended = running.end().await;
    assert!(ended.status.success(), "{}", ended.stderr);
    supervisor.state(|state| {
        // The answer is recorded whole and once.
        assert_eq!(state.said(), words);
        assert_eq!(state.stop_reason(), Some("end_turn"));
        assert!(state.calls.len() >= 2 && state.left);
        // The runner that comes back says that it is the one that left.
        assert!(state.calls.iter().all(|call| call == &state.calls[0]));
    });
}

#[tokio::test(flavor = "multi_thread")]
async fn a_turn_fails_when_no_supervisor_comes_for_too_long() {
    let home = Home::new();
    let (message, _) = drawl(100);
    let mut supervisor = Supervisor::start(&message).await;
    let mut command = runner(&supervisor, &home);
    command.env("AVIARY_MAX_RECONNECT_SECONDS", "1");
    let running = start(command);
    supervisor
        .until("the agent has not begun its answer", |state| {
            state.said().contains("w3")
        })
        .await;

    supervisor.go_away().await;
    let gone = Instant::now();
    let ended = running.end().await;
    assert!(
        ended.failed("supervisor_lost", "no Supervisor for 1 seconds"),
        "{}",
        ended.stderr
    );
    assert!(
        gone.elapsed() >= Duration::from_secs(1) && gone.elapsed() < Duration::from_secs(3),
        "the runner gave up after {:?}",
        gone.elapsed()
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn a_turn_without_a_limit_waits_for_a_supervisor() {
    let home = Home::new();
    let (message, words) = drawl(10);
    let mut supervisor = Supervisor::start(&message).await;
    let mut command = runner(&supervisor, &home);
    command.env("AVIARY_MAX_RECONNECT_SECONDS", "0");
    let mut running = start(command);
    supervisor
        .until("the agent has not begun its answer", |state| {
            state.said().contains("w3")
        })
        .await;

    supervisor.go_away().await;
    time::sleep(Duration::from_millis(2500)).await;
    assert!(
        running.runs(),
        "the runner gave up although it may wait for any time"
    );
    supervisor.come_back().await;
    let ended = running.end().await;
    assert!(ended.status.success(), "{}", ended.stderr);
    assert_eq!(supervisor.state(|state| state.said()), words);
}

/// What the agent did is reported also when the turn fails: the runner holds
/// it until a Supervisor has recorded it.
#[tokio::test(flavor = "multi_thread")]
async fn the_last_reports_of_a_turn_that_fails_are_not_lost() {
    let home = Home::new();
    let mut supervisor = Supervisor::start("say one two ; fail broken").await;
    supervisor.state(|state| state.holding = true);
    let mut running = start(runner(&supervisor, &home));
    supervisor
        .until("the agent has not said its words", |state| {
            state.said() == "one two"
        })
        .await;
    // The harness has failed by now, and no Supervisor has answered for
    // what the agent said.
    time::sleep(Duration::from_millis(500)).await;
    assert!(
        running.runs(),
        "the runner ended with reports that no Supervisor has answered for"
    );

    // The Supervisor dies with them.
    supervisor.go_away().await;
    supervisor.state(|state| {
        state.reports.clear();
        state.last = 0;
        state.holding = false;
    });
    supervisor.come_back().await;
    let ended = running.end().await;
    assert!(ended.failed("harness_error", "broken"), "{}", ended.stderr);
    assert_eq!(supervisor.state(|state| state.said()), "one two");
}

/// The Supervisor that a runner calls tells it again to stop a turn that was
/// cancelled. The harness has been told.
#[tokio::test(flavor = "multi_thread")]
async fn a_harness_is_told_once_to_stop_however_often_its_runner_is() {
    let home = Home::new();
    let (message, _) = drawl(100);
    let mut supervisor = Supervisor::start(&message).await;
    let mut command = runner(&supervisor, &home);
    command.env("FAKE_HARNESS_STOP_MS", "1000");
    let running = start(command);
    supervisor
        .until("the agent has not begun its answer", |state| {
            state.said().contains("w3")
        })
        .await;

    supervisor.cancel();
    time::sleep(Duration::from_millis(100)).await;
    supervisor.go_away().await;
    supervisor.come_back().await;
    supervisor
        .until("the runner has not called again", |state| {
            state.calls.len() >= 2
        })
        .await;
    supervisor.cancel();

    let ended = running.end().await;
    assert!(ended.status.success(), "{}", ended.stderr);
    supervisor.state(|state| {
        assert_eq!(state.stop_reason(), Some("cancelled"));
        assert!(
            state.said().ends_with("[told to stop 1 times]"),
            "{}",
            state.said()
        );
    });
}

#[tokio::test(flavor = "multi_thread")]
async fn a_supervisor_that_ends_a_call_for_good_ends_the_turn() {
    for (code, calls_again) in [
        (Code::DataLoss, false),
        (Code::FailedPrecondition, false),
        (Code::Unavailable, true),
        (Code::Internal, true),
    ] {
        let home = Home::new();
        let (message, words) = drawl(8);
        let supervisor = Supervisor::start(&message).await;
        let running = start(runner(&supervisor, &home));
        supervisor
            .until("the agent has not begun its answer", |state| {
                state.said().contains("w3")
            })
            .await;
        supervisor.end_call(code, "the Supervisor says why");
        let ended = running.end().await;
        if calls_again {
            assert!(ended.status.success(), "after {code:?}: {}", ended.stderr);
            supervisor.state(|state| {
                assert_eq!(state.said(), words, "after {code:?}");
                assert_eq!(state.calls.len(), 2, "after {code:?}");
            });
        } else {
            assert!(
                ended.failed("supervisor_lost", "the Supervisor says why"),
                "after {code:?}: {}",
                ended.stderr
            );
            assert_eq!(
                supervisor.state(|state| state.calls.len()),
                1,
                "after {code:?}"
            );
        }
    }
}

/// A Supervisor may die with reports that it was given and has not
/// recorded. The runner holds what it has no answer for, and sends it to the
/// next one.
#[tokio::test(flavor = "multi_thread")]
async fn reports_that_were_sent_and_not_recorded_are_sent_again() {
    let home = Home::new();
    let (message, words) = drawl(15);
    let mut supervisor = Supervisor::start(&message).await;
    supervisor.state(|state| state.holding = true);
    let running = start(runner(&supervisor, &home));
    supervisor
        .until("the agent has not begun its answer", |state| {
            state.said().contains("w5")
        })
        .await;

    supervisor.go_away().await;
    supervisor.state(|state| {
        state.reports.truncate(2);
        state.last = 2;
        state.holding = false;
    });
    supervisor.come_back().await;
    let ended = running.end().await;
    assert!(ended.status.success(), "{}", ended.stderr);
    assert_eq!(supervisor.state(|state| state.said()), words);
}

/// The time without a Supervisor is counted from when the runner lost one,
/// not from when it first lost one.
#[tokio::test(flavor = "multi_thread")]
async fn a_supervisor_that_was_found_again_may_be_lost_again() {
    let home = Home::new();
    let (message, words) = drawl(40);
    let mut supervisor = Supervisor::start(&message).await;
    let mut command = runner(&supervisor, &home);
    command.env("AVIARY_MAX_RECONNECT_SECONDS", "2");
    let running = start(command);
    for word in ["w3 ", "w25 "] {
        supervisor
            .until("the agent has not come so far in its answer", |state| {
                state.said().contains(word)
            })
            .await;
        supervisor.go_away().await;
        time::sleep(Duration::from_millis(600)).await;
        supervisor.come_back().await;
    }
    let ended = running.end().await;
    assert!(ended.status.success(), "{}", ended.stderr);
    assert_eq!(supervisor.state(|state| state.said()), words);
}

/// A Supervisor that has not recorded what an earlier one answered for has
/// lost a part of the turn. The runner no longer holds that part.
#[tokio::test(flavor = "multi_thread")]
async fn a_turn_whose_reports_a_supervisor_lost_fails() {
    let home = Home::new();
    let (message, _) = drawl(100);
    let mut supervisor = Supervisor::start(&message).await;
    let running = start(runner(&supervisor, &home));
    supervisor
        .until("the agent has not begun its answer", |state| {
            state.said().contains("w5")
        })
        .await;

    supervisor.go_away().await;
    supervisor.state(|state| {
        state.reports.truncate(2);
        state.last = 2;
    });
    supervisor.come_back().await;
    let ended = running.end().await;
    assert!(
        ended.failed("supervisor_lost", "a report is missing"),
        "{}",
        ended.stderr
    );
}

/// A Supervisor of an older release says nothing of what it has recorded and
/// gives no turn again. With it a turn runs as it did before: it completes,
/// or it fails when the call breaks.
#[tokio::test(flavor = "multi_thread")]
async fn a_turn_runs_with_a_supervisor_of_an_older_release() {
    let home = Home::new();
    let older = Supervisor::start("say Hello there ; tool ls").await;
    older.state(|state| state.older = true);
    let ended = start(runner(&older, &home)).end().await;
    assert!(ended.status.success(), "{}", ended.stderr);
    older.state(|state| {
        assert_eq!(
            state.kinds(),
            [
                "started",
                "message",
                "message",
                "tool call",
                "tool update",
                "finished"
            ]
        );
        assert!(state.left);
    });

    let (message, _) = drawl(100);
    let breaking = Supervisor::start(&message).await;
    breaking.state(|state| state.older = true);
    let running = start(runner(&breaking, &home));
    breaking
        .until("the agent has not begun its answer", |state| {
            state.said().contains("w3")
        })
        .await;
    breaking.end_call(Code::Unavailable, "the Supervisor is stopping");
    let ended = running.end().await;
    assert!(
        ended.failed("supervisor_lost", "the Supervisor is stopping"),
        "{}",
        ended.stderr
    );
    assert_eq!(breaking.state(|state| state.calls.len()), 1);
}

/// A call that gets no start would keep the runner from a Supervisor that
/// answers.
#[tokio::test(flavor = "multi_thread")]
async fn a_supervisor_that_gives_no_start_is_called_again() {
    let home = Home::new();
    let supervisor = Supervisor::start("say Hello").await;
    supervisor.state(|state| state.stalls = 1);
    let called = Instant::now();
    let ended = start(runner(&supervisor, &home)).end().await;
    assert!(ended.status.success(), "{}", ended.stderr);
    assert!(called.elapsed() < PATIENCE);
    supervisor.state(|state| {
        assert_eq!(state.calls.len(), 2);
        assert_eq!(state.said(), "Hello");
    });
}

/// Reports that no Supervisor answers for pile up in the runner. Beyond a
/// megabyte of them the harness is held back, so that the runner does not
/// grow with what the agent says.
#[tokio::test(flavor = "multi_thread")]
async fn the_harness_is_held_back_while_too_many_reports_wait() {
    const MEGABYTE: usize = 1 << 20;
    let home = Home::new();
    // Three megabytes, in pieces of ten kilobytes.
    let supervisor = Supervisor::start("flood 300 10000").await;
    supervisor.state(|state| state.holding = true);
    let running = start(runner(&supervisor, &home));
    supervisor
        .until("a megabyte of reports has not come", |state| {
            state.bytes >= MEGABYTE
        })
        .await;
    time::sleep(Duration::from_millis(500)).await;
    let came = supervisor.state(|state| state.bytes);
    assert!(
        came < MEGABYTE + 100_000,
        "{came} bytes of reports came while none was answered for"
    );

    supervisor.answer();
    let ended = running.end().await;
    assert!(ended.status.success(), "{}", ended.stderr);
    assert_eq!(supervisor.state(|state| state.said().len()), 300 * 10_000);
}
