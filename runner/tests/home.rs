//! Tests of the archive of the home, with the fake harness.

mod support;

use std::fs;
use std::io;
use std::time::Duration;

use futures::channel::mpsc;
use reqwest::{Body, StatusCode};
use tokio::time;

use support::{Home, PATIENCE, names, runner, said, start};

#[tokio::test(flavor = "multi_thread")]
async fn session_continues_in_a_home_unpacked_from_its_archive() {
    let home = Home::new();
    let first = start(runner(&home)).await;
    let began = first.run("t1", "say one").await;
    fs::write(home.path().join("workspace/notes"), "of the agent").unwrap();
    let (status, archive) = first.packed().await;
    assert_eq!(status, StatusCode::OK);
    drop(first);

    // The session's next sandbox: another home, and another runner.
    let next = Home::new();
    let second = start(runner(&next)).await;
    assert_eq!(second.unpack(archive).await, StatusCode::NO_CONTENT);
    assert_eq!(
        fs::read_to_string(next.path().join("workspace/notes")).unwrap(),
        "of the agent"
    );
    let resumed = second.run("t2", "recall").await;
    assert_eq!(said(&resumed), "say one");
    assert_eq!(
        resumed[0].data["harness_session_id"],
        began[0].data["harness_session_id"]
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn home_is_packed_between_turns_and_unpacked_only_before_the_first() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    let (_, archive) = runner.packed().await;
    assert_eq!(
        runner.unpack(archive.clone()).await,
        StatusCode::NO_CONTENT,
        "before the first turn, an archive is unpacked as often as it is sent"
    );
    assert_eq!(runner.unpack(archive.clone()).await, StatusCode::NO_CONTENT);

    assert_eq!(
        runner.turn("t1", "sleep 60000").await,
        StatusCode::NO_CONTENT
    );
    let mut following = runner.follow("t1", 0).await;
    assert_eq!(following.next().await.unwrap().name, "started");
    assert_eq!(runner.packed().await.0, StatusCode::CONFLICT);
    assert_eq!(runner.unpack(archive.clone()).await, StatusCode::CONFLICT);

    assert_eq!(runner.cancel("t1").await, StatusCode::NO_CONTENT);
    while following.next().await.is_some() {}
    assert_eq!(runner.packed().await.0, StatusCode::OK);
    assert_eq!(runner.unpack(archive).await, StatusCode::CONFLICT);
}

#[tokio::test(flavor = "multi_thread")]
async fn archive_that_is_not_whole_is_refused_and_a_whole_one_is_unpacked_after_it() {
    let home = Home::new();
    fs::write(home.path().join("workspace/notes"), "of the agent").unwrap();
    let first = start(runner(&home)).await;
    let (_, archive) = first.packed().await;

    let next = Home::new();
    let second = start(runner(&next)).await;
    for broken in [&b"not an archive"[..], &archive[..archive.len() - 3], &[]] {
        assert_eq!(
            second.unpack(broken.to_vec()).await,
            StatusCode::INTERNAL_SERVER_ERROR
        );
    }
    assert_eq!(second.unpack(archive).await, StatusCode::NO_CONTENT);
    assert_eq!(
        fs::read_to_string(next.path().join("workspace/notes")).unwrap(),
        "of the agent"
    );
    let events = second.run("t1", "say done").await;
    assert_eq!(names(&events), ["started", "message.delta", "finished"]);
}

#[tokio::test(flavor = "multi_thread")]
async fn no_turn_starts_while_the_home_is_unpacked() {
    let home = Home::new();
    // Does not compress, so that half of the archive has half of the file.
    let mut state = 0x2545_f491_4f6c_dd1d_u64;
    let large: Vec<u8> = (0..4 << 20)
        .map(|_| {
            state ^= state << 13;
            state ^= state >> 7;
            state ^= state << 17;
            state as u8
        })
        .collect();
    fs::write(home.path().join("workspace/large"), &large).unwrap();
    let first = start(runner(&home)).await;
    let (_, archive) = first.packed().await;

    let next = Home::new();
    let second = start(runner(&next)).await;
    let (parts, body) = mpsc::unbounded::<io::Result<Vec<u8>>>();
    let (half, rest) = archive.split_at(archive.len() / 2);
    parts.unbounded_send(Ok(half.to_vec())).unwrap();
    let unpacking = second.unpack(Body::wrap_stream(body));
    let turns = async {
        // The unpacking has begun once a file of the archive is there.
        let unpacked = next.path().join("workspace/large");
        time::timeout(PATIENCE, async {
            while !unpacked.exists() {
                time::sleep(Duration::from_millis(5)).await;
            }
        })
        .await
        .expect("timed out waiting for the unpacking to begin");
        assert_eq!(second.turn("t1", "say done").await, StatusCode::CONFLICT);
        parts.unbounded_send(Ok(rest.to_vec())).unwrap();
        drop(parts);
    };
    let (unpacked, ()) = tokio::join!(unpacking, turns);
    assert_eq!(unpacked, StatusCode::NO_CONTENT);
    assert_eq!(
        fs::read(next.path().join("workspace/large")).unwrap(),
        large
    );
    let events = second.run("t1", "say done").await;
    assert_eq!(said(&events), "done");
}
