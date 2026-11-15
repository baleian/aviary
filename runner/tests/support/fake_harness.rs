//! A harness for the tests of the runner. It speaks the Agent Client Protocol
//! on its standard input and output as a harness does, with no model behind
//! it: the message of a turn tells it what to do.
//!
//!   say <text>         says the text, a word at a time
//!   drawl <text>       says it slowly, which leaves time to stop the turn
//!   think <text>       thinks the text
//!   tool <command>     calls a tool, and updates the call when it has returned
//!   big <bytes>        calls a tool that returns so many bytes
//!   flood <n> <bytes>  says n pieces of so many bytes each
//!   plan <entry>       makes a plan with the entry
//!   ask                asks for permission and says which option it was given
//!   instructions       says what its file of instructions holds
//!   recall             says the messages of the turns before this one
//!   sleep <ms>         does nothing for so long
//!   deaf               does not stop from here on when it is told to
//!   fail <text>        answers the message with an error
//!
//! Several of them are separated by " ; ".
//!
//! Its sessions are files in .fake-harness in its working directory, so a
//! session can be continued where its home is, and nowhere else.
//!
//! FAKE_HARNESS_START_MS makes it slow to start, and FAKE_HARNESS_STOP_MS slow
//! to stop when it is told to.

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
    };
    // The runner closes the input when the turn is over.
    while let Ok(message) = harness.incoming.recv() {
        harness.handle(message);
    }
}

struct Harness {
    incoming: Receiver<Value>,
    /// How many times it was told to stop the turn.
    cancels: u32,
    deaf: bool,
    /// How many tools it has called.
    calls: u32,
}

/// How a turn ended: with a stop reason, or with an error.
enum Ending {
    Stopped(&'static str),
    Failed(String),
}

fn send(message: Value) {
    let mut output = io::stdout().lock();
    // A runner that is gone reads nothing more.
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
                    return refuse(id, "the session cannot be kept");
                }
                answer(id, json!({"sessionId": session}));
            }
            Some("session/resume") => {
                let session = params["sessionId"].as_str().unwrap_or_default();
                if session_file(session).is_file() {
                    answer(id, json!({}));
                } else {
                    refuse(id, "the session is not known");
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

    /// Waits for so long, and takes in what the runner sends meanwhile. It
    /// returns the answer to the request with the ID, if that comes.
    fn wait(&mut self, duration: Duration, answer_to: Option<&str>) -> Option<Value> {
        let until = Instant::now() + duration;
        loop {
            let left = until.saturating_duration_since(Instant::now());
            match self.incoming.recv_timeout(left) {
                Ok(message) if answer_to.is_some() && message["id"].as_str() == answer_to => {
                    return Some(message);
                }
                Ok(message) => self.handle(message),
                Err(RecvTimeoutError::Timeout) => return None,
                Err(RecvTimeoutError::Disconnected) => process::exit(0),
            }
        }
    }

    /// Whether the turn is to stop.
    fn stopping(&mut self) -> bool {
        self.wait(Duration::ZERO, None);
        self.cancels > 0 && !self.deaf
    }

    fn turn(&mut self, session: &str, text: &str) -> Ending {
        let before = fs::read_to_string(session_file(session)).unwrap_or_default();
        let _ = fs::write(session_file(session), format!("{before}{text}\n"));
        self.deaf = false;
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
                        self.wait(Duration::from_millis(100), None);
                        if self.stopping() {
                            break;
                        }
                    }
                }
                "think" => self.update(
                    session,
                    json!({"sessionUpdate": "agent_thought_chunk", "content": {"type": "text", "text": rest}}),
                ),
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
                "ask" => {
                    send(json!({
                        "jsonrpc": "2.0", "id": "permission", "method": "session/request_permission",
                        "params": {
                            "sessionId": session,
                            "toolCall": {"toolCallId": "call-0", "title": "ask"},
                            "options": [
                                {"optionId": "always", "name": "Always", "kind": "allow_always"},
                                {"optionId": "once", "name": "Once", "kind": "allow_once"},
                                {"optionId": "never", "name": "Never", "kind": "reject_once"},
                            ],
                        },
                    }));
                    let given = self.wait(Duration::from_secs(5), Some("permission"));
                    let outcome = &given.unwrap_or_default()["result"]["outcome"];
                    self.say(
                        session,
                        outcome["optionId"]
                            .as_str()
                            .or(outcome["outcome"].as_str())
                            .unwrap_or("nothing"),
                    );
                }
                "instructions" => {
                    let instructions = env::var("AVIARY_INSTRUCTIONS_FILE")
                        .map_err(|_| ())
                        .and_then(|file| fs::read_to_string(file).map_err(|_| ()));
                    self.say(
                        session,
                        match instructions.as_deref() {
                            Ok("") => "no instructions",
                            Ok(instructions) => instructions,
                            Err(()) => "no file",
                        },
                    );
                }
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
                        self.wait(Duration::from_millis(20), None);
                    }
                }
                "deaf" => self.deaf = true,
                "fail" => return Ending::Failed(rest.to_owned()),
                _ => self.say(session, "fake"),
            }
            if self.stopping() {
                // A harness takes a moment to stop. Whatever it is told
                // meanwhile, it tells.
                self.wait(pause("FAKE_HARNESS_STOP_MS"), None);
                self.say(session, &format!("[told to stop {} times]", self.cancels));
                return Ending::Stopped("cancelled");
            }
        }
        Ending::Stopped("end_turn")
    }
}
