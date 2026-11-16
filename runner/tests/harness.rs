//! Each harness image keeps what images/README.md asks of it. A test runs
//! the turns of a session the way the pods of the turns run them: the runner
//! in a container of the harness image, as the user of the agent, with the
//! home of the session as its only lasting storage. The Supervisor is the
//! one of the runner's tests and the model is mock-llm, each in a container
//! beside it. The network of the containers leads nowhere else, and mock-llm
//! is also their forward proxy, which lets nothing through.
//!
//! The tests need the images, so they run with `make test-harness`.

use std::future::Future;
use std::path::PathBuf;
use std::process::Stdio;
use std::sync::atomic::{AtomicU32, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use serde_json::Value;
use tempfile::TempDir;
use tokio::io::{AsyncBufReadExt, AsyncReadExt, BufReader};
use tokio::process::Command;
use tokio::task::JoinHandle;
use tokio::time;

/// How long a test waits for a turn.
const PATIENCE: Duration = Duration::from_secs(120);

/// The token of every turn, which is also the key that the model asks for:
/// no Egress Proxy stands between the harness and the model here.
const TOKEN: &str = "the-turn-token";

/// The forward proxy of the containers, which is the model server.
const PROXY: &str = "http://turn:the-turn-token@model:8080";

const RUNNER_IMAGE: &str = "aviary-runner:dev";
const MODEL_IMAGE: &str = "aviary-mock-llm:dev";
const SUPERVISOR_IMAGE: &str = "aviary-fake-supervisor:dev";

/// The harness images to test.
fn images() -> Vec<String> {
    let images = std::env::var("AVIARY_HARNESS_IMAGES")
        .expect("AVIARY_HARNESS_IMAGES names no harness images: run `make test-harness`");
    images.split_whitespace().map(str::to_owned).collect()
}

/// Runs docker and returns what it printed.
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

/// Where a harness image is tested: a network of its own with a model
/// server on it.
struct Bench {
    image: String,
    /// A name that nothing else on the machine has.
    name: String,
    files: TempDir,
    made: AtomicU32,
}

/// The first message of a session, or a later one.
#[derive(Default)]
struct Message<'a> {
    text: &'a str,
    /// The harness session to continue.
    session: Option<&'a str>,
    /// The instructions of the session's agent.
    instructions: &'a str,
}

