---
title: Sessions and messages
description: The requests for sessions, their MCP servers, messages, the files that messages carry and the files of their workspaces.
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
  before its first turn, unless a file was written into its
  [workspace](#workspace), and after it has ended.
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

Returns what the installation sets for the files that messages carry and for a file that is written into a [workspace](#workspace): `{"max_attachment_bytes": 67108864, "max_attachments": 10, "max_file_bytes": 268435456}`, its `runtime.maxAttachmentBytes`, `runtime.maxAttachments` and `runtime.maxFileBytes`. The first two are 0 if it takes no attachments.

## Workspace

The workspace of a session is the directory in which its agent works:
`/home/workspace` of its sandbox. These requests read and write its files
until the session ends, whether a turn runs or not.

- **Paths.** A `path` is relative to the workspace, with `/` between its
  names and without `.` and `..`, as `src/main.go`. An empty path is the
  workspace itself. A link is followed as long as it leads to something in
  the workspace.
- **The sandbox.** A request wakes a sandbox that sleeps, which is in use
  while its files are read or written: see `sandboxes.idleSeconds` in
  [Configuration](../configuration.md#sandboxes). A session without a
  sandbox, as before its first turn or after its sandbox was lost, lists
  nothing, and the first file that is written creates a sandbox for it.
- **Who.** Those who may read the session may read its files. Besides
  admins, only the creator of the session may write them.

Each of them returns 400 `invalid_request` for a path that is not one below the workspace, 409 `session_ended` if the session has ended, 503 `no_room` with `Retry-After` if no worker has room for the sandbox, and 503 `sandbox_unavailable` with `Retry-After` if the sandbox cannot be woken for the moment, as while it is put to sleep, or after it crashed and before it is restored.

### List a directory

```http
GET /v1/sessions/{id}/files?path=...
```

Lists a directory, its directories first and each kind by name: `{"entries": [{"name": "main.go", "type": "file", "size": 1024, "modified_at": "..."}], "next": "..."}`. `?limit=` is the page size, 200 by default and at most 1000. A listing has `next` if the directory has more entries, and `?after=` takes it and returns the entries after them. The `type` of an entry is `file`, `directory`, or `other` for what is neither, and only a file has a `size`, in bytes. A link has its `target`, and the type and the size of what it names; its type is `symlink` if it names nothing in the workspace. Returns 404 if there is no such directory, and 400 `not_a_directory` if the path names something else.

### Search the workspace

```http
GET /v1/sessions/{id}/files/search?query=...
```

Finds the files and directories whose names contain `query`, whatever the case of the letters: `{"entries": [...], "truncated": false}`, where an entry is as in a listing and has its `path` in the workspace. Those whose names begin with the query come first, and then those nearest to the workspace. `?limit=` is the most entries to return, 100 by default and at most 1000. `truncated` says that the workspace has, or may have, more of them. The directories that links name are not searched.

### Read a file

```http
GET /v1/sessions/{id}/files/content?path=...
```

Returns the file, as a download. With a `Range` header that names one range, it returns that part of the file with 206, or 416 `range_not_satisfiable` if the file has no such part. Returns 404 if there is no such file, 400 `is_a_directory` if the path names a directory, and 403 `not_permitted` if the sandbox may not read the file, or if the path names what is neither a file nor a directory, such as a pipe.

### Write a file

```http
PUT /v1/sessions/{id}/files/content?path=...
```

Writes the body as the file at the path, with `Content-Length` its size, in place of a file that is there, and creates the directories above it that are missing. The agent finds the file there once all of it is written. Returns 201 with the entry of the file, as in a listing, or 200 if the file replaced one. Returns 411 `length_required` without the size of the file, 413 `file_too_large` if the file is larger than `runtime.maxFileBytes`, 507 `storage_full` if the sandbox has no room for it, 400 `is_a_directory` if the path names a directory, 400 `not_a_directory` if a name above the file is not that of a directory, and 403 `not_permitted` if the sandbox may not write there.

### Download a directory

```http
GET /v1/sessions/{id}/files/archive?path=...
```

Returns the directory as a ZIP file, as a download. The file is written while it is sent, so a response that is cut off is not all of it. The paths in it begin with the name of the directory, or with `workspace` for the workspace itself. A link is in it as a link, and what the sandbox cannot read is left out. Returns 404 if there is no such directory, and 400 `not_a_directory` if the path names something else.
