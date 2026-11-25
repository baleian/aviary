//! Tests of the files that the messages of a session carry.

mod support;

use std::fs;

use reqwest::StatusCode;
use serde_json::json;

use support::{Home, runner, said, start};

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

#[tokio::test(flavor = "multi_thread")]
async fn prompt_links_the_files_of_its_message_after_its_text() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    let files = home.path().join(".attachments");
    let at = |path: &str| format!("file://{}/{path}", files.display());

    // After the text, in the order of the message, which is not that of the
    // names. A link says its name and where the file is, and not what the
    // file is.
    let start = json!({
        "text": "say with ; links",
        "files": ["1/notes-k.txt", "1/보고서 (2).pdf", "1/notes-a.txt", "1/100% #1?.txt"],
    });
    assert_eq!(runner.turn_with("t1", start).await, StatusCode::NO_CONTENT);
    assert_eq!(
        said(&runner.events("t1").await),
        [
            format!("withnotes-k.txt {}", at("1/notes-k.txt")),
            format!("보고서 (2).pdf {}", at("1/보고서 (2).pdf")),
            format!("notes-a.txt {}", at("1/notes-a.txt")),
            format!("100% #1?.txt {}", at("1/100%25 %231%3F.txt")),
        ]
        .join("\n")
    );

    // A message of files alone has no text: the prompt begins with a link.
    let start = json!({"text": "", "files": ["2/notes.txt"]});
    assert_eq!(runner.turn_with("t2", start).await, StatusCode::NO_CONTENT);
    assert_eq!(
        said(&runner.events("t2").await),
        format!("notes.txt {}", at("2/notes.txt"))
    );

    // A message without files has only its text: the files of the messages
    // before it are not linked again.
    assert_eq!(said(&runner.run("t3", "links").await), "none");

    // A file is below the directory of the files.
    for path in ["../workspace/notes.txt", "/etc/passwd", "3/..", ""] {
        let start = json!({"text": "links", "files": [path]});
        assert_eq!(
            runner.turn_with("t4", start).await,
            StatusCode::BAD_REQUEST,
            "{path}"
        );
    }
}
