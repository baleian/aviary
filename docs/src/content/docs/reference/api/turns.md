---
title: Turns
description: The requests for turns, and what the record of a turn holds.
---

## Turns

### List the turns of a session

```http
GET /v1/sessions/{id}/turns
```

Lists the turns of the session, newest first: `{"turns": [...]}`. `?limit=` is the page size, 20 by default and at most 100. `?before=` takes the `seq` of a turn and returns the turns before it, which is how to read the next page. The first turn of a session has `seq` 1.

### Read a turn

```http
GET /v1/turns/{id}
```

Returns the turn, as [Turn records](#turn-records) describes it.

### Cancel a turn

```http
POST /v1/turns/{id}/cancel
```

Cancels a turn. A queued turn ends at once: 200 with the turn. A running turn is told to stop, and ends when its harness has stopped: 202 with the turn. Returns 409 if the turn has ended in another way.

### Answer an approval request

```http
POST /v1/turns/{id}/approvals/{request}
```

Answers an approval request of the turn. Body: `{"option_id": "..."}`, the `id` of one of the request's options. Returns 202 when the answer is on its way to the harness, 400 if the request has no such option, and 409 if the request already has an answer or the turn has ended.

## Turn records

A turn, in `GET /v1/turns/{id}` and in the session's list of turns, has
`attachments` if its message carries files: each with its `id`, `name`,
`media_type` and `size`, in the order of the message. Two files of one name
have different names there: the second is `report (2).pdf`. A turn also has
`items`: what the agent said and did, in order. The items are assembled
from the events when the turn ends, so a turn that has not ended has none,
and a client follows its events instead. The events are kept for an hour
after the turn ends.

| Item `type` | Fields |
| --- | --- |
| `message`, `thought` | `text`: consecutive pieces, joined into one text. |
| `tool_call` | The fields of `tool.call`, with the call's updates applied. |
| `plan` | `entries`: the plan as it last stood. |
| `mcp_unsupported` | The fields of `mcp.unsupported`. |
| `approval` | The fields of `approval.requested`, and how the request ended: `outcome` and, if an option was selected, `option_id`. |

A turn is `queued` until it leaves the queue, and `running` from then until
it ends, also while its sandbox starts: `started_at` is set once the harness
has received the message.

A turn that did not complete keeps what happened until then: the text that
the agent had written so far, and each tool call with its last status. A
turn's `state`, `failure` and `stop_reason` say how it ended. A turn has a
`stop_reason` only if its harness ended it. A turn without one was stopped
from outside, or never reached its harness.

| `state` | `stop_reason` | Meaning |
| --- | --- | --- |
| `completed` | `end_turn`, `max_tokens`, `max_turn_requests` or `refusal` | The harness ended the turn. |
| `completed` | `cancelled` | The harness ended the turn because a person refused an action that it asked approval for. Codex does this. |
| `cancelled` | `cancelled` | The turn was cancelled, and the harness stopped when it was asked to. |
| `cancelled` | none | Without `started_at`, the turn was cancelled before its harness received the message. Otherwise its harness did not stop in time, and its sandbox was put to sleep. |
| `failed` | none | The turn failed, for the reason in `failure`. |
| `expired` | none | The turn was queued for longer than allowed. |

The items are what the user saw. The harness may remember less of a turn
that did not complete: if what runs in the sandbox is stopped before the
turn ends, as when the turn runs for too long, the harness stops before it
can save what it was saying. With `memory` snapshots the sandbox goes back to
its last snapshot, and what the agent did since is lost. With `rootfs` and
`volumes` the home keeps what the agent did.

`usage` has `total_tokens`, `input_tokens` and `output_tokens`, and
`thought_tokens`, `cached_read_tokens` and `cached_write_tokens` if the
harness reports them. An ended turn has it too. It is what the harness
reports, not a measurement, and harnesses count differently: Codex and
Claude Code report the sum over the model requests of a turn, OpenCode only
the last request.
The log of the egress gateway has every request of a sandbox, and the log of
the Supervisors every secret that went into one, with its turn.
