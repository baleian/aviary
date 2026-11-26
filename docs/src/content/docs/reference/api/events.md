---
title: Events
description: The events of a turn as it runs, why a turn fails, and the changes of the sessions of a project.
---

## Turn events

`GET /v1/turns/{id}/events` streams the events of the turn as server-sent
events. A client that reconnects sends `Last-Event-ID` and receives the
events after it. The events of a turn can come again: after `turn.rewound`,
they follow from `turn.started`, and a client that has put together what
the turn said drops it.

| Event | Data |
| --- | --- |
| `turn.queued` | `position`: the turn's place in the queue, 1 for the turn that runs next. |
| `turn.started` | The harness has received the message. |
| `turn.rewound` | The event log had lost events of the turn. Its events follow again, from `turn.started`. |
| `message.delta`, `thought.delta` | `text`: the next piece of what the agent says or thinks. Pieces with the same `message_id` belong together. |
| `tool.call` | `id`, `title`, `kind`, `status`, `input`: the agent started to use a tool. A tool of an MCP server also has `mcp`: the `server` by ID, its `slug`, and the `tool`. |
| `tool.update` | `id`, and those of `title`, `kind`, `status`, `input` and `output` that changed. Text in `input` or `output` longer than `runtime.maxToolOutputBytes` is truncated, and says so at its end. |
| `plan.update` | `entries`: the agent's plan, each entry with `content`, `priority` and `status`. |
| `session.update` | `title`: a title that the harness gives the session. Aviary does not set it: a client that wants it changes the title of the session. |
| `mcp.unsupported` | `servers`: the MCP servers that the session has on but whose transport the harness does not support, each with `id`, `name` and `transport`. The turn runs without them. |
| `approval.requested` | The harness asks a person before it acts, and waits. `id`: the request. `tool_call`: what the harness is about to do, with the fields of `tool.call`. `options`: the possible answers, each with `id`, `name` and `kind`, which is `allow_once`, `allow_always`, `reject_once` or `reject_always`. |
| `approval.resolved` | The harness has its answer to the request `id`. `outcome`: `selected` with the chosen `option_id`, or `cancelled` if the turn was stopped before an answer came. `answered_by`: with an identity provider, the ID of the user who answered. |
| `turn.completed` | `stop_reason`: why the harness ended the turn. `usage`: the token usage that the harness reported, if any. The last event of the turn. |
| `turn.failed` | The turn did not complete. `reason`: one of the reasons below. `message`: what is known of the failure. The last event of the turn. |
| `turn.cancelled` | The turn was cancelled. The last event of the turn. |
| `turn.expired` | The turn was queued for longer than `runtime.maxQueueWaitSeconds`. The last event of the turn. |

The events that come from the agent are passed on as the runner emits them:
see `runner/src/main.rs`.

## Failure reasons

| Failure reason | Meaning |
| --- | --- |
| `harness_error` | The harness could not be started or returned an error. This is also how a harness reports that the model endpoint refused it. |
| `resume_failed` | The harness could not continue the session, because its saved conversation is missing or unreadable. The next turn of the session tries again, so a session whose harness cannot recover fails every turn. |
| `startup_timed_out` | The harness did not receive the message within `runtime.maxStartupSeconds`, for example because the sandbox could not be created. The message says what the turn waited for, such as a worker with room for the sandbox. A sandbox that was still being created or woken is kept with the agent's files, and the session's next turn uses it. |
| `timed_out` | The turn ran for longer than `runtime.maxTurnSeconds`. |
| `sandbox_lost` | The session's sandbox is gone, with everything that the agent had in it. The next turn of the session gets a new one. |
| `attachment_failed` | A file that the message carries could not be written into the session's sandbox, because its home has no room. The message names the file. |
| `session_expired` | The session was not used for longer than `sandboxes.retentionSeconds`. Its sandbox is gone, and the session has ended. A message fails this way only if it arrives before a Supervisor has ended the session. |
| `runner_error` | The runner failed in another way. |

A failed turn also has its reason and message in its `failure` field.
Whether an error of the model endpoint fails the turn depends on the
harness: OpenCode and Claude Code fail the turn, while Codex mostly ends it
normally, with the error as the agent's reply. Claude Code first retries a
request that the endpoint refused with 401, for about three minutes, unless
the response has `x-should-retry: false`.

## Session changes

`GET /v1/projects/{id}/events` reports the sessions of a project as they
change, so that a client can show their state without reading them over and
over. An event has the type `session`, and its data is the session as
`GET /v1/sessions/{id}` returns it. A session is sent when:

- it is created, gets another title or ends,
- an MCP server is turned on or off for it,
- its sandbox wakes, sleeps or is lost,
- its newest turn changes: a message is accepted, the turn starts, an
  approval request is made or answered, the turn is asked to stop, or it
  ends.

Changes that follow each other closely are sent as one event, with the
session as it then is. A caller is sent the sessions that it may read: its
own with `sessions.run`, and everyone's with `sessions.view_all`. What the
agent says and does is not sent here: a client that shows a conversation
follows the events of its turn.

The stream starts with the changes that follow its opening, and cannot be
resumed with `Last-Event-ID`. A client opens the stream and then reads the
sessions that it shows, and does both again when the stream ends. The
Supervisor ends a stream after about five minutes, so that the caller's
token and permissions are checked again, and earlier if the client reads too
slowly.
