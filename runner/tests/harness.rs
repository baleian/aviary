//! Tests that the harness images meet the contract in images/README.md.
//!
//! A session runs in a container of the image, started the way a sandbox
//! starts its pod, and the test sends turns to its runner. The containers are
//! on an internal network with mock-llm, which is both their model and their
//! forward proxy. As a proxy, it refuses every request.
//!
//! The tests need the images: run them with `make test-harness`.

mod support;

use std::future::Future;
use std::sync::Arc;
use std::sync::atomic::{AtomicU32, Ordering};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use reqwest::StatusCode;
use serde_json::{Value, json};
use tokio::process::Command;
use tokio::time;

use support::{Event, Following, Session, said};

/// How long a test waits for a container to start or for the next event of a
/// turn.
const PATIENCE: Duration = Duration::from_secs(120);

/// mock-llm's API key. With no Egress Proxy in between, the harness is given
/// the real key.
const KEY: &str = "the-key-of-the-model";

const PROXY: &str = "http://turn:the-key-of-the-model@model:8080";

const MODEL_IMAGE: &str = "aviary-mock-llm:dev";
/// Forwards a port of the host to the runner, since the internal network
/// publishes no ports.
const FORWARDER_IMAGE: &str =
    "alpine/socat:1.8.1.3@sha256:71131702f671a88b3808a70b7330f9679cd9b9933adcafadc191508edfe55bba";

fn images() -> Vec<String> {
    let images = std::env::var("AVIARY_HARNESS_IMAGES")
        .expect("AVIARY_HARNESS_IMAGES names no harness images: run `make test-harness`");
    images.split_whitespace().map(str::to_owned).collect()
}

/// Runs docker and returns its trimmed standard output. Panics if it fails.
async fn docker(arguments: &[&str]) -> String {
    let ran = Command::new("docker")
        .args(arguments)
        .output()
        .await
        .expect("docker");
    assert!(
        ran.status.success(),
        "docker {arguments:?} failed: {}",
        String::from_utf8_lossy(&ran.stderr)
    );
    String::from_utf8_lossy(&ran.stdout).trim().to_owned()
}

/// Returns a unique word that starts with `prefix`.
fn word(prefix: &str) -> String {
    static MADE: AtomicU32 = AtomicU32::new(0);
    let since = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default();
    format!(
        "{prefix}-{:x}{:x}",
        since.as_nanos() & 0xffff_ffff,
        MADE.fetch_add(1, Ordering::Relaxed)
    )
}

/// The test environment of a harness image: a network of its own, with
/// mock-llm on it.
struct Bench {
    image: String,
    /// Unique on the machine. It names and labels the Docker objects of the
    /// bench.
    name: String,
    made: AtomicU32,
}

/// A turn's message, with the agent's instructions and approval mode.
#[derive(Default)]
struct Message<'a> {
    text: &'a str,
    instructions: &'a str,
    /// "ask", or empty.
    approval: &'a str,
}

impl Bench {
    async fn new(image: &str) -> Arc<Self> {
        let bench = Arc::new(Self {
            image: image.to_owned(),
            name: word("aviary-test"),
            made: AtomicU32::new(0),
        });
        let label = bench.label();
        // No route out, as from a sandbox.
        docker(&[
            "network",
            "create",
            "--internal",
            "--label",
            &label,
            &bench.name,
        ])
        .await;
        let model = format!("{}-model", bench.name);
        docker(&[
            "run",
            "--detach",
            "--name",
            &model,
            "--label",
            &label,
            "--network",
            &bench.name,
            "--network-alias",
            "model",
            "--env",
            &format!("MOCK_LLM_API_KEY={KEY}"),
            MODEL_IMAGE,
        ])
        .await;
        let began = Instant::now();
        while !docker(&["logs", &model]).await.contains("listening") {
            assert!(began.elapsed() < PATIENCE, "the model server did not start");
            time::sleep(Duration::from_millis(50)).await;
        }
        bench
    }

    fn label(&self) -> String {
        format!("aviary-test={}", self.name)
    }

    fn next(&self, what: &str) -> String {
        format!(
            "{}-{what}-{}",
            self.name,
            self.made.fetch_add(1, Ordering::Relaxed)
        )
    }

