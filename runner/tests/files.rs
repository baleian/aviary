//! Tests of the files that the messages of a session carry.

mod support;

use std::fs;

use reqwest::StatusCode;

use support::{Home, runner, start};

#[tokio::test(flavor = "multi_thread")]
async fn file_of_a_message_is_written_below_the_attachments_of_the_home() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    let files = home.path().join(".attachments");

    assert_eq!(
        runner.file("3/notes.txt", "to do").await,
        StatusCode::NO_CONTENT
    );
    assert_eq!(
        fs::read_to_string(files.join("3/notes.txt")).unwrap(),
        "to do"
    );
    // A name is taken as it is, and a file as large as it comes.
    let large = vec![b'a'; 5 << 20];
    assert_eq!(
        runner
            .file("3/%EB%B3%B4%EA%B3%A0%EC%84%9C%20(2).pdf", large.clone())
            .await,
        StatusCode::NO_CONTENT
    );
    assert_eq!(fs::read(files.join("3/보고서 (2).pdf")).unwrap(), large);
    // Sent again, a file replaces the one that is there.
    assert_eq!(
        runner.file("3/notes.txt", "done").await,
        StatusCode::NO_CONTENT
    );
    assert_eq!(
        fs::read_to_string(files.join("3/notes.txt")).unwrap(),
        "done"
    );
    let mut written: Vec<_> = fs::read_dir(files.join("3"))
        .unwrap()
        .map(|entry| entry.unwrap().file_name().into_string().unwrap())
        .collect();
    written.sort();
    assert_eq!(written, ["notes.txt", "보고서 (2).pdf"]);
}

#[tokio::test(flavor = "multi_thread")]
async fn file_is_not_written_outside_the_attachments_of_the_home() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    for path in [
        "..%2Fworkspace%2Fnotes.txt",
        "3%2F..%2F..%2Fnotes.txt",
        "%2Fetc%2Fpasswd",
    ] {
        assert_eq!(
            runner.file(path, "outside").await,
            StatusCode::BAD_REQUEST,
            "{path}"
        );
    }
    assert!(!home.path().join("workspace/notes.txt").exists());
    assert!(!home.path().join("notes.txt").exists());
}