impl Bench {
    async fn new(image: &str) -> Arc<Self> {
        let name = format!("aviary-test-{}", uuid::Uuid::new_v4().simple());
        let files = TempDir::new().unwrap();
        let bench = Arc::new(Self {
            image: image.to_owned(),
            name,
            files,
            made: AtomicU32::new(0),
        });
        let label = bench.label();
        // No way out, as from a pod.
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
            &format!("MOCK_LLM_API_KEY={TOKEN}"),
            MODEL_IMAGE,
        ])
        .await;
        let began = Instant::now();
        while !docker(&["logs", &model]).await.contains("listening") {
            assert!(began.elapsed() < PATIENCE, "the model server did not start");
            time::sleep(Duration::from_millis(50)).await;
        }
        // The runner comes from its own image, as it does in a pod.
        let runner = format!("{}-runner", bench.name);
        docker(&[
            "create",
            "--name",
            &runner,
            "--label",
            &label,
            RUNNER_IMAGE,
            "/aviary-runner",
        ])
        .await;
        let bin = bench.files.path().join("bin");
        std::fs::create_dir(&bin).unwrap();
        docker(&[
            "cp",
            &format!("{runner}:/aviary-runner"),
            bin.to_str().unwrap(),
        ])
        .await;
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

    /// Makes the home of a new session, as the pod of its first turn does,
    /// and returns the volume that holds it.
    async fn home(&self) -> String {
        let volume = self.next("home");
        docker(&["volume", "create", "--label", &self.label(), &volume]).await;
        let store = format!("{volume}:/store");
        docker(&[
            "run",
            "--rm",
            "--user",
            "0:0",
            "--volume",
            &store,
            "--entrypoint",
            "chown",
            &self.image,
            "10000:10000",
            "/store",
        ])
        .await;
        docker(&[
            "run",
            "--rm",
            "--user",
            "10000:10000",
            "--volume",
            &store,
            RUNNER_IMAGE,
            "/aviary-runner",
            "init-home",
            "/store/home",
        ])
        .await;
        volume
    }

    /// The hosts that the proxy was asked for: what the harness and the
    /// tools of the agent tried to reach outside.
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

    /// How many megabytes the home of a session holds.
    async fn megabytes(&self, home: &str) -> u64 {
        let used = docker(&[
            "run",
            "--rm",
            "--volume",
            &format!("{home}:/store"),
            "--entrypoint",
            "du",
            &self.image,
            "--summarize",
            "--block-size=1M",
            "/store",
        ])
        .await;
        used.split_whitespace().next().unwrap().parse().unwrap()
    }

    /// Starts a turn of the session whose home the volume holds.
    async fn start(&self, home: &str, message: Message<'_>) -> Turn {
        let label = self.label();
        let supervisor = self.next("supervisor");
        let mut supervising = Command::new("docker")
            .args([
                "run",
                "--rm",
                "--name",
                &supervisor,
                "--label",
                &label,
                "--network",
                &self.name,
                "--env",
                &format!("TURN_TEXT={}", message.text),
                "--env",
                &format!(
                    "TURN_HARNESS_SESSION={}",
                    message.session.unwrap_or_default()
                ),
                "--env",
                &format!("TURN_INSTRUCTIONS={}", message.instructions),
                SUPERVISOR_IMAGE,
            ])
            .stdout(Stdio::piped())
            .stderr(Stdio::null())
            .kill_on_drop(true)
            .spawn()
            .unwrap();
        let reports = Arc::new(Mutex::new(Vec::new()));
        let mut lines = BufReader::new(supervising.stdout.take().unwrap()).lines();
        let recorded = reports.clone();
        let reading = tokio::spawn(async move {
            while let Ok(Some(line)) = lines.next_line().await {
                if let Ok(report) = serde_json::from_str::<Value>(&line) {
                    recorded.lock().unwrap().push(report);
                }
            }
            let _ = supervising.wait().await;
        });

        // What the runner leaves when it fails, where Kubernetes reads it.
        let left = self.files.path().join(format!("{supervisor}.log"));
        std::fs::write(&left, "").unwrap();
        std::fs::set_permissions(&left, std::os::unix::fs::PermissionsExt::from_mode(0o666))
            .unwrap();
        let harness = self.next("harness");
        let direct = format!("localhost,127.0.0.1,::1,model,{supervisor}");
        let mut running = Command::new("docker")
            .args([
                "run",
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
                "/home/workspace",
                "--mount",
                &format!("type=volume,source={home},target=/home,volume-subpath=home"),
                "--volume",
                &format!("{}:/aviary/bin:ro", self.files.path().join("bin").display()),
                "--volume",
                &format!("{}:/dev/termination-log", left.display()),
                "--env",
                "HOME=/home",
                "--env",
                &format!("AVIARY_GATEWAY_URL=http://{supervisor}:9090"),
                "--env",
                &format!("AVIARY_TURN_TOKEN={TOKEN}"),
                "--env",
                "AVIARY_LLM_BASE_URL=http://model:8080/v1",
                "--env",
                &format!("AVIARY_LLM_API_KEY={TOKEN}"),
                "--env",
                "AVIARY_LLM_MODEL=mock",
                "--env",
                "AVIARY_MAX_TOOL_OUTPUT_BYTES=65536",
                "--env",
                "AVIARY_MAX_RECONNECT_SECONDS=60",
                // Everything else goes to the proxy, as it does in a pod.
                "--env",
                &format!("HTTP_PROXY={PROXY}"),
                "--env",
                &format!("HTTPS_PROXY={PROXY}"),
                "--env",
                &format!("NO_PROXY={direct}"),
                "--env",
                &format!("http_proxy={PROXY}"),
                "--env",
                &format!("https_proxy={PROXY}"),
                "--env",
                &format!("no_proxy={direct}"),
                &self.image,
                "/aviary/bin/aviary-runner",
            ])
            .stdout(Stdio::null())
            .stderr(Stdio::piped())
            .kill_on_drop(true)
            .spawn()
            .unwrap();
        let mut stderr = running.stderr.take().unwrap();
        let running = tokio::spawn(async move {
            let mut said = String::new();
            let _ = stderr.read_to_string(&mut said).await;
            let status = running.wait().await.unwrap();
            (status.code(), said)
        });
        Turn {
            image: self.image.clone(),
            supervisor,
            harness,
            reports,
            reading,
            running,
            left,
        }
    }

    /// Runs a turn of the session to its end.
    async fn run(&self, home: &str, message: Message<'_>) -> Ended {
        self.start(home, message).await.end().await
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
        for (kind, list, remove) in [
            (
                "container",
                vec!["ps", "--all", "--quiet"],
                vec!["rm", "--force", "--volumes"],
            ),
            (
                "volume",
                vec!["volume", "ls", "--quiet"],
                vec!["volume", "rm", "--force"],
            ),
            (
                "network",
                vec!["network", "ls", "--quiet"],
                vec!["network", "rm"],
            ),
        ] {
            let mut list = list;
            list.extend(["--filter", &filter]);
            let found = docker(&list);
            let mut remove = remove;
            remove.extend(found.split_whitespace());
            if found.trim().is_empty() {
                continue;
            }
            let _ = kind;
            docker(&remove);
        }
    }
}