    /// Returns the hosts that the harness and the agent's tools asked the
    /// proxy for.
    async fn asked(&self) -> Vec<String> {
        let log = docker(&["logs", &format!("{}-model", self.name)]).await;
        let mut hosts: Vec<String> = log
            .lines()
            .filter_map(|line| serde_json::from_str::<Value>(line).ok())
            .filter(|line| line["msg"] == "asked for")
            .filter_map(|line| line["host"].as_str().map(str::to_owned))
            .collect();
        hosts.sort();
        hosts.dedup();
        hosts
    }

    /// Starts a new session, with a new volume as its home.
    async fn session(&self) -> Sitting {
        let home = self.next("home");
        docker(&["volume", "create", "--label", &self.label(), &home]).await;
        self.sitting(home).await
    }

    /// Moves the session to a new container with the same home, as when a
    /// sandbox wakes in a new pod.
    async fn again(&self, sitting: Sitting) -> Sitting {
        docker(&["rm", "--force", &sitting.harness]).await;
        self.sitting(sitting.home).await
    }

    /// Starts a session container on the `home` volume, run as a sandbox runs
    /// its pod: as the agent's user, who cannot become root, in /.
    async fn sitting(&self, home: String) -> Sitting {
        let label = self.label();
        let harness = self.next("harness");
        docker(&[
            "run",
            "--detach",
            "--name",
            &harness,
            "--label",
            &label,
            "--network",
            &self.name,
            "--user",
            "10000:10000",
            "--cap-drop",
            "ALL",
            "--security-opt",
            "no-new-privileges",
            "--workdir",
            "/",
            "--volume",
            &format!("{home}:/home"),
            "--env",
            "AVIARY_MAX_TOOL_OUTPUT_BYTES=65536",
            &self.image,
            "aviary-session",
        ])
        .await;
        // The forwarder starts with its published port and joins the network
        // afterwards: Docker sometimes leaves the port unconnected when a
        // container starts with both.
        let forwarder = self.next("forwarder");
        docker(&[
            "run",
            "--detach",
            "--name",
            &forwarder,
            "--label",
            &label,
            "--publish",
            "127.0.0.1::8700",
            FORWARDER_IMAGE,
            "tcp-listen:8700,fork,reuseaddr",
            &format!("tcp-connect:{harness}:8700"),
        ])
        .await;
        docker(&["network", "connect", &self.name, &forwarder]).await;
        let port = docker(&["port", &forwarder, "8700/tcp"]).await;
        let sitting = Sitting {
            image: self.image.clone(),
            harness,
            home,
            runner: Session::at(format!("http://{port}")),
            turns: AtomicU32::new(0),
        };
        let began = Instant::now();
        while !sitting.listens().await {
            assert!(
                began.elapsed() < PATIENCE,
                "{}: the runner does not listen\n{}",
                self.image,
                sitting.log()
            );
            time::sleep(Duration::from_millis(50)).await;
        }
        sitting
    }
}

impl Drop for Bench {
    fn drop(&mut self) {
        let docker = |arguments: &[&str]| {
            let ran = std::process::Command::new("docker")
                .args(arguments)
                .output();
            String::from_utf8_lossy(&ran.map(|ran| ran.stdout).unwrap_or_default()).into_owned()
        };
        let filter = format!("label={}", self.label());
        for (list, remove) in [
            (
                vec!["ps", "--all", "--quiet"],
                vec!["rm", "--force", "--volumes"],
            ),
            (
                vec!["volume", "ls", "--quiet"],
                vec!["volume", "rm", "--force"],
            ),
            (vec!["network", "ls", "--quiet"], vec!["network", "rm"]),
        ] {
            let mut list = list;
            list.extend(["--filter", &filter]);
            let found = docker(&list);
            let mut remove = remove;
            remove.extend(found.split_whitespace());
            if found.trim().is_empty() {
                continue;
            }
            docker(&remove);
        }
    }
}

/// A running session: its container, which runs the runner, and its home
/// volume.
struct Sitting {
    image: String,
    harness: String,
    home: String,
    runner: Session,
    turns: AtomicU32,
}

