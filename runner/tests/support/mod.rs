//! What the tests run the runner with. The runner is the program as it is
//! built. Its Supervisor is one that the test steers, and its harness is the
//! fake harness, which does what the message of the turn tells it.

#![allow(dead_code)]

use std::net::SocketAddr;
use std::path::{Path, PathBuf};
use std::pin::Pin;
use std::process::{ExitStatus, Stdio};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use futures::Stream;
use futures::channel::mpsc::{self, UnboundedSender};
use prost::Message;
use tempfile::TempDir;
use tokio::io::{AsyncReadExt, copy_bidirectional};
use tokio::net::{TcpListener, TcpStream};
use tokio::process::{Child, Command};
use tokio::task::JoinHandle;
use tokio::time;
use tokio_stream::wrappers::TcpListenerStream;
use tonic::transport::Server;
use tonic::{Code, Request, Response, Status, Streaming};

use proto::run_turn_request::Message as Report;
use proto::run_turn_response::Message as Order;
use proto::runner_service_server::{RunnerService, RunnerServiceServer};
use proto::{Cancel, Received, RunTurnRequest, RunTurnResponse, Start};

/// The protocol between the runner and the Supervisor, with the Supervisor's
/// side of it.
pub mod proto {
    include!(concat!(env!("OUT_DIR"), "/supervisor/aviary.runner.v1.rs"));
}

/// How long a test waits for what the runner is to do.
pub const PATIENCE: Duration = Duration::from_secs(10);

/// The token of the turn that the Supervisor gives.
pub const TOKEN: &str = "the-token";

/// What a Supervisor does with its runner, and what it has seen of it.
#[derive(Default)]
pub struct State {
    /// What the turn is started with.
    pub text: String,
    pub harness_session: Option<String>,
    pub instructions: String,
    /// A Supervisor of an older release says nothing of what it has
    /// recorded.
    pub older: bool,
    /// Whether the reports are left without an answer for now.
    pub holding: bool,
    /// What the next call is ended with, in place of a start.
    pub refusal: Option<(Code, &'static str)>,
    /// How many calls are still to be taken and left without a start.
    pub stalls: u32,
    /// The reports that are recorded, in order and each once.
    pub reports: Vec<Report>,
    /// The number of the last of them.
    pub last: u64,
    /// The bytes of all the reports that came, also of those that came again.
    pub bytes: usize,
    /// The authorization and the name that each call came with.
    pub calls: Vec<(String, String)>,
    /// Whether the runner ended a call itself, as it does when it has done.
    pub left: bool,
    orders: Option<UnboundedSender<Result<RunTurnResponse, Status>>>,
    /// The calls that were left without a start, which stay open.
    stalled: Vec<UnboundedSender<Result<RunTurnResponse, Status>>>,
}

impl State {
    /// What the agent said, as the reports have it.
    pub fn said(&self) -> String {
        self.reports
            .iter()
            .filter_map(|report| match report {
                Report::MessageDelta(delta) => Some(delta.text.as_str()),
                _ => None,
            })
            .collect()
    }

    /// The reports by their kinds.
    pub fn kinds(&self) -> Vec<&'static str> {
        self.reports
            .iter()
            .map(|report| match report {
                Report::Started(_) => "started",
                Report::Finished(_) => "finished",
                Report::MessageDelta(_) => "message",
                Report::ThoughtDelta(_) => "thought",
                Report::ToolCall(_) => "tool call",
                Report::ToolUpdate(_) => "tool update",
                Report::PlanUpdate(_) => "plan",
            })
            .collect()
    }

    /// Why the harness stopped, if the runner has reported it.
    pub fn stop_reason(&self) -> Option<&str> {
        self.reports.iter().find_map(|report| match report {
            Report::Finished(finished) => Some(finished.stop_reason.as_str()),
            _ => None,
        })
    }
}

struct Service {
    state: Arc<Mutex<State>>,
}

fn order(order: Order) -> Result<RunTurnResponse, Status> {
    Ok(RunTurnResponse {
        message: Some(order),
    })
}

#[tonic::async_trait]
impl RunnerService for Service {
    type RunTurnStream = Pin<Box<dyn Stream<Item = Result<RunTurnResponse, Status>> + Send>>;