/// A turn that runs.
struct Turn {
    image: String,
    supervisor: String,
    harness: String,
    reports: Arc<Mutex<Vec<Value>>>,
    reading: JoinHandle<()>,
    running: JoinHandle<(Option<i32>, String)>,
    left: PathBuf,
}

/// How a turn ended.
struct Ended {
    image: String,
    /// The exit code of the runner, which is that of the pod.
    code: Option<i32>,
    /// What the Supervisor saw of the turn.
    reports: Vec<Value>,
    /// What the runner left for the Supervisor when it failed.
    failure: Value,
    /// What the runner and the harness wrote for a person to read.
    log: String,
}

fn said(reports: &[Value]) -> String {
    reports
        .iter()
        .filter(|report| report["report"] == "message")
        .filter_map(|report| report["text"].as_str())
        .collect()
}

/// What the reports tell of the tools that the agent called.
fn tools(reports: &[Value]) -> String {
    reports
        .iter()
        .filter(|report| {
            report["report"]
                .as_str()
                .is_some_and(|kind| kind.starts_with("tool"))
        })
        .map(Value::to_string)
        .collect()
}

fn find<'a>(reports: &'a [Value], kind: &str) -> Option<&'a Value> {
    reports.iter().find(|report| report["report"] == kind)
}

impl Turn {
    /// Waits for the Supervisor to have seen what the condition tells.
    async fn until(&self, what: &str, holds: impl Fn(&[Value]) -> bool) {
        let began = Instant::now();
        while !holds(&self.reports.lock().unwrap()) {
            assert!(
                began.elapsed() < PATIENCE && !self.running.is_finished(),
                "{}: {what}; the reports are {:?}",
                self.image,
                self.reports.lock().unwrap()
            );
            time::sleep(Duration::from_millis(20)).await;
        }
    }

    /// Has the Supervisor tell the runner to stop the turn.
    async fn cancel(&self) {
        docker(&["kill", "--signal", "USR1", &self.supervisor]).await;
    }

    /// Tells the container to stop, as Kubernetes tells the pod of a turn.
    async fn stop(&self) {
        docker(&["kill", "--signal", "TERM", &self.harness]).await;
    }

    /// How many seconds of a CPU the container has taken so far.
    async fn cpu(&self) -> f64 {
        let stat = docker(&["exec", &self.harness, "cat", "/sys/fs/cgroup/cpu.stat"]).await;
        let used = stat
            .lines()
            .find_map(|line| line.strip_prefix("usage_usec "))
            .expect("the CPU time of the container");
        used.trim().parse::<f64>().unwrap() / 1e6
    }

    /// Waits for the turn to end.
    async fn end(self) -> Ended {
        let (code, log) = time::timeout(PATIENCE, self.running)
            .await
            .unwrap_or_else(|_| panic!("{}: the turn did not end", self.image))
            .unwrap();
        // The Supervisor ends when the runner has ended its call. After a
        // runner that failed it is still there.
        if time::timeout(Duration::from_secs(2), self.reading)
            .await
            .is_err()
        {
            docker(&["rm", "--force", &self.supervisor]).await;
        }
        let left = std::fs::read_to_string(&self.left).unwrap_or_default();
        Ended {
            image: self.image,
            code,
            reports: self.reports.lock().unwrap().clone(),
            failure: serde_json::from_str(&left).unwrap_or_default(),
            log,
        }
    }
}