impl Sitting {
    /// Reports whether the runner responds. The request times out, since a
    /// connection to a container that has just started can hang.
    async fn listens(&self) -> bool {
        let client = reqwest::Client::new();
        let asking = client
            .get(format!("{}/turns/none/events", self.runner.url))
            .send();
        let asked = time::timeout(Duration::from_secs(2), asking).await;
        matches!(asked, Ok(Ok(answer)) if answer.status() == StatusCode::NOT_FOUND)
    }

    /// Returns the container's log, which holds the output of the runner and
    /// the harness.
    fn log(&self) -> String {
        let logged = std::process::Command::new("docker")
            .args(["logs", &self.harness])
            .output()
            .expect("docker");
        format!(
            "{}{}",
            String::from_utf8_lossy(&logged.stdout),
            String::from_utf8_lossy(&logged.stderr)
        )
    }

    /// Starts the next turn of the session.
    async fn start(&self, message: Message<'_>) -> Turn<'_> {
        let id = format!("t{}", self.turns.fetch_add(1, Ordering::Relaxed) + 1);
        let mut environment = json!({
            "AVIARY_LLM_BASE_URL": "http://model:8080/v1",
            "AVIARY_LLM_API_KEY": KEY,
            "AVIARY_LLM_MODEL": "mock",
            // Everything else goes through the proxy.
            "HTTP_PROXY": PROXY, "HTTPS_PROXY": PROXY, "NO_PROXY": "localhost,127.0.0.1,::1,model",
            "http_proxy": PROXY, "https_proxy": PROXY, "no_proxy": "localhost,127.0.0.1,::1,model",
        });
        // Set only for an agent that asks, as the Supervisor does.
        if !message.approval.is_empty() {
            environment["AVIARY_APPROVAL"] = json!(message.approval);
        }
        let started = self
            .runner
            .turn_with(
                &id,
                json!({
                    "text": message.text,
                    "instructions": message.instructions,
                    "approval": message.approval,
                    "environment": environment,
                }),
            )
            .await;
        assert_eq!(started, StatusCode::NO_CONTENT, "{}: {id}", self.image);
        let following = self.runner.follow(&id, 0).await;
        assert_eq!(following.status, StatusCode::OK, "{}: {id}", self.image);
        Turn {
            sitting: self,
            id,
            following,
            events: Vec::new(),
        }
    }

    /// Runs the next turn of the session to its end.
    async fn run(&self, message: Message<'_>) -> Ended {
        self.start(message).await.end().await
    }

    /// Runs a turn with only a message.
    async fn say(&self, text: &str) -> Ended {
        let text = Message {
            text,
            ..Message::default()
        };
        self.run(text).await
    }

    /// Lists the files in the session's workspace.
    async fn files(&self) -> String {
        docker(&["exec", &self.harness, "ls", "/home/workspace"]).await
    }

    /// Returns the size of the session's home in megabytes.
    async fn megabytes(&self) -> u64 {
        let used = docker(&[
            "exec",
            &self.harness,
            "du",
            "--summarize",
            "--block-size=1M",
            "/home",
        ])
        .await;
        used.split_whitespace().next().unwrap().parse().unwrap()
    }

    /// Kills the harness between turns, as if it had crashed: every child of
    /// the runner, which is PID 1.
    async fn end_harness(&self) {
        let ended = "for stat in /proc/[0-9]*/stat; do read -r pid name state parent rest < $stat; \
                     [ \"$parent\" != 1 ] || kill \"$pid\"; done";
        docker(&["exec", &self.harness, "sh", "-c", ended]).await;
        time::sleep(Duration::from_millis(500)).await;
    }

    /// Returns the CPU seconds that the container has used.
    async fn cpu(&self) -> f64 {
        let stat = docker(&["exec", &self.harness, "cat", "/sys/fs/cgroup/cpu.stat"]).await;
        let used = stat
            .lines()
            .find_map(|line| line.strip_prefix("usage_usec "))
            .expect("the CPU time of the container");
        used.trim().parse::<f64>().unwrap() / 1e6
    }
}

/// A running turn.
struct Turn<'a> {
    sitting: &'a Sitting,
    id: String,
    following: Following,
    /// The events read so far.
    events: Vec<Event>,
}

