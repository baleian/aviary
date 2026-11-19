//! What the tests run the runner with. The runner is the program as it is
//! built, the tests are who sends it the turns, and its harness is the fake
//! harness, which does what the message of a turn tells it.

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

/// How long a test waits for what is to happen.
pub const PATIENCE: Duration = Duration::from_secs(20);

/// The home of a session, as the sandbox of the session has it.
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

    /// The file that the harness reads its instructions from.
    pub fn instructions(&self) -> PathBuf {
        self.path().join("AGENTS.md")
    }
}

/// The runner of a session, set up as it is in the sandbox of the session,
/// on a port that it chooses.
pub fn runner(home: &Home) -> Command {
    let runner = Path::new(env!("CARGO_BIN_EXE_aviary-runner"));
    let harness = runner.with_file_name("examples").join("fake-harness");
    assert!(
        harness.is_file(),
        "{} is not built: run all the tests of the package, as `cargo test` does",
        harness.display()
    );
    let mut command = Command::new(runner);
    command
        .env_clear()
        .env("AVIARY_RUNNER_ADDRESS", "127.0.0.1:0")
        .env("AVIARY_HARNESS_COMMAND", harness)
        .current_dir(home.path().join("workspace"))
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::inherit())
        .kill_on_drop(true);
    command
}

/// A runner that the test started.
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

/// A runner as whoever sends it the turns of its session has it.
pub struct Session {
    /// Where the runner listens.
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
        .expect("the runner did not say where it listens")
        .unwrap()
        .expect("the runner ended before it listened");
    let address = line
        .strip_prefix("listening on ")
        .unwrap_or_else(|| panic!("the runner said {line:?}"));
    Running {
        child,
        session: Session::at(format!("http://{address}")),
    }
}

/// An event of a turn as the runner sent it.
#[derive(Clone, Debug, PartialEq)]
pub struct Event {
    pub seq: u64,
    pub name: String,
    pub data: Value,
}

impl Session {
    /// Starts the turn with the message, and with nothing else.
    pub async fn turn(&self, id: &str, text: &str) -> StatusCode {
        self.turn_with(id, json!({"text": text})).await
    }

    /// Starts the turn with what the JSON says.
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

    /// Answers the request of the turn for approval with the option.
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
            .expect("the runner did not answer")
            .unwrap()
            .status()
    }

    /// Reads the events of the turn that come after the one with the number.
    pub async fn follow(&self, id: &str, after: u64) -> Following {
        let mut request = self.client.get(format!("{}/turns/{id}/events", self.url));
        if after > 0 {
            request = request.header("Last-Event-ID", after);
        }
        let answer = time::timeout(PATIENCE, request.send())
            .await
            .expect("the runner did not answer")
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

    /// The events of the turn, all of them: it waits for the turn to end.
    pub async fn events(&self, id: &str) -> Vec<Event> {
        let mut following = self.follow(id, 0).await;
        assert_eq!(following.status, StatusCode::OK, "the events of {id}");
        let mut events = Vec::new();
        while let Some(event) = following.next().await {
            events.push(event);
        }
        events
    }

    /// Runs the turn to its end and returns its events.
    pub async fn run(&self, id: &str, text: &str) -> Vec<Event> {
        assert_eq!(self.turn(id, text).await, StatusCode::NO_CONTENT);
        self.events(id).await
    }
}

/// The events of a turn as they come.
pub struct Following {
    pub status: StatusCode,
    bytes: std::pin::Pin<Box<dyn futures::Stream<Item = reqwest::Result<Vec<u8>>> + Send>>,
    read: String,
}

impl Following {
    /// The next event, or none when the runner has ended its answer.
    pub async fn next(&mut self) -> Option<Event> {
        loop {
            // An event ends with an empty line. What starts with a colon is
            // there to keep the connection, and tells nothing.
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
                .expect("the runner sent no event and did not end its answer")?;
            self.read
                .push_str(&String::from_utf8(bytes.unwrap()).unwrap());
        }
    }
}

/// What the agent said in the events, as one text.
pub fn said(events: &[Event]) -> String {
    events
        .iter()
        .filter(|event| event.name == "message.delta")
        .filter_map(|event| event.data["text"].as_str())
        .collect()
}

/// The names of the events, in their order.
pub fn names(events: &[Event]) -> Vec<&str> {
    events.iter().map(|event| event.name.as_str()).collect()
}

/// The event with which the turn ended.
pub fn ending(events: &[Event]) -> &Event {
    events.last().expect("the turn has no events")
}
