---
title: Sessions and messages
description: The requests for sessions, their MCP servers, messages and the files that messages carry.
---

## Sessions

### Create a session

```http
POST /v1/agents/{id}/sessions
```

Creates a session, which belongs to the caller. Body, which may be left out: `{"title": "...", "mcp_servers": ["..."]}`. The title is optional and only for display; a session created without one has an empty title. A session begins with the MCP servers of its agent, or with those that the body names. Returns the session, as [The session](#the-session) describes it.

### List sessions

```http
GET /v1/agents/{id}/sessions
```

Lists the sessions of the agent that the caller may read, newest first: `{"sessions": [...]}`. These are the caller's own with `sessions.run`, and everyone's with `sessions.view_all`. `?limit=` is the page size, 20 by default and at most 100. `?before=` takes the ID of a session and returns the sessions before it, which is how to read the next page.

### Read a session

```http
GET /v1/sessions/{id}
```

Returns the session.

### Change the title of a session

```http
PATCH /v1/sessions/{id}
```

Changes the title of a session, also of one that has ended. Body: `{"title": "..."}`, where an empty title leaves the session without one. Returns 200 with the session.

### End a session

```http
DELETE /v1/sessions/{id}
```

Ends a session. Returns 200 with the session, which then has `ended_at`. The session's sandbox is deleted with its snapshot and everything that the agent had in it. The conversation stays readable, and the session accepts no more messages. Returns 409 if a turn of the session has not ended.

## The session

A session, as these requests return it, has:

- **`mcp_servers`.** While it has not ended, a session's `mcp_servers` are
  those that it has.
- **`created_by`.** With an identity provider, its `created_by` is the
  caller's user ID.
- **`sandbox`.** A session's `sandbox` is `awake`, or `asleep` while the
  session sleeps and a snapshot keeps its sandbox. It is
  `lost` if the sandbox is gone although the session has not ended, as after
  it was deleted in Agent Substrate: everything that the agent had in it is
  gone, and the next turn gets a new sandbox. What happens to a sandbox
  outside Aviary shows after a short delay. A session has no `sandbox`
  before its first turn and after it has ended.
- **`last_turn`.** A session with turns carries the newest as `last_turn`:
  the turn as in [Turn records](./turns.md#turn-records),
  without its `text` and `items`.
- **`end_reason`.** A session that ended because it expired has the
  `end_reason` `expired` (see `sandboxes.retentionSeconds` in
  [Configuration](../configuration.md#sandboxes)), one that
  ended because its agent was deleted has `agent_deleted`, and a session
  that a caller ended has none.

## MCP servers of a session

### Turn an MCP server on or off

```http
PUT /v1/sessions/{id}/mcp-servers/{server}
```

Turns an MCP server of the project on or off for a session. Body: `{"enabled": true}`. Returns 200 with the session, and 409 if the session has ended. Besides admins, only the creator of the session may.

### Reset the MCP servers of a session

```http
DELETE /v1/sessions/{id}/mcp-servers
```

Gives a session the MCP servers of its agent again. Returns 200 with the session.

## Messages

### Send a message

```http
POST /v1/sessions/{id}/messages
```

Sends a message. Body: `{"text": "...", "attachments": ["..."]}`, where `attachments` are the IDs of files that were uploaded for the session, and the text may be left out if there are any. Returns 202 with the turn, whose `created_by` is set as for a session; 409 if a turn of the session has not ended or the session has ended; 429 with `Retry-After` if too many turns are queued; 400 `too_many_attachments` if the message names more files than `runtime.maxAttachments`, and 400 `unknown_attachment` if one of them is not a file of the session that no message carries yet. Before the turn starts, the files are written into the session's sandbox at `/home/.attachments/<seq>/<name>`, where `seq` is that of the turn, and they stay there for the turns that follow. The harness gets the text and after it a link to each file, in the order of the message, so that the agent knows which files came with the message, and which of them is the second.

## Attachments

### Upload a file

```http
POST /v1/sessions/{id}/attachments?name=...
```

Uploads a file for a message of the session to carry. The body is the file, `name` its name, `Content-Type` what it is, and `Content-Length` its size. Returns 201 with the attachment: `{"id": "...", "name": "...", "media_type": "...", "size": 0}`. The name is kept without its directories, control characters and leading dots. Besides admins, only the creator of the session may. Returns 411 `length_required` without the size of the file, 413 `attachment_too_large` if the file is larger than `runtime.maxAttachmentBytes`, 507 `storage_full` if the [object storage](../../operate/storage.md#the-object-storage) has no room for it, 403 `attachments_disabled` if the installation takes no attachments, and 409 if the session has ended. A file that no message carries within an hour is deleted.

### Download a file

```http
GET /v1/attachments/{id}
```

Returns the file of an attachment, as a download, to those who may read its session. Returns 404 once the session has ended: the files are kept until then.

### Read the limits

```http
GET /v1/limits
```

Returns what the installation sets for the files that messages carry: `{"max_attachment_bytes": 67108864, "max_attachments": 10}`, its `runtime.maxAttachmentBytes` and `runtime.maxAttachments`. Both are 0 if it takes no attachments.
