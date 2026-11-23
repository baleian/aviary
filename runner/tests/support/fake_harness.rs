//! A fake ACP harness for the runner's tests. It has no model: each prompt is
//! a script of commands, separated by " ; ".
//!
//!   say <text>         says the text, a word at a time
//!   drawl <text>       says the text slowly, leaving time to cancel the turn
//!   think <text>       sends the text as a thought
//!   notice <text>      sends the text as a notice, or says it if the client
//!                      does not take notices
//!   tool <command>     calls a tool, then reports the call completed
//!   big <bytes>        calls a tool that returns that many bytes
//!   flood <n> <bytes>  says n chunks of that many bytes each
//!   plan <entry>       sends a plan with the entry
//!   title [<text>]     gives the session the title, or with no text sends
//!                      session information without a title
//!   ask [<n>]          sends n permission requests at once (default 1), then
//!                      says the option or outcome that each one got
//!   instructions       says the contents of AVIARY_INSTRUCTIONS_FILE
//!   instructed         says them as they were when the harness started
//!   recall             says the prompts of the earlier turns of the session
//!   sleep <ms>         waits, unless the turn is cancelled
//!   deaf               ignores cancellation for the rest of the turn
//!   fail <text>        fails the prompt with the text as the error
//!   env <name>         says the value of the environment variable
//!   die                exits without answering the prompt
//!   leave              exits after answering the prompt
//!
//! Sessions are stored in .fake-harness in the working directory, so a
//! session can only be resumed in the same home.
//!
//! FAKE_HARNESS_START_MS delays its start, and FAKE_HARNESS_STOP_MS its stop
//! after a cancellation.

use std::env;
use std::fs;
use std::io::{self, BufRead, Write};
use std::path::PathBuf;
use std::process;
use std::sync::mpsc::{self, Receiver, RecvTimeoutError};
use std::thread;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use serde_json::{Value, json};

const SESSIONS: &str = ".fake-harness";

fn main() {
    let (lines, incoming) = mpsc::channel();
    thread::spawn(move || {
        for line in io::stdin().lock().lines() {
            let Ok(line) = line else { break };
            let Ok(message) = serde_json::from_str::<Value>(&line) else {
                continue;
            };
            if lines.send(message).is_err() {
                break;
            }
        }
    });
    let mut harness = Harness {
        incoming,
        cancels: 0,
        deaf: false,
        calls: 0,
        leaving: false,
        notices: false,
        instructed: instructions(),
    };
    // Ends when the runner closes standard input.
    while let Ok(message) = harness.incoming.recv() {
        harness.handle(message);
    }
}

struct Harness {
    incoming: Receiver<Value>,
    /// The session/cancel notifications received during the current turn.
    cancels: u32,
    deaf: bool,
    /// Tool calls made by this process, for numbering them.
    calls: u32,
    /// Exits after answering the current prompt.
    leaving: bool,
    /// Whether the client takes notices.
    notices: bool,
    /// The instructions when the process started, as `instructions` says them.
    instructed: String,
}

/// Returns what `instructions` says about AVIARY_INSTRUCTIONS_FILE now.
fn instructions() -> String {
    let instructions = env::var("AVIARY_INSTRUCTIONS_FILE")
        .map_err(|_| ())
        .and_then(|file| fs::read_to_string(file).map_err(|_| ()));
    match instructions {
        Ok(instructions) if instructions.is_empty() => "no instructions".to_owned(),
        Ok(instructions) => instructions,
        Err(()) => "no file".to_owned(),
    }
}