/// A turn that has ended.
struct Ended {
    image: String,
    events: Vec<Event>,
    /// The container's log, for failure messages.
    log: String,
}

/// Returns the data of the tool events, concatenated.
fn tools(events: &[Event]) -> String {
    events
        .iter()
        .filter(|event| event.name.starts_with("tool."))
        .map(|event| event.data.to_string())
        .collect()
}

fn find<'a>(events: &'a [Event], name: &str) -> Option<&'a Event> {
    events.iter().find(|event| event.name == name)
}

impl Turn<'_> {
    /// Reads events until `holds` is true of them. Panics with `what` if the
    /// turn ends or times out first.
    async fn until(&mut self, what: &str, holds: impl Fn(&[Event]) -> bool) {
        while !holds(&self.events) {
            let read = time::timeout(PATIENCE, self.following.next()).await;
            match read {
                Ok(Some(event)) => self.events.push(event),
                _ => panic!(
                    "{}: {what}; the events are {:?}\n{}",
                    self.sitting.image,
                    self.events,
                    self.sitting.log()
                ),
            }
        }
    }

    async fn cancel(&mut self) {
        let told = self.sitting.runner.cancel(&self.id).await;
        assert_eq!(told, StatusCode::NO_CONTENT);
    }

    /// Answers the approval request with the option of the given kind.
    async fn answer(&mut self, kind: &str) {
        let asked = find(&self.events, "approval.requested").expect("a request for approval");
        let options = asked.data["options"].as_array().unwrap();
        let option = options
            .iter()
            .find(|option| option["kind"] == kind)
            .unwrap_or_else(|| panic!("the harness takes no {kind}: {}", asked.data));
        let answered = self
            .sitting
            .runner
            .answer(
                &self.id,
                asked.data["id"].as_str().unwrap(),
                option["id"].as_str().unwrap(),
            )
            .await;
        assert_eq!(answered, StatusCode::NO_CONTENT);
    }

    /// Waits for the turn to end.
    async fn end(mut self) -> Ended {
        loop {
            let read = time::timeout(PATIENCE, self.following.next()).await;
            match read {
                Ok(Some(event)) => self.events.push(event),
                Ok(None) => break,
                Err(_) => panic!(
                    "{}: the turn did not end; its events are {:?}\n{}",
                    self.sitting.image,
                    self.events,
                    self.sitting.log()
                ),
            }
        }
        Ended {
            image: self.sitting.image.clone(),
            events: self.events,
            log: self.sitting.log(),
        }
    }
}

impl Ended {
    /// Returns the stop reason if the turn finished.
    fn stopped(&self) -> Option<&str> {
        let last = self.events.last().filter(|last| last.name == "finished")?;
        last.data["stop_reason"].as_str()
    }

    /// Asserts that the turn finished with end_turn.
    fn completed(&self) -> &Self {
        assert!(
            self.stopped() == Some("end_turn"),
            "{}: the turn ended with {:?}\n{}",
            self.image,
            self.events.last(),
            self.log
        );
        self
    }

    /// Asserts that the turn failed for `reason`, with a message.
    fn failed(&self, reason: &str) -> &Self {
        let last = self.events.last().expect("the events of the turn");
        assert!(
            last.name == "failed"
                && last.data["reason"] == reason
                && last.data["message"]
                    .as_str()
                    .is_some_and(|message| !message.is_empty()),
            "{}: the turn ended with {last:?}, not as failed for the reason {reason}\n{}",
            self.image,
            self.log
        );
        self
    }

    /// Asserts that the agent's message contains `text`.
    fn says(&self, text: &str) -> &Self {
        let said = said(&self.events);
        assert!(
            said.contains(text),
            "{}: the agent said {said:?}, not {text:?}",
            self.image
        );
        self
    }

    /// Returns the harness session ID of the turn.
    fn session(&self) -> String {
        let started = find(&self.events, "started").expect("the start of the turn");
        started.data["harness_session_id"]
            .as_str()
            .unwrap()
            .to_owned()
    }