impl Ended {
    /// Checks that the turn completed.
    fn completed(&self) -> &Self {
        let finished = find(&self.reports, "finished");
        assert!(
            self.code == Some(0)
                && finished.is_some_and(|finished| finished["stop_reason"] == "end_turn"),
            "{}: the turn ended with the exit code {:?} and {finished:?}, and left {}\n{}",
            self.image,
            self.code,
            self.failure,
            self.log
        );
        self
    }

    /// Checks that the turn failed for the reason.
    fn failed(&self, reason: &str) -> &Self {
        assert!(
            self.code == Some(1)
                && self.failure["reason"] == reason
                && self.failure["message"]
                    .as_str()
                    .is_some_and(|message| !message.is_empty()),
            "{}: the turn ended with the exit code {:?} and left {}, not the reason {reason}\n{}",
            self.image,
            self.code,
            self.failure,
            self.log
        );
        self
    }

    /// Checks that the agent said the text. A harness may say more.
    fn says(&self, text: &str) -> &Self {
        let said = said(&self.reports);
        assert!(
            said.contains(text),
            "{}: the agent said {said:?}, not {text:?}",
            self.image
        );
        self
    }

    /// The harness session that the turn ran in.
    fn session(&self) -> String {
        let started = find(&self.reports, "started").expect("the start of the turn");
        started["harness_session"].as_str().unwrap().to_owned()
    }
}

/// Runs the check for every harness image, all of them at once.
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

/// A word that no other test uses.
fn word(prefix: &str) -> String {
    format!(
        "{prefix}-{}",
        &uuid::Uuid::new_v4().simple().to_string()[..8]
    )
}

#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs the harness images"]
async fn a_session_goes_on_in_its_home_and_nowhere_else() {
    each(|bench| async move {
        let home = bench.home().await;
        let content = word("content");
        let write = format!("run printf {content} > note.txt");
        let first = bench
            .run(
                &home,
                Message {
                    text: &write,
                    ..Message::default()
                },
            )
            .await;
        first.completed();
        // A harness tells the command with the call or with an update of it.
        assert!(
            tools(&first.reports).contains(&content),
            "{}: the agent called no tool with the command: {:?}",
            first.image,
            first.reports
        );
        // A harness reports the usage of one answer of the model or of all
        // of them, and every answer of mock-llm takes 11 tokens in and 7 out.
        let usage = &find(&first.reports, "finished").unwrap()["usage"];
        let answers = usage["total"].as_i64().unwrap_or(0) / 18;
        assert!(
            answers >= 1 && usage["input"] == 11 * answers && usage["output"] == 7 * answers,
            "{}: the harness reported the usage {usage}",
            first.image
        );
        let session = first.session();

        // The next turn runs in another container. The harness finds its
        // conversation and the files of the agent in the home.
        let recall = Message {
            text: "recall",
            session: Some(&session),
            ..Message::default()
        };
        let second = bench.run(&home, recall).await;
        second.completed().says(&write);
        assert_eq!(second.session(), session, "{}", second.image);
        let read = Message {
            text: "run cat note.txt",
            session: Some(&session),
            ..Message::default()
        };
        bench.run(&home, read).await.completed().says(&content);

        // Whatever a harness keeps for itself, it keeps little of it.
        let megabytes = bench.megabytes(&home).await;
        assert!(
            megabytes < 50,
            "{}: the home of the session holds {megabytes} MB",
            bench.image
        );

        // In another home there is no such session, and a new one starts
        // without that conversation.
        let elsewhere = bench.home().await;
        let lost = Message {
            text: "recall",
            session: Some(&session),
            ..Message::default()
        };
        bench.run(&elsewhere, lost).await.failed("resume_failed");
        let anew = bench
            .run(
                &elsewhere,
                Message {
                    text: "recall",
                    ..Message::default()
                },
            )
            .await;
        anew.completed().says("nothing");
    })
    .await;
}