enum Ending {
    Stopped(&'static str),
    Failed(String),
}

fn send(message: Value) {
    let mut output = io::stdout().lock();
    // The runner has exited.
    if writeln!(output, "{message}").is_err() || output.flush().is_err() {
        process::exit(0);
    }
}

fn answer(id: &Value, result: Value) {
    send(json!({"jsonrpc": "2.0", "id": id, "result": result}));
}

fn refuse(id: &Value, message: &str) {
    send(json!({"jsonrpc": "2.0", "id": id, "error": {"code": -32603, "message": message}}));
}

fn pause(variable: &str) -> Duration {
    Duration::from_millis(
        env::var(variable)
            .ok()
            .and_then(|millis| millis.parse().ok())
            .unwrap_or(0),
    )
}

fn session_file(session: &str) -> PathBuf {
    PathBuf::from(SESSIONS).join(session)
}

impl Harness {
    fn handle(&mut self, message: Value) {
        let id = &message["id"];
        let params = &message["params"];
        match message["method"].as_str() {
            Some("initialize") => {
                thread::sleep(pause("FAKE_HARNESS_START_MS"));
                self.notices = params["clientCapabilities"]["session"]["notices"].is_object();
                answer(
                    id,
                    json!({"protocolVersion": params["protocolVersion"], "agentCapabilities": {}}),
                );
            }
            Some("session/new") => {
                let since = SystemTime::now()
                    .duration_since(UNIX_EPOCH)
                    .unwrap_or_default();
                let session = format!("session-{}-{}", process::id(), since.as_nanos());
                if fs::create_dir_all(SESSIONS).is_err()
                    || fs::write(session_file(&session), "").is_err()
                {
                    return refuse(id, "cannot store session");
                }
                answer(id, json!({"sessionId": session}));
            }
            Some("session/resume") => {
                let session = params["sessionId"].as_str().unwrap_or_default();
                if session_file(session).is_file() {
                    answer(id, json!({}));
                } else {
                    refuse(id, "unknown session");
                }
            }
            Some("session/prompt") => {
                let session = params["sessionId"].as_str().unwrap_or_default().to_owned();
                let text = params["prompt"][0]["text"]
                    .as_str()
                    .unwrap_or_default()
                    .to_owned();
                match self.turn(&session, &text) {
                    Ending::Stopped(reason) => answer(
                        id,
                        json!({
                            "stopReason": reason,
                            "usage": {"totalTokens": 18, "inputTokens": 11, "outputTokens": 7},
                        }),
                    ),
                    Ending::Failed(message) => refuse(id, &message),
                }
                if self.leaving {
                    process::exit(0);
                }
            }
            Some("session/cancel") => self.cancels += 1,
            Some(_) if !id.is_null() => send(json!({
                "jsonrpc": "2.0", "id": id, "error": {"code": -32601, "message": "method not found"},
            })),
            _ => {}
        }
    }

    fn update(&self, session: &str, update: Value) {
        send(json!({
            "jsonrpc": "2.0",
            "method": "session/update",
            "params": {"sessionId": session, "update": update},
        }));
    }

    fn say(&self, session: &str, text: &str) {
        self.update(
            session,
            json!({"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": text}}),
        );
    }

    /// Handles incoming messages for `duration`.
    fn wait(&mut self, duration: Duration) {
        let until = Instant::now() + duration;
        loop {
            let left = until.saturating_duration_since(Instant::now());
            match self.incoming.recv_timeout(left) {
                Ok(message) => self.handle(message),
                Err(RecvTimeoutError::Timeout) => return,
                Err(RecvTimeoutError::Disconnected) => process::exit(0),
            }
        }
    }

    /// Handles pending messages and reports whether the turn should stop.
    fn stopping(&mut self) -> bool {
        self.wait(Duration::ZERO);
        self.cancels > 0 && !self.deaf
    }