    /// Returns the outcome of the turn's approval request.
    fn resolved(&self) -> Option<&str> {
        find(&self.events, "approval.resolved")?.data["outcome"].as_str()
    }
}

/// Runs `check` for every harness image concurrently, each on a bench of its
/// own.
async fn each<Check, Checking>(check: Check)
where
    Check: Fn(Arc<Bench>) -> Checking,
    Checking: Future<Output = ()> + Send + 'static,
{
    let mut checks = Vec::new();
    for image in images() {
        let bench = Bench::new(&image).await;
        checks.push((image, tokio::spawn(check(bench))));
    }
    let mut failed = Vec::new();
    for (image, checked) in checks {
        if checked.await.is_err() {
            failed.push(image);
        }
    }
    assert!(failed.is_empty(), "the check failed for {failed:?}");
}

#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs the harness images"]
async fn a_session_goes_on_in_its_home_and_nowhere_else() {
    each(|bench| async move {
        let session = bench.session().await;
        let content = word("content");
        let write = format!("run printf {content} > note.txt");
        let first = session.say(&write).await;
        first.completed();
        // The command may be in the tool call or in an update of it.
        assert!(
            tools(&first.events).contains(&content),
            "{}: the agent called no tool with the command: {:?}",
            first.image,
            first.events
        );
        // A harness reports the usage of its last model response or of all of
        // them, and mock-llm reports the same usage for every response.
        let usage = &first.events.last().unwrap().data["usage"];
        let answers = usage["total_tokens"].as_i64().unwrap_or(0) / 18;
        assert!(
            answers >= 1
                && usage["input_tokens"] == 11 * answers
                && usage["output_tokens"] == 7 * answers,
            "{}: the harness reported the usage {usage}",
            first.image
        );

        let second = session.say("recall").await;
        second.completed().says(&write);
        assert_eq!(second.session(), first.session(), "{}", second.image);

        session.end_harness().await;
        let third = session.say("recall").await;
        third.completed().says(&write);
        assert_eq!(third.session(), first.session(), "{}", third.image);
        session
            .say("run cat note.txt")
            .await
            .completed()
            .says(&content);

        // Catches a harness that downloads large data into every new home.
        let megabytes = session.megabytes().await;
        assert!(
            megabytes < 50,
            "{}: the home of the session holds {megabytes} MB",
            bench.image
        );

        let elsewhere = bench.session().await;
        elsewhere.say("recall").await.completed().says("nothing");
    })
    .await;
}

#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs the harness images"]
async fn a_session_goes_on_in_another_pod_with_its_home() {
    each(|bench| async move {
        let session = bench.session().await;
        let content = word("content");
        let write = format!("run printf {content} > note.txt");
        let first = session.say(&write).await;
        first.completed();

        let later = bench.again(session).await;
        let recalled = later.say("recall").await;
        recalled.completed().says(&write);
        assert_eq!(recalled.session(), first.session(), "{}", recalled.image);
        later
            .say("run cat note.txt")
            .await
            .completed()
            .says(&content);
    })
    .await;
}

/// The agent cannot become root, and npm and pip install into its home.
#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs the harness images"]
async fn an_agent_is_a_user_who_installs_into_its_home() {
    each(|bench| async move {
        let session = bench.session().await;
        let who = session
            .say("run echo I am $(id -un) in $(pwd) with the home $HOME, and npm installs into $(npm config get prefix)")
            .await;
        who.completed();
        let told = tools(&who.events);
        assert!(
            told.contains("I am agent in /home/workspace with the home /home, and npm installs into /home/.local"),
            "{}: {told}",
            who.image
        );
        // mock-llm serves a package to install.
        let installed = session
            .say("run pip install http://model:8080/packages/aviary_probe-1.0-py3-none-any.whl && pip show aviary-probe")
            .await;
        installed.completed();
        let told = tools(&installed.events);
        assert!(
            told.contains("Location: /home/.local/lib/python3"),
            "{}: {told}",
            installed.image
        );
        let root = session
            .say("run sudo -n true || echo nobody becomes root here")
            .await;
        root.completed();
        let told = tools(&root.events);
        assert!(
            told.contains("nobody becomes root here"),
            "{}: {told}",
            root.image
        );
    })
    .await;
}