#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs the harness images"]
async fn the_instructions_of_an_agent_reach_the_model_in_every_turn() {
    each(|bench| async move {
        let home = bench.home().await;
        let plum = word("plum");
        let (find, instructions) = (
            format!("find {plum}"),
            format!("The word of the day is {plum}."),
        );
        let told = Message {
            text: &find,
            instructions: &instructions,
            ..Message::default()
        };
        let first = bench.run(&home, told).await;
        first.completed().says("present");
        let session = first.session();
        let again = Message {
            text: &find,
            instructions: &instructions,
            session: Some(&session),
        };
        bench.run(&home, again).await.completed().says("present");

        // An agent without instructions sends the model none.
        let plain = bench.home().await;
        let untold = bench
            .run(
                &plain,
                Message {
                    text: &find,
                    ..Message::default()
                },
            )
            .await;
        untold.completed().says("absent");
    })
    .await;
}

/// The pod of a turn makes the home of its session before the agent starts.
/// A runner that cannot make it leaves the reason where Kubernetes reads it,
/// as it does for a turn.
#[tokio::test]
#[ignore = "needs the harness images"]
async fn a_home_that_cannot_be_made_is_why_the_pod_fails() {
    let files = tempfile::tempdir().unwrap();
    let left = files.path().join("termination-log");
    std::fs::write(&left, "").unwrap();
    std::fs::set_permissions(&left, std::os::unix::fs::PermissionsExt::from_mode(0o666)).unwrap();
    // The user of the agent may make nothing at the root of the image.
    let made = Command::new("docker")
        .args(["run", "--rm", "--user", "10000:10000", "--cap-drop", "ALL"])
        .args([
            "--volume",
            &format!("{}:/dev/termination-log", left.display()),
        ])
        .args([RUNNER_IMAGE, "/aviary-runner", "init-home", "/nowhere/home"])
        .output()
        .await
        .expect("docker");
    let reason: Value =
        serde_json::from_str(&std::fs::read_to_string(&left).unwrap()).unwrap_or_default();
    assert!(
        made.status.code() == Some(1)
            && reason["reason"] == "runner_error"
            && reason["message"]
                .as_str()
                .is_some_and(|message| message.contains("could not be made: /nowhere:")),
        "the runner ended with {:?} and left {reason}: {}",
        made.status.code(),
        String::from_utf8_lossy(&made.stderr)
    );
}

#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs the harness images"]
async fn a_turn_that_the_model_refuses_fails() {
    each(|bench| async move {
        let home = bench.home().await;
        let refused = Message {
            text: "fail the model is out of order",
            ..Message::default()
        };
        bench.run(&home, refused).await.failed("harness_error");
    })
    .await;
}

/// A model endpoint that cannot take a request right now answers 503, and so
/// does the Egress Proxy that cannot ask a Supervisor about a request.
#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs the harness images"]
async fn a_request_that_the_model_cannot_take_right_now_is_sent_again() {
    each(|bench| async move {
        let home = bench.home().await;
        let later = word("later");
        let flaky = format!("flaky {later}");
        let turn = bench
            .run(
                &home,
                Message {
                    text: &flaky,
                    ..Message::default()
                },
            )
            .await;
        turn.completed().says(&later);
    })
    .await;
}

#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs the harness images"]
async fn a_turn_that_is_cancelled_is_ended_by_the_harness() {
    each(|bench| async move {
        let home = bench.home().await;
        let words: Vec<String> = (1..=40).map(|word| format!("w{word}")).collect();
        let slow = format!("drawl {}", words.join(" "));
        let turn = bench
            .start(
                &home,
                Message {
                    text: &slow,
                    ..Message::default()
                },
            )
            .await;
        turn.until("the agent has not begun its answer", |reports| {
            said(reports).contains("w2 ")
        })
        .await;
        turn.cancel().await;
        let ended = turn.end().await;
        let finished = find(&ended.reports, "finished");
        assert!(
            ended.code == Some(0)
                && finished.is_some_and(|finished| finished["stop_reason"] == "cancelled"),
            "{}: the cancelled turn ended with the exit code {:?} and {finished:?}\n{}",
            ended.image,
            ended.code,
            ended.log
        );
        // The answer is reported as far as it got.
        let said = said(&ended.reports);
        assert!(
            said.contains("w1 w2 ") && !said.contains("w40"),
            "{}: {said:?}",
            ended.image
        );
    })
    .await;
}