    async fn run_turn(
        &self,
        request: Request<Streaming<RunTurnRequest>>,
    ) -> Result<Response<Self::RunTurnStream>, Status> {
        let metadata = |name| {
            let value = request.metadata().get(name);
            value
                .and_then(|value| value.to_str().ok())
                .unwrap_or_default()
                .to_owned()
        };
        let call = (metadata("authorization"), metadata("aviary-runner"));
        let (orders, responses) = mpsc::unbounded();
        {
            let mut state = self.state.lock().unwrap();
            state.calls.push(call);
            if let Some((code, message)) = state.refusal.take() {
                return Err(Status::new(code, message));
            }
            if state.stalls > 0 {
                state.stalls -= 1;
                state.stalled.push(orders);
                return Ok(Response::new(Box::pin(responses)));
            }
            let start = Start {
                text: state.text.clone(),
                harness_session_id: state.harness_session.clone(),
                instructions: state.instructions.clone(),
                received: (!state.older).then_some(state.last),
            };
            let _ = orders.unbounded_send(order(Order::Start(start)));
            state.orders = Some(orders.clone());
        }
        let state = self.state.clone();
        let mut reports = request.into_inner();
        tokio::spawn(async move {
            loop {
                let report = match reports.message().await {
                    Ok(Some(report)) => report,
                    Ok(None) => {
                        state.lock().unwrap().left = true;
                        break;
                    }
                    Err(_) => break,
                };
                let mut state = state.lock().unwrap();
                state.bytes += report.encoded_len();
                let seq = report.seq;
                // A runner of an older release does not number its reports.
                if seq == 0 || seq == state.last + 1 {
                    state.last = seq.max(state.last);
                    state.reports.extend(report.message);
                } else if seq > state.last + 1 {
                    let _ = orders.unbounded_send(Err(Status::data_loss("a report is missing")));
                    break;
                }
                if seq > 0 && !state.older && !state.holding {
                    let _ = orders.unbounded_send(order(Order::Received(Received { seq })));
                }
            }
            // The call ends when nothing holds its orders any more.
            let mut state = state.lock().unwrap();
            if state
                .orders
                .as_ref()
                .is_some_and(|current| current.same_receiver(&orders))
            {
                state.orders = None;
            }
        });
        Ok(Response::new(Box::pin(responses)))
    }
}

/// A Supervisor for one turn. The runner reaches it through a relay, which
/// the test stops and starts again as a Supervisor that goes and comes.
pub struct Supervisor {
    state: Arc<Mutex<State>>,
    server: SocketAddr,
    address: SocketAddr,
    relay: Option<JoinHandle<()>>,
    connections: Arc<Mutex<Vec<JoinHandle<()>>>>,
}

impl Supervisor {
    /// Starts a Supervisor that gives its runner a turn with the message.
    pub async fn start(text: &str) -> Self {
        let state = Arc::new(Mutex::new(State {
            text: text.to_owned(),
            ..State::default()
        }));
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let server = listener.local_addr().unwrap();
        let service = RunnerServiceServer::new(Service {
            state: state.clone(),
        });
        tokio::spawn(
            Server::builder()
                .add_service(service)
                .serve_with_incoming(TcpListenerStream::new(listener)),
        );
        let mut supervisor = Self {
            state,
            server,
            address: "127.0.0.1:0".parse().unwrap(),
            relay: None,
            connections: Arc::default(),
        };
        supervisor.come_back().await;
        supervisor
    }

    /// Where the runner reaches the Supervisor.
    pub fn url(&self) -> String {
        format!("http://{}", self.address)
    }

    /// Looks at what the Supervisor does and has seen, or changes it.
    pub fn state<T>(&self, with: impl FnOnce(&mut State) -> T) -> T {
        with(&mut self.state.lock().unwrap())
    }

    /// Waits for the Supervisor to have seen what the condition tells.
    pub async fn until(&self, what: &str, holds: impl Fn(&State) -> bool) {
        let deadline = Instant::now() + PATIENCE;
        while !self.state(|state| holds(state)) {
            assert!(
                Instant::now() < deadline,
                "after {PATIENCE:?}, {what}; the reports are {:?}",
                self.state(|state| state.kinds())
            );
            time::sleep(Duration::from_millis(5)).await;
        }
    }

    /// Tells the runner to stop its turn.
    pub fn cancel(&self) {
        self.send(order(Order::Cancel(Cancel {})));
    }

    /// Ends the call of the runner with an error.
    pub fn end_call(&self, code: Code, message: &'static str) {
        self.send(Err(Status::new(code, message)));
    }

    /// Answers the reports that were left without an answer.
    pub fn answer(&self) {
        let last = self.state(|state| {
            state.holding = false;
            state.last
        });
        self.send(order(Order::Received(Received { seq: last })));
    }

