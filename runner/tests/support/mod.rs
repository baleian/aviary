//! Test helpers: running the runner binary with the fake harness, and a client
//! for its HTTP API.

#![allow(dead_code)]

use std::path::{Path, PathBuf};
use std::process::Stdio;
use std::time::Duration;

use futures::StreamExt;
use reqwest::StatusCode;
use serde_json::{Value, json};
use tempfile::TempDir;
use tokio::io::{AsyncBufReadExt, BufReader};
use tokio::process::{Child, Command};
use tokio::time;

/// How long a test waits for the runner.
pub const PATIENCE: Duration = Duration::from_secs(20);

/// A temporary session home, laid out as in a sandbox.
pub struct Home(TempDir);

impl Home {
    pub fn new() -> Self {
        let home = TempDir::new().unwrap();
        std::fs::create_dir(home.path().join("workspace")).unwrap();
        Self(home)
    }

    pub fn path(&self) -> &Path {
        self.0.path()
    }

    /// A path for `AVIARY_INSTRUCTIONS_FILE`.
    pub fn instructions(&self) -> PathBuf {
        self.path().join("AGENTS.md")
    }
}

/// Returns a command that runs the runner with the fake harness in `home`,
/// on a free loopback port.
pub fn runner(home: &Home) -> Command {
    let runner = Path::new(env!("CARGO_BIN_EXE_aviary-runner"));
    let harness = runner.with_file_name("examples").join("fake-harness");
    assert!(
        harness.is_file(),
        "{} not found: run `cargo test` for the whole package to build it",
        harness.display()
    );
    let mut command = Command::new(runner);
    command
        .env_clear()
        .env("HOME", home.path())
        .env("AVIARY_RUNNER_ADDRESS", "127.0.0.1:0")
        .env("AVIARY_HARNESS_COMMAND", harness)
        .current_dir(home.path().join("workspace"))
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::inherit())
        .kill_on_drop(true);
    command
}

/// A runner process started by a test.
pub struct Running {
    pub child: Child,
    session: Session,
}

impl std::ops::Deref for Running {
    type Target = Session;

    fn deref(&self) -> &Session {
        &self.session
    }
}

/// A client of a runner's HTTP API.
pub struct Session {
    /// The runner's base URL.
    pub url: String,
    client: reqwest::Client,
}

impl Session {
    pub fn at(url: String) -> Self {
        let client = reqwest::Client::new();
        Self { url, client }
    }
}

/// Starts the runner and waits until it listens.
pub async fn start(mut command: Command) -> Running {
    let mut child = command.spawn().unwrap();
    let mut said = BufReader::new(child.stdout.take().unwrap()).lines();
    let line = time::timeout(PATIENCE, said.next_line())
        .await
        .expect("timed out waiting for the runner to listen")
        .unwrap()
        .expect("runner exited before listening");
    let address = line
        .strip_prefix("listening on ")
        .unwrap_or_else(|| panic!("runner printed {line:?}, want listening on <address>"));
    Running {
        child,
        session: Session::at(format!("http://{address}")),
    }
}

/// A turn event, as read from the event stream.
#[derive(Clone, Debug, PartialEq)]
pub struct Event {
    pub seq: u64,
    pub name: String,
    pub data: Value,
}

impl Session {
    /// Starts a turn with only a message.
    pub async fn turn(&self, id: &str, text: &str) -> StatusCode {
        self.turn_with(id, json!({"text": text})).await
    }

    /// Starts a turn with the given request body.
    pub async fn turn_with(&self, id: &str, start: Value) -> StatusCode {
        self.sent(
            self.client
                .put(format!("{}/turns/{id}", self.url))
                .json(&start),
        )
        .await
    }

    pub async fn cancel(&self, id: &str) -> StatusCode {
        self.sent(self.client.post(format!("{}/turns/{id}/cancel", self.url)))
            .await
    }

    pub async fn answer(&self, id: &str, request: &str, option: &str) -> StatusCode {
        self.sent(
            self.client
                .put(format!("{}/turns/{id}/approvals/{request}", self.url))
                .json(&json!({"option_id": option})),
        )
        .await
    }

    async fn sent(&self, request: reqwest::RequestBuilder) -> StatusCode {
        time::timeout(PATIENCE, request.send())
            .await
            .expect("timed out waiting for the runner")
            .unwrap()
            .status()
    }

    /// Opens the turn's event stream after event number `after`, or from the
    /// start if it is 0.
    pub async fn follow(&self, id: &str, after: u64) -> Following {
        let mut request = self.client.get(format!("{}/turns/{id}/events", self.url));
        if after > 0 {
            request = request.header("Last-Event-ID", after);
        }
        let answer = time::timeout(PATIENCE, request.send())
            .await
            .expect("timed out waiting for the runner")
            .unwrap();
        Following {
            status: answer.status(),
            bytes: Box::pin(
                answer
                    .bytes_stream()
                    .map(|bytes| bytes.map(|bytes| bytes.to_vec())),
            ),
            read: String::new(),
        }
    }

    /// Returns all events of the turn, once it has ended.
    pub async fn events(&self, id: &str) -> Vec<Event> {
        let mut following = self.follow(id, 0).await;
        assert_eq!(following.status, StatusCode::OK, "GET /turns/{id}/events");
        let mut events = Vec::new();
        while let Some(event) = following.next().await {
            events.push(event);
        }
        events
    }

    /// Runs a turn to its end and returns its events.
    pub async fn run(&self, id: &str, text: &str) -> Vec<Event> {
        assert_eq!(self.turn(id, text).await, StatusCode::NO_CONTENT);
        self.events(id).await
    }
}

/// An open event stream of a turn.
pub struct Following {
    pub status: StatusCode,
    bytes: std::pin::Pin<Box<dyn futures::Stream<Item = reqwest::Result<Vec<u8>>> + Send>>,
    read: String,
}

impl Following {
    /// Returns the next event, or `None` when the stream ends.
    pub async fn next(&mut self) -> Option<Event> {
        loop {
            // An event ends with a blank line. Keep-alive comments, which
            // start with a colon, have no fields and are skipped.
            while let Some(end) = self.read.find("\n\n") {
                let block: String = self.read.drain(..end + 2).collect();
                let field = |name: &str| {
                    block
                        .lines()
                        .find_map(|line| line.strip_prefix(name))
                        .map(|value| value.trim_start().to_owned())
                };
                if let (Some(seq), Some(name), Some(data)) =
                    (field("id:"), field("event:"), field("data:"))
                {
                    return Some(Event {
                        seq: seq.parse().unwrap(),
                        name,
                        data: serde_json::from_str(&data).unwrap(),
                    });
                }
            }
            let bytes = time::timeout(PATIENCE, self.bytes.next())
                .await
                .expect("timed out waiting for an event")?;
            self.read
                .push_str(&String::from_utf8(bytes.unwrap()).unwrap());
        }
    }
}

/// Returns the agent's message: the text of the message.delta events.
pub fn said(events: &[Event]) -> String {
    events
        .iter()
        .filter(|event| event.name == "message.delta")
        .filter_map(|event| event.data["text"].as_str())
        .collect()
}

pub fn names(events: &[Event]) -> Vec<&str> {
    events.iter().map(|event| event.name.as_str()).collect()
}

/// Returns the last event, which ends the turn.
pub fn ending(events: &[Event]) -> &Event {
    events.last().expect("turn has no events")
}