/// Kubernetes tells a pod to stop and kills it a while later. In that while
/// the harness ends the turn, and writes down what it has to.
#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs the harness images"]
async fn a_pod_that_is_stopped_has_its_harness_end_the_turn() {
    each(|bench| async move {
        let home = bench.home().await;
        let turn = bench
            .start(
                &home,
                Message {
                    text: "run sleep 60",
                    ..Message::default()
                },
            )
            .await;
        turn.until("the agent has not called its tool", |reports| {
            find(reports, "tool call").is_some()
        })
        .await;
        turn.stop().await;
        let stopped = Instant::now();
        let ended = turn.end().await;
        let finished = find(&ended.reports, "finished");
        assert!(
            ended.code == Some(143)
                && finished.is_some_and(|finished| finished["stop_reason"] == "cancelled"),
            "{}: the stopped turn ended with the exit code {:?} and {finished:?}\n{}",
            ended.image,
            ended.code,
            ended.log
        );
        assert!(
            stopped.elapsed() < Duration::from_secs(15),
            "{}: the turn took {:?} to stop",
            ended.image,
            stopped.elapsed()
        );
    })
    .await;
}

#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs the harness images"]
async fn a_long_answer_is_reported_whole() {
    each(|bench| async move {
        let home = bench.home().await;
        let words: Vec<String> = (1..=1200).map(|word| format!("w{word}")).collect();
        let long = format!("say {}", words.join(" "));
        let turn = bench
            .run(
                &home,
                Message {
                    text: &long,
                    ..Message::default()
                },
            )
            .await;
        turn.completed().says(&words.join(" "));
    })
    .await;
}

/// A tool may return any byte, and the character U+0000 is the one that not
/// everything after the harness can keep. The runner reports it as it is,
/// with the tool call or with what the agent says of it.
#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs the harness images"]
async fn the_character_that_a_tool_returns_is_reported_as_it_is() {
    each(|bench| async move {
        let home = bench.home().await;
        let odd = Message {
            text: r"run printf 'a\0b'",
            ..Message::default()
        };
        let turn = bench.run(&home, odd).await;
        turn.completed();
        let reported: String = turn.reports.iter().map(Value::to_string).collect();
        assert!(
            reported.contains(r"a\u0000b"),
            "{}: no report has what the tool returned: {:?}",
            turn.image,
            turn.reports
        );
    })
    .await;
}

/// A turn is mostly waiting: for the model, for a tool. Neither the runner
/// nor the harness is to keep a CPU busy with it.
#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs the harness images"]
async fn a_turn_that_waits_takes_little_cpu() {
    each(|bench| async move {
        let home = bench.home().await;
        let turn = bench
            .start(
                &home,
                Message {
                    text: "run sleep 8",
                    ..Message::default()
                },
            )
            .await;
        turn.until("the agent has not called its tool", |reports| {
            find(reports, "tool call").is_some()
        })
        .await;
        time::sleep(Duration::from_secs(1)).await;
        let before = turn.cpu().await;
        time::sleep(Duration::from_secs(4)).await;
        let taken = turn.cpu().await - before;
        assert!(
            taken < 1.0,
            "{}: the container took {taken:.1} seconds of a CPU in 4 seconds of waiting",
            turn.image
        );
        turn.end().await.completed();
    })
    .await;
}

/// What a program in the pod fetches goes to the proxy, which lets through
/// what the installation allows.
#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs the harness images"]
async fn what_the_tools_of_an_agent_fetch_goes_to_the_proxy() {
    each(|bench| async move {
        let home = bench.home().await;
        let fetch = r#"run node -e "fetch('http://example.com/').then(r => console.log('status', r.status), () => console.log('failed'))""#;
        let turn = bench
            .run(
                &home,
                Message {
                    text: fetch,
                    ..Message::default()
                },
            )
            .await;
        turn.completed().says("status 403");
        let asked = bench.asked().await;
        assert!(
            asked.iter().any(|host| host == "example.com"),
            "{}: the proxy was asked for {asked:?}",
            bench.image
        );
    })
    .await;
}

/// A harness is started anew for every turn, in a home that was empty when
/// the session began. What it fetches for itself when it starts, it fetches
/// in every turn or into every home.
#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs the harness images"]
async fn a_harness_fetches_nothing_for_itself() {
    each(|bench| async move {
        let home = bench.home().await;
        let turn = bench
            .run(
                &home,
                Message {
                    text: "say hello",
                    ..Message::default()
                },
            )
            .await;
        turn.completed().says("hello");
        let asked = bench.asked().await;
        assert!(
            asked.is_empty(),
            "{}: the harness asked the proxy for {asked:?}",
            bench.image
        );
    })
    .await;
}