    fn send(&self, response: Result<RunTurnResponse, Status>) {
        let orders = self.state(|state| state.orders.clone());
        let _ = orders
            .expect("a runner is connected")
            .unbounded_send(response);
    }

    /// Goes away as a Supervisor does whose pod is killed: its connections
    /// break, and nothing listens where it was.
    pub async fn go_away(&mut self) {
        if let Some(relay) = self.relay.take() {
            relay.abort();
            let _ = relay.await;
        }
        for connection in self.connections.lock().unwrap().drain(..) {
            connection.abort();
        }
    }

    /// Comes back where it was. What it had recorded is still there, as it
    /// is with the Supervisor that a runner reaches next.
    pub async fn come_back(&mut self) {
        let listener = TcpListener::bind(self.address).await.unwrap();
        self.address = listener.local_addr().unwrap();
        let (server, connections) = (self.server, self.connections.clone());
        self.relay = Some(tokio::spawn(async move {
            while let Ok((mut incoming, _)) = listener.accept().await {
                let connection = tokio::spawn(async move {
                    if let Ok(mut outgoing) = TcpStream::connect(server).await {
                        let _ = copy_bidirectional(&mut incoming, &mut outgoing).await;
                    }
                });
                connections.lock().unwrap().push(connection);
            }
        }));
    }
}

/// The home of a session, as the pod of a turn has it.
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

/// The runner of a turn, set up as it is in the pod of the turn.
pub fn runner(supervisor: &Supervisor, home: &Home) -> Command {
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
        .env("AVIARY_GATEWAY_URL", supervisor.url())
        .env("AVIARY_TURN_TOKEN", TOKEN)
        .env("AVIARY_HARNESS_COMMAND", harness)
        .env("AVIARY_MAX_RECONNECT_SECONDS", "60")
        .current_dir(home.path().join("workspace"))
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::piped())
        .kill_on_drop(true);
    command
}

/// A runner that runs.
pub struct Running {
    child: Child,
    stderr: JoinHandle<String>,
}

/// How a runner ended.
pub struct Ended {
    pub status: ExitStatus,
    /// What the runner said of its failure, if it failed.
    pub stderr: String,
}

impl Ended {
    /// Whether the runner failed for the reason, which it leaves for the
    /// Supervisor, with a message that has the text.
    pub fn failed(&self, reason: &str, message: &str) -> bool {
        self.status.code() == Some(1)
            && self.stderr.contains(&format!("reason: \"{reason}\""))
            && self.stderr.contains(message)
    }
}

pub fn start(mut command: Command) -> Running {
    let mut child = command.spawn().unwrap();
    let mut stderr = child.stderr.take().unwrap();
    let stderr = tokio::spawn(async move {
        let mut said = String::new();
        let _ = stderr.read_to_string(&mut said).await;
        said
    });
    Running { child, stderr }
}

impl Running {
    /// Waits for the runner to end.
    pub async fn end(mut self) -> Ended {
        let status = time::timeout(PATIENCE, self.child.wait())
            .await
            .expect("the runner did not end")
            .unwrap();
        Ended {
            status,
            stderr: self.stderr.await.unwrap(),
        }
    }

    /// Whether the runner still runs.
    pub fn runs(&mut self) -> bool {
        self.child.try_wait().unwrap().is_none()
    }

    /// Tells the runner to stop, as Kubernetes tells the pod of a turn.
    pub async fn terminate(&self) {
        let pid = self.child.id().expect("the runner runs").to_string();
        let killed = Command::new("kill").args(["-TERM", &pid]).status().await;
        assert!(killed.unwrap().success());
    }

    /// How much time of a CPU the runner has taken so far.
    pub fn cpu(&self) -> Duration {
        let pid = self.child.id().expect("the runner runs");
        let stat = std::fs::read_to_string(format!("/proc/{pid}/stat")).unwrap();
        // The fields after the name of the program, which may have spaces.
        let fields: Vec<&str> = stat
            .rsplit_once(')')
            .unwrap()
            .1
            .split_whitespace()
            .collect();
        let ticks: u64 = fields[11].parse::<u64>().unwrap() + fields[12].parse::<u64>().unwrap();
        // A tick is a hundredth of a second on Linux.
        Duration::from_millis(ticks * 10)
    }
}

/// Runs a turn to its end and returns how the runner ended.
pub async fn run(supervisor: &Supervisor, home: &Home) -> Ended {
    start(runner(supervisor, home)).end().await
}