#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs the harness images"]
async fn the_instructions_of_an_agent_reach_the_model_in_every_turn() {
    each(|bench| async move {
        let session = bench.session().await;
        let plum = word("plum");
        let (find, instructions) = (
            format!("find {plum}"),
            format!("The word of the day is {plum}."),
        );
        for _ in 0..2 {
            let told = Message {
                text: &find,
                instructions: &instructions,
                ..Message::default()
            };
            session.run(told).await.completed().says("present");
        }
        session.end_harness().await;
        let told = Message {
            text: &find,
            instructions: &instructions,
            ..Message::default()
        };
        session.run(told).await.completed().says("present");

        let plain = bench.session().await;
        plain.say(&find).await.completed().says("absent");
    })
    .await;
}

/// How a turn ends after a refusal depends on the harness.
#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs the harness images"]
async fn an_agent_that_is_to_ask_runs_what_a_person_allows() {
    each(|bench| async move {
        let session = bench.session().await;
        let asked = |events: &[Event]| find(events, "approval.requested").is_some();
        let (allowed, refused, dropped) = (word("allowed"), word("refused"), word("dropped"));
        let ask = |text| Message {
            text,
            approval: "ask",
            ..Message::default()
        };

        let allow = format!("escalate touch {allowed}");
        let mut turn = session.start(ask(&allow)).await;
        turn.until("the harness asked for no approval", asked).await;
        let request = find(&turn.events, "approval.requested")
            .unwrap()
            .data
            .clone();
        let kinds: Vec<&str> = request["options"]
            .as_array()
            .unwrap()
            .iter()
            .filter_map(|option| option["kind"].as_str())
            .collect();
        assert!(
            request.to_string().contains(&allowed)
                && kinds.contains(&"allow_once")
                && kinds.contains(&"reject_once"),
            "{}: the harness asked {request}",
            bench.image
        );
        assert!(
            !session.files().await.contains(&allowed),
            "{}: the command ran before a person allowed it",
            bench.image
        );
        turn.answer("allow_once").await;
        let first = turn.end().await;
        first.completed();
        assert!(
            first.resolved() == Some("selected") && session.files().await.contains(&allowed),
            "{}: a command that a person allowed did not run: {:?}",
            first.image,
            first.events
        );

        let refuse = format!("escalate touch {refused}");
        let mut turn = session.start(ask(&refuse)).await;
        turn.until("the harness asked for no approval", asked).await;
        turn.answer("reject_once").await;
        let second = turn.end().await;
        assert!(
            second.stopped().is_some()
                && second.resolved() == Some("selected")
                && !session.files().await.contains(&refused),
            "{}: after a refusal the turn ended with {:?}\n{}",
            second.image,
            second.events,
            second.log
        );

        let drop = format!("escalate touch {dropped}");
        let mut turn = session.start(ask(&drop)).await;
        turn.until("the harness asked for no approval", asked).await;
        turn.cancel().await;
        let third = turn.end().await;
        assert!(
            third.stopped() == Some("cancelled")
                && third.resolved() == Some("cancelled")
                && !session.files().await.contains(&dropped),
            "{}: a turn that was cancelled while its harness asked ended with {:?}\n{}",
            third.image,
            third.events,
            third.log
        );

        session.run(ask("recall")).await.completed().says(&allow);
    })
    .await;
}

#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs the harness images"]
async fn a_turn_that_the_model_refuses_fails() {
    each(|bench| async move {
        let session = bench.session().await;
        session
            .say("fail the model is out of order")
            .await
            .failed("harness_error");
        session.say("say hello").await.completed().says("hello");
    })
    .await;
}

/// mock-llm refuses the first request with 503, as a busy model provider
/// does.
#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs the harness images"]
async fn a_request_that_the_model_cannot_take_right_now_is_sent_again() {
    each(|bench| async move {
        let session = bench.session().await;
        let later = word("later");
        let turn = session.say(&format!("flaky {later}")).await;
        turn.completed().says(&later);
    })
    .await;
}