    fn turn(&mut self, session: &str, text: &str) -> Ending {
        let before = fs::read_to_string(session_file(session)).unwrap_or_default();
        let _ = fs::write(session_file(session), format!("{before}{text}\n"));
        self.deaf = false;
        // Cancellations of an earlier turn do not apply.
        self.cancels = 0;
        for instruction in text.split(" ; ") {
            let (word, rest) = instruction
                .trim()
                .split_once(' ')
                .unwrap_or((instruction.trim(), ""));
            match word {
                "say" => {
                    for piece in rest.split_inclusive(' ') {
                        self.say(session, piece);
                    }
                }
                "drawl" => {
                    for piece in rest.split_inclusive(' ') {
                        self.say(session, piece);
                        self.wait(Duration::from_millis(100));
                        if self.stopping() {
                            break;
                        }
                    }
                }
                "think" => self.update(
                    session,
                    json!({"sessionUpdate": "agent_thought_chunk", "content": {"type": "text", "text": rest}}),
                ),
                "notice" if self.notices => self.update(
                    session,
                    json!({"sessionUpdate": "notice", "severity": "warning", "title": rest}),
                ),
                "notice" => self.say(session, rest),
                "tool" | "big" => {
                    self.calls += 1;
                    let call = format!("call-{}", self.calls);
                    let output = match word {
                        "big" => "x".repeat(rest.parse().unwrap_or(0)),
                        _ => format!("ran {rest}"),
                    };
                    self.update(
                        session,
                        json!({
                            "sessionUpdate": "tool_call", "toolCallId": call, "title": rest,
                            "kind": "execute", "status": "in_progress", "rawInput": {"command": rest},
                        }),
                    );
                    self.update(
                        session,
                        json!({
                            "sessionUpdate": "tool_call_update", "toolCallId": call,
                            "status": "completed", "rawOutput": {"output": output},
                        }),
                    );
                }
                "flood" => {
                    let (pieces, bytes) = rest.split_once(' ').unwrap_or((rest, "1"));
                    let piece = "x".repeat(bytes.parse().unwrap_or(1));
                    for _ in 0..pieces.parse().unwrap_or(0) {
                        self.say(session, &piece);
                    }
                }
                "plan" => self.update(
                    session,
                    json!({
                        "sessionUpdate": "plan",
                        "entries": [{"content": rest, "priority": "medium", "status": "pending"}],
                    }),
                ),
                "title" => {
                    let mut info = json!({
                        "sessionUpdate": "session_info_update",
                        "updatedAt": "2026-01-01T00:00:00Z",
                    });
                    if !rest.is_empty() {
                        info["title"] = json!(rest);
                    }
                    self.update(session, info);
                }
                "ask" => {
                    let requests: Vec<String> = (0..rest.parse().unwrap_or(1))
                        .map(|request| format!("permission-{request}"))
                        .collect();
                    for (call, request) in requests.iter().enumerate() {
                        send(json!({
                            "jsonrpc": "2.0", "id": request, "method": "session/request_permission",
                            "params": {
                                "sessionId": session,
                                "toolCall": {
                                    "toolCallId": format!("call-{call}"), "title": "ask",
                                    "kind": "execute", "rawInput": {"command": "ask"},
                                },
                                "options": [
                                    {"optionId": "always", "name": "Always", "kind": "allow_always"},
                                    {"optionId": "once", "name": "Once", "kind": "allow_once"},
                                    {"optionId": "never", "name": "Never", "kind": "reject_once"},
                                ],
                            },
                        }));
                    }
                    // Answers may arrive in any order.
                    let mut given = vec![String::from("nothing"); requests.len()];
                    let until = Instant::now() + Duration::from_secs(5);
                    let mut left = requests.len();
                    while left > 0 {
                        let Ok(message) = self
                            .incoming
                            .recv_timeout(until.saturating_duration_since(Instant::now()))
                        else {
                            break;
                        };
                        match requests.iter().position(|request| message["id"] == **request) {
                            Some(request) => {
                                let outcome = &message["result"]["outcome"];
                                given[request] = outcome["optionId"]
                                    .as_str()
                                    .or(outcome["outcome"].as_str())
                                    .unwrap_or("nothing")
                                    .to_owned();
                                left -= 1;
                            }
                            None => self.handle(message),
                        }
                    }
                    self.say(session, &given.join(" "));
                }
                "instructions" => self.say(session, &instructions()),
                "instructed" => self.say(session, &self.instructed.clone()),
                "recall" => self.say(
                    session,
                    match before.trim_end() {
                        "" => "nothing",
                        before => before,
                    },
                ),
                "sleep" => {
                    let until = Instant::now() + Duration::from_millis(rest.parse().unwrap_or(0));
                    while Instant::now() < until && !self.stopping() {
                        self.wait(Duration::from_millis(20));
                    }
                }
                "deaf" => self.deaf = true,
                "fail" => return Ending::Failed(rest.to_owned()),
                "env" => self.say(session, &env::var(rest).unwrap_or_else(|_| "not set".to_owned())),
                "die" => process::exit(1),
                "leave" => self.leaving = true,
                _ => self.say(session, "fake"),
            }
            if self.stopping() {
                // Stops slowly, counting any further cancellations.
                self.wait(pause("FAKE_HARNESS_STOP_MS"));
                self.say(session, &format!("[told to stop {} times]", self.cancels));
                return Ending::Stopped("cancelled");
            }
        }
        Ending::Stopped("end_turn")
    }
}
