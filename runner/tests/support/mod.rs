//! What the tests run the runner with. The runner is the program as it is
//! built. Its Supervisor is one that the test steers, and its harness is the
//! fake harness, which does what the message of the turn tells it.

#![allow(dead_code)]

use std::path::{Path, PathBuf};
use std::process::{ExitStatus, Stdio};
use std::time::Duration;

use tempfile::TempDir;
use tokio::io::AsyncReadExt;
use tokio::process::{Child, Command};
use tokio::task::JoinHandle;
use tokio::time;

mod supervisor;

// A file of tests uses a part of these.
#[allow(unused_imports)]
pub use supervisor::{PATIENCE, State, Supervisor, TOKEN, proto};

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

/// The runner of a turn of an agent that asks a person before it acts.
pub fn asking(supervisor: &Supervisor, home: &Home) -> Command {
    let mut command = runner(supervisor, home);
    command.env("AVIARY_APPROVAL", "ask");
    command
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