#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs the harness images"]
async fn a_turn_that_is_cancelled_is_ended_by_the_harness() {
    each(|bench| async move {
        let session = bench.session().await;
        let words: Vec<String> = (1..=40).map(|word| format!("w{word}")).collect();
        let slow = format!("drawl {}", words.join(" "));
        let slow = Message {
            text: &slow,
            ..Message::default()
        };
        let mut turn = session.start(slow).await;
        turn.until("the agent has not begun its answer", |events| {
            said(events).contains("w2 ")
        })
        .await;
        turn.cancel().await;
        let ended = turn.end().await;
        assert!(
            ended.stopped() == Some("cancelled"),
            "{}: the cancelled turn ended with {:?}\n{}",
            ended.image,
            ended.events.last(),
            ended.log
        );
        let said = said(&ended.events);
        assert!(
            said.contains("w1 w2 ") && !said.contains("w40"),
            "{}: {said:?}",
            ended.image
        );
        session.say("say hello").await.completed().says("hello");
    })
    .await;
}

#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs the harness images"]
async fn a_long_answer_is_told_whole() {
    each(|bench| async move {
        let session = bench.session().await;
        let words: Vec<String> = (1..=1200).map(|word| format!("w{word}")).collect();
        let turn = session.say(&format!("say {}", words.join(" "))).await;
        turn.completed().says(&words.join(" "));
    })
    .await;
}

/// A tool can return any byte. U+0000 is the character that some storage
/// downstream cannot hold, and the runner reports it unchanged, in the tool
/// call or in the agent's message.
#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs the harness images"]
async fn the_character_that_a_tool_returns_is_told_as_it_is() {
    each(|bench| async move {
        let session = bench.session().await;
        let turn = session.say(r"run printf 'a\0b'").await;
        turn.completed();
        let told: String = turn
            .events
            .iter()
            .map(|event| event.data.to_string())
            .collect();
        assert!(
            told.contains(r"a\u0000b"),
            "{}: no event has what the tool returned: {:?}",
            turn.image,
            turn.events
        );
    })
    .await;
}

#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs the harness images"]
async fn a_session_that_waits_takes_little_cpu() {
    each(|bench| async move {
        let session = bench.session().await;
        let wait = Message {
            text: "run sleep 8",
            ..Message::default()
        };
        let mut turn = session.start(wait).await;
        turn.until("the agent has not called its tool", |events| {
            find(events, "tool.call").is_some()
        })
        .await;
        time::sleep(Duration::from_secs(1)).await;
        let before = session.cpu().await;
        time::sleep(Duration::from_secs(4)).await;
        let taken = session.cpu().await - before;
        assert!(
            taken < 1.0,
            "{}: the container took {taken:.1} seconds of a CPU in 4 seconds of a turn that waits",
            session.image
        );
        turn.end().await.completed();

        time::sleep(Duration::from_secs(1)).await;
        let before = session.cpu().await;
        time::sleep(Duration::from_secs(4)).await;
        let taken = session.cpu().await - before;
        assert!(
            taken < 1.0,
            "{}: the container took {taken:.1} seconds of a CPU in 4 seconds between two turns",
            session.image
        );
    })
    .await;
}

#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs the harness images"]
async fn what_the_tools_of_an_agent_fetch_goes_to_the_proxy() {
    each(|bench| async move {
        let session = bench.session().await;
        let fetch = r#"run node -e "fetch('http://example.com/').then(r => console.log('status', r.status), () => console.log('failed'))""#;
        session.say(fetch).await.completed().says("status 403");
        let asked = bench.asked().await;
        assert!(
            asked.iter().any(|host| host == "example.com"),
            "{}: the proxy was asked for {asked:?}",
            bench.image
        );
    })
    .await;
}

/// Whatever a harness fetches at startup, it fetches again for every new
/// session home.
#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs the harness images"]
async fn a_harness_fetches_nothing_for_itself() {
    each(|bench| async move {
        let session = bench.session().await;
        session.say("say hello").await.completed().says("hello");
        let asked = bench.asked().await;
        assert!(
            asked.is_empty(),
            "{}: the harness asked the proxy for {asked:?}",
            bench.image
        );
    })
    .await;
}
