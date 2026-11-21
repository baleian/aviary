# Aviary

Aviary runs AI agent harnesses, such as OpenCode and Codex, on Kubernetes.
Each message that a user sends is a turn, and each session runs its turns in
a sandbox of its own: a pod that runs the agent's harness, with a volume
that keeps the conversation and the files of the session between turns. The
sandboxes are managed by
[agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox).

Aviary is meant as a reference for organizations that want to run agents for
many users on their own cluster:

- **Controlled egress.** An agent reaches only the hosts that the
  installation allows, and every request that leaves a sandbox is logged
  with the turn and the user that it came from.
- **No keys in sandboxes.** Each agent names its model at a model provider
  that its project registers, and no sandbox holds the provider's API key.
- **Persistent sessions.** Sessions continue across turns, and their
  conversations can be read back through the API.
- **Sleeping sessions.** A session that is not in use is put to sleep: its
  pod is deleted, so it uses no CPU or memory, and its volume is kept. Its
  next turn continues in a new pod.
- **A queue.** The number of turns that run at once is a setting. Further
  turns wait in order.
- **Approval.** An agent can be made to ask a person before it acts.
- **Access control.** With an identity provider, callers are authenticated
  and limited to what they may do in each project.
- **Replaceable Supervisors.** Supervisors can be replaced while turns run.

> **Status:** Aviary is at an early stage and has only run on a local
> cluster. See [Limitations](#limitations).

## How it works

```
            messages, events              creates, wakes, puts to sleep, deletes
  client ───────────────────▶ Supervisor ─────────────────────────▶ agent-sandbox
                               │  ▲   ▲                                  │
                    database   │  │   │ HTTP: turns down,                ▼
                    event log  │  │   └─ events up ───────────────── session sandbox
                                  │                                ┌─────────────────┐
                                  │                                │ runner          │
                                  │                                │   │ ACP         │
                                  │                                │ harness         │
                                  │                                │   /home: volume │
                                  │                                └────────┬────────┘
                                  │ asks about each request:                │ all outbound traffic
                                  └─ its turn, the model API key ──── Egress Proxy ──▶ model endpoint,
                                                                                       allowed hosts
```

1. A client creates a project with a model provider and an agent that uses
   one of the provider's models, creates a session of the agent, and sends
   the session a message.
2. The **Supervisor** records the message as a turn. If fewer turns are
   running than the limit allows, a Supervisor takes the turn and follows it
   until it ends. Otherwise the turn is queued, and queued turns run in the
   order in which they were sent.
3. The Supervisor has agent-sandbox create the session's **sandbox**, a pod
   that runs the agent's harness with the session volume, or wake it if it
   sleeps. It then gives the turn to the **runner** in the sandbox. The
   runner starts the **harness** on the first turn and talks to it over the
   [Agent Client Protocol](https://agentclientprotocol.com), so it does not
   depend on which harness it runs.
4. The harness works on the message with the volume as its home, `/home`.
   The runner reports what the agent does, the Supervisor records it, and
   the client reads it as a stream of events. If the Supervisor goes away,
   another one continues the turn from what was recorded.
5. When the runner reports that the turn has ended, the Supervisor ends the
   turn and stores what the agent said and did. The harness and the files
   stay in the sandbox for the next turn. A session with no running turn is
   put to sleep after a while: its pod is deleted, and its volume is kept
   with the harness's memory of the conversation. The next message wakes the
   session in a new pod, and the harness continues from there.
6. Everything that the sandbox sends out goes through the
   [Egress Proxy](#egress-proxy).

The Supervisor keeps its records in a database and the events of running
turns in an event log, and reaches each through a plugin. Plugins for
PostgreSQL, MySQL and Valkey are included. They need only a URL, so both can
be managed services. [Plugins](PLUGINS.md) lists the tested products and
versions, and describes how to connect other products. A model provider is a
URL and an API key, so it can be a provider's API, an AI gateway or your own
model server.

### Egress Proxy

The Egress Proxy is [Envoy](https://www.envoyproxy.io), which the chart
installs. A network policy allows a sandbox to connect only to the proxy,
and the sandbox has no DNS.

A sandbox holds no model API key, only the token of its session. For each
request, the proxy asks the Supervisor which turn runs in the token's
session:

- For a model request, the Supervisor also returns the endpoint and the API
  key of the agent's provider. The proxy puts the key in place of the token
  and forwards the request to that endpoint.
- Any other request is let through to the hosts that the installation
  allows.
- A request from a session in which no turn runs is refused.

The proxy logs every request as a line of JSON on its standard output. Each
line identifies the origin of its request on its own:

- `turn`, `session`, `agent` and `project`.
- With an identity provider, `user` and `subject`: who sent the turn's
  message, as the user ID that `GET /v1/me` returns and as the identity
  provider's subject, which identifies the user without Aviary's records.
- For a model request, `provider`: the model provider that it went to.
- `time`, `host`, `path` (except for HTTPS URLs), `status`,
  `request_bytes`, `response_bytes` and `duration_ms`.

How long the lines are kept depends on the cluster's log collection.

## Try it locally

You need Docker, [kind](https://kind.sigs.k8s.io), kubectl, Helm, jq, make,
curl, openssl and bash. Tested with kind 0.33, Helm 4.3 and Docker 29 on
Linux.

```sh
make cluster-up   # create a kind cluster with agent-sandbox, PostgreSQL and Valkey
make images       # build the images and load them into the cluster
make deploy       # install Aviary with its Helm chart
```

`make cluster-up DATABASE=mysql` uses MySQL instead of PostgreSQL.
`make cluster-down` deletes the cluster, including the session volumes,
which are directories on its nodes.

`make headlamp` installs [Headlamp](https://headlamp.dev), a web UI for the
resources, logs and events of the cluster, at http://localhost:4466.

`make grafana` installs [Prometheus](https://prometheus.io), which scrapes
the Supervisors and the Egress Proxies, and [Grafana](https://grafana.com)
with the Aviary dashboard, at http://localhost:3000.

### Send a message

`make deploy` serves the API at http://localhost:8080. Register a model
provider for a project, and send a message. A model provider is the base
URL of a model endpoint and its API key. The endpoint must be reachable from
the cluster, so `localhost` does not work, and an endpoint at a private
address needs an entry in `egressProxy.extraEgress` (see
[Configuration](#configuration)). OpenCode needs an endpoint with the OpenAI
Chat Completions API, and Codex one with the Responses API.

```sh
project=$(curl -s -X POST localhost:8080/v1/projects -d '{"slug": "demo"}' | jq -r .id)
provider=$(curl -s -X POST localhost:8080/v1/projects/$project/providers \
  -d '{"name": "a model server", "url": "https://model-server.example.com/v1", "api_key": {"value": "..."}}' | jq -r .id)
agent=$(curl -s -X POST localhost:8080/v1/projects/$project/agents \
  -d '{"slug": "assistant", "harness": "opencode", "model": {"provider": "'$provider'", "name": "..."}}' | jq -r .id)
session=$(curl -s -X POST localhost:8080/v1/agents/$agent/sessions | jq -r .id)

turn=$(curl -s -X POST localhost:8080/v1/sessions/$session/messages \
  -d '{"text": "Create a file named hello.txt that says hello."}' | jq -r .id)
curl -N localhost:8080/v1/turns/$turn/events
```

The last command prints the events of the turn as they happen, and returns
when the turn has ended. Send the session another message: the agent still
knows the conversation and finds the file.

## API

With an identity provider, every request needs a token of that provider in
the `Authorization: Bearer <token>` header (see
[Authentication](#authentication)). A caller has the permissions that a
project grants to the caller and to the caller's groups (see
[Roles](#roles)). A project that the caller is not a member of returns 404,
and a request that needs a permission that the caller lacks returns 403.
Administrators may do everything. Without an identity provider, requests are
not authenticated, and every caller is an administrator.

Paths name a project or an agent by its ID. Each also has a slug, chosen
when it is created: 1 to 63 lowercase letters, digits and hyphens, beginning
with a letter and not ending with a hyphen. A project's slug is unique in the
installation, and an agent's slug is unique in its project. Slugs cannot be
changed. The name of a project or an agent is optional and only for display;
a client shows the slug when the name is empty.

| Request | Description |
| --- | --- |
| `POST /v1/projects` | Creates a project. Body: `{"slug": "...", "name": "..."}`. Returns 409 if another project has the slug. Administrators only. |
| `POST /v1/projects/{id}/providers` | Creates a model provider in the project. Body: `{"name": "...", "url": "...", "api_key": {"value": "..."}}`. The URL is the base URL of a model endpoint, and the key is sent to it as a bearer token. A key in a secret store of the project is given by its location: `"api_key": {"store": "...", "path": "...", "key": "..."}`. An endpoint that takes no key has no `api_key`. See [Model providers](#model-providers). |
| `PUT /v1/providers/{id}` | Replaces the name, the URL and the key of a provider together: a provider given no key has none afterwards. |
| `POST /v1/projects/{id}/secret-stores` | Registers a secret store of the project. Body: `{"name": "...", "kind": "...", "settings": {...}, "credential": {"value": "..."}}`. See [Secret stores](#secret-stores). |
| `PUT /v1/secret-stores/{id}` | Replaces the name, the kind, the settings and the credential of a secret store together. |
| `POST /v1/projects/{id}/agents` | Creates an agent. Body: `{"slug": "...", "name": "...", "description": "...", "harness": "...", "model": {"provider": "...", "name": "..."}, "instructions": "...", "approval": "ask"}`. The optional description says what the agent is for, and like the name is only for display. The harness is one of those that `GET /v1/harnesses` lists. The model is a provider of the project, by ID, and the provider's name for the model. The optional instructions are given to the agent before every turn. With `"approval": "ask"`, the agent asks a person before it acts (see [Approval](#approval)); without it, it asks nobody. Returns 409 if another agent of the project has the slug. |
| `POST /v1/agents/{id}/sessions` | Creates a session, which belongs to the caller. Body, which may be left out: `{"title": "..."}`. The title is optional and only for display; a session created without one has an empty title. With an identity provider, its `created_by` is the caller's user ID. A session's `sandbox` is `awake`, or `asleep` while the session sleeps. It is `lost` if the sandbox is gone although the session has not ended, as after it was deleted in the cluster: everything that the agent had in it is gone, and the next turn gets a new sandbox. What happens to a sandbox outside Aviary shows after a short delay. A session has no `sandbox` before its first turn and after it has ended. A session with turns carries the newest as `last_turn`: the turn as in [Turn records](#turn-records), without its `text` and `items`. A session that ended because it expired has the `end_reason` `expired` (see `sandboxes.retentionSeconds` in [Configuration](#configuration)), and a session that a caller ended has none. |
| `PATCH /v1/sessions/{id}` | Changes the title of a session, also of one that has ended. Body: `{"title": "..."}`, where an empty title leaves the session without one. Returns 200 with the session. |
| `POST /v1/sessions/{id}/messages` | Sends a message. Body: `{"text": "..."}`. Returns 202 with the turn, whose `created_by` is set as for a session; 409 if a turn of the session has not ended or the session has ended; 429 with `Retry-After` if too many turns are queued. |
| `DELETE /v1/sessions/{id}` | Ends a session. Returns 200 with the session, which then has `ended_at`. The session's sandbox is deleted with its volume and everything that the agent had in it, unless the storage class keeps the data of deleted volumes (see `runtime.volume` in [Configuration](#configuration)). The conversation stays readable, and the session accepts no more messages. Returns 409 if a turn of the session has not ended. |
| `POST /v1/turns/{id}/cancel` | Cancels a turn. A queued turn ends at once: 200 with the turn. A running turn is told to stop, and ends when its harness has stopped: 202 with the turn. Returns 409 if the turn has ended in another way. |
| `POST /v1/turns/{id}/approvals/{request}` | Answers an approval request of the turn. Body: `{"option_id": "..."}`, the `id` of one of the request's options. Returns 202 when the answer is on its way to the harness, 400 if the request has no such option, and 409 if the request already has an answer or the turn has ended. |
| `GET /v1/turns/{id}/events` | Streams the events of the turn as server-sent events. A client that reconnects sends `Last-Event-ID` and receives the events after it. |
| `GET /v1/projects/{id}/events` | Streams the sessions of the project as server-sent events, each when it changes. See [Session changes](#session-changes). |
| `GET /v1/sessions/{id}/turns` | Lists the turns of the session, newest first: `{"turns": [...]}`. `?limit=` is the page size, 20 by default and at most 100. `?before=` takes the `seq` of a turn and returns the turns before it, which is how to read the next page. The first turn of a session has `seq` 1. |
| `GET /v1/projects/{id}`, `/v1/providers/{id}`, `/v1/secret-stores/{id}`, `/v1/agents/{id}`, `/v1/sessions/{id}`, `/v1/turns/{id}` | Returns the resource. |
| `GET /v1/me` | Returns the caller, as the identity provider identifies it. See [Authentication](#authentication). |
| `GET /v1/projects` | Lists the caller's projects, oldest first: `{"projects": [...]}`. These are the projects that the caller or one of the caller's groups is a member of, and all projects for an administrator. |
| `GET /v1/projects/{id}/agents` | Lists the agents of the project in the order in which they were created: `{"agents": [...]}`. |
| `GET /v1/projects/{id}/providers` | Lists the model providers of the project in the order in which they were created: `{"providers": [...]}`. |
| `GET /v1/projects/{id}/secret-stores` | Lists the secret stores of the project in the order in which they were registered: `{"secret_stores": [...]}`. |
| `GET /v1/agents/{id}/sessions` | Lists the sessions of the agent that the caller may read, newest first: `{"sessions": [...]}`. These are the caller's own with `sessions.run`, and everyone's with `sessions.view_all`. `?limit=` is the page size, 20 by default and at most 100. `?before=` takes the ID of a session and returns the sessions before it, which is how to read the next page. |
| `GET /v1/projects/{id}/members` | Lists the members of the project in the order in which they were added: `{"members": [...]}`. See [Roles](#roles). |
| `POST /v1/projects/{id}/members` | Adds a user or a group to the project. Body: `{"user": "..."}` with a username, or `{"group": "..."}` with the name of a group of the identity provider, and with it `{"role": "..."}` or `{"permissions": [...]}`. Administrators only. Returns 409 if the project already has that member. |
| `PUT /v1/members/{id}` | Gives a member another role, or other permissions of its own. Body: `{"role": "..."}` or `{"permissions": [...]}`. Administrators only. |
| `DELETE /v1/members/{id}` | Removes a member from its project. Returns 204. Administrators only. |
| `GET /v1/harnesses` | Lists the harnesses that agents can use, by name: `{"harnesses": [{"name": "codex"}, {"name": "opencode"}]}`. They are the installation's `runtime.harnesses`. |
| `GET /v1/roles` | Lists the roles and their permissions: `{"roles": [{"name": "editor", "permissions": [...]}, ...]}`. See [Roles](#roles). |
| `PUT /v1/roles/{name}` | Sets the permissions of a role. Body: `{"permissions": [...]}`. Administrators only: other callers get 403. Returns 400 for an unknown permission and 404 for an unknown role. |

### Turn events

| Event | Data |
| --- | --- |
| `turn.queued` | `position`: the turn's place in the queue, 1 for the turn that runs next. |
| `turn.started` | The harness has received the message. |
| `message.delta`, `thought.delta` | `text`: the next piece of what the agent says or thinks. Pieces with the same `message_id` belong together. |
| `tool.call` | `id`, `title`, `kind`, `status`, `input`: the agent started to use a tool. |
| `tool.update` | `id`, and those of `title`, `kind`, `status`, `input` and `output` that changed. Text in `input` or `output` longer than `runtime.maxToolOutputBytes` is truncated, and says so at its end. |
| `plan.update` | `entries`: the agent's plan, each entry with `content`, `priority` and `status`. |
| `approval.requested` | The harness asks a person before it acts, and waits. `id`: the request. `tool_call`: what the harness is about to do, with the fields of `tool.call`. `options`: the possible answers, each with `id`, `name` and `kind`, which is `allow_once`, `allow_always`, `reject_once` or `reject_always`. |
| `approval.resolved` | The harness has its answer to the request `id`. `outcome`: `selected` with the chosen `option_id`, or `cancelled` if the turn was stopped before an answer came. `answered_by`: with an identity provider, the ID of the user who answered. |
| `turn.completed` | `stop_reason`: why the harness ended the turn. `usage`: the token usage that the harness reported, if any. The last event of the turn. |
| `turn.failed` | The turn did not complete. `reason`: one of the reasons below. `message`: what is known of the failure. The last event of the turn. |
| `turn.cancelled` | The turn was cancelled. The last event of the turn. |
| `turn.expired` | The turn was queued for longer than `runtime.maxQueueWaitSeconds`. The last event of the turn. |

The events that come from the agent are passed on as the runner emits them:
see [`runner/src/main.rs`](runner/src/main.rs).

| Failure reason | Meaning |
| --- | --- |
| `harness_error` | The harness could not be started or returned an error. This is also how a harness reports that the model endpoint refused it. |
| `resume_failed` | The harness could not continue the session, because its saved conversation is missing or unreadable. The next turn of the session tries again, so a session whose harness cannot recover fails every turn. |
| `startup_timed_out` | The harness did not receive the message within `runtime.maxStartupSeconds`, for example because the sandbox could not be created. The message says what the turn waited for. A sandbox that was still being created or woken is kept with the agent's files, and the session's next turn uses it. |
| `timed_out` | The turn ran for longer than `runtime.maxTurnSeconds`. |
| `sandbox_lost` | The session's sandbox is gone, with everything that the agent had in it. The next turn of the session gets a new one. |
| `session_expired` | The session was not used for longer than `sandboxes.retentionSeconds`. Its sandbox is gone, and the session has ended. A message fails this way only if it arrives before a Supervisor has ended the session. |
| `runner_error` | The runner failed in another way. |

A failed turn also has its reason and message in its `failure` field.
Whether an error of the model endpoint fails the turn depends on the
harness: OpenCode fails the turn, while Codex mostly ends it normally, with
the error as the agent's reply.

### Turn records

A turn, in `GET /v1/turns/{id}` and in the session's list of turns, has
`items`: what the agent said and did, in order. The items are assembled
from the events when the turn ends, so a turn that has not ended has none,
and a client follows its events instead. The events are kept for an hour
after the turn ends.

| Item `type` | Fields |
| --- | --- |
| `message`, `thought` | `text`: consecutive pieces, joined into one text. |
| `tool_call` | The fields of `tool.call`, with the call's updates applied. |
| `plan` | `entries`: the plan as it last stood. |
| `approval` | The fields of `approval.requested`, and how the request ended: `outcome` and, if an option was selected, `option_id`. |

A turn that did not complete keeps what happened until then: the text that
the agent had written so far, and each tool call with its last status. A
turn's `state`, `failure` and `stop_reason` say how it ended. A turn has a
`stop_reason` only if its harness ended it. A turn without one was stopped
from outside, or never reached its harness: `started_at` is set once the
harness has received the message.

| `state` | `stop_reason` | Meaning |
| --- | --- | --- |
| `completed` | `end_turn`, `max_tokens`, `max_turn_requests` or `refusal` | The harness ended the turn. |
| `completed` | `cancelled` | The harness ended the turn because a person refused an action that it asked approval for. Codex does this. |
| `cancelled` | `cancelled` | The turn was cancelled, and the harness stopped when it was asked to. |
| `cancelled` | none | Without `started_at`, the turn was cancelled before its harness received the message. Otherwise its harness did not stop in time, and its sandbox was put to sleep. |
| `failed` | none | The turn failed, for the reason in `failure`. |
| `expired` | none | The turn was queued for longer than allowed. |

The items are what the user saw. The harness may remember less of a turn
that did not complete: if the sandbox is put to sleep before the turn ends,
as when the turn runs for too long, the harness stops with its pod before it
can save what it was saying. The next turn wakes the sandbox with the volume
as it was.

`usage` has `total_tokens`, `input_tokens` and `output_tokens`, and
`thought_tokens`, `cached_read_tokens` and `cached_write_tokens` if the
harness reports them. An ended turn has it too. It is what the harness
reports, not a measurement, and harnesses count differently: Codex reports
the sum over the model requests of a turn, OpenCode only the last request.
The Egress Proxy log is reliable: it has every model request, with its turn.

### Approval

An agent created with `"approval": "ask"` asks a person before it acts.
Before the harness acts, the turn emits `approval.requested`, and the
harness waits. A client answers with `POST /v1/turns/{id}/approvals/{request}`
and one of the options that the harness offered. The harness continues with
that answer, and the turn emits `approval.resolved`, which says who
answered. While a request waits, it is listed in the turn's `approvals`,
both in `GET /v1/turns/{id}` and in the session's list of turns, so a client
that connects later can find it.

- **What needs approval depends on the harness** and on how its image
  configures it. Aviary passes the requests and the answers on unchanged. In
  the images in this repository, OpenCode asks before it runs a command or
  changes a file, and Codex before a command runs outside its sandbox: see
  [`images/README.md`](images/README.md).
- **A request waits as long as its turn may run.** The time spent waiting
  counts toward `runtime.maxTurnSeconds`, because the sandbox keeps running
  and the turn keeps its place among the running turns. A turn whose time
  runs out while it waits fails as `timed_out`, and the session's next
  message continues the conversation. Cancelling the turn ends its requests
  as `cancelled`.
- **What an option does depends on the harness too.** `allow_always` lasts
  as long as the harness remembers it: in OpenCode until the harness process
  ends, for example when the session sleeps, and in Codex for the rest of
  the session. After a refusal, both end the turn, Codex with the stop
  reason `cancelled`.
- **Approval is not a security boundary.** The harness that asks runs in
  the sandbox, next to the agent's tools and as the same user. Approval
  keeps a well-meaning agent from acting on a mistake. What a sandbox can
  reach is limited by the network policy and the Egress Proxy, whatever was
  approved.

An agent without `approval` asks nobody. If its harness asks for permission
anyway, the runner allows the action once.

### Session changes

`GET /v1/projects/{id}/events` reports the sessions of a project as they
change, so that a client can show their state without reading them over and
over. An event has the type `session`, and its data is the session as
`GET /v1/sessions/{id}` returns it. A session is sent when:

- it is created, gets another title or ends,
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

### Authentication

Aviary does not sign users in or store passwords. The installation names an
identity provider in `supervisor.auth.provider`, and a caller sends a token
of that provider with every request. A request without a token that the
provider issued for this installation returns 401. A caller becomes a user
of the installation with its first request. `GET /v1/me` returns the user's
ID, what the installation read from the token, and whether the caller is an
administrator: `{"id": "...", "subject": "...", "username": "...",
"display_name": "...", "groups": [...], "admin": false}`.

The groups that the provider reports for a caller decide what the caller
may do. Aviary has no built-in groups: the installation names groups of its
provider in its configuration. Members of the groups in
`supervisor.auth.admins` are administrators. Members of the groups in
`supervisor.auth.users` may use the API as well, and if no group is listed
there, every user that the provider authenticates may. Other callers get
403, but can still call `GET /v1/me`. Without an identity provider, every
caller is an administrator.

### Identity providers

A provider of the kind `oidc` is an OpenID Connect issuer:

```yaml
supervisor:
  auth:
    provider:
      kind: oidc
      settings:
        issuer: https://login.example.com/realms/main
        audience: aviary
        claims: {subject: sub, username: email, displayName: name, groups: groups}
```

- **`issuer`** is the URL under which the provider publishes its
  configuration and its signing keys. A Supervisor reads the configuration
  when it starts, and does not start if it cannot.
- **`audience`** is the audience that a token must be issued for. Tokens
  are JWTs, ID tokens or access tokens, signed by the issuer for that
  audience and not expired.
- **`claims`** names the claims that identify the caller. `subject` is the
  caller's permanent ID at the provider, `sub` by default. `username` is a
  human-readable name that no other caller has, and a caller has one only if
  the settings name its claim. `displayName` defaults to `name`, and
  `groups` to `groups`. A claim inside another is named by its path, such
  as `realm_access.roles`. An email address is accepted as a username only
  if the token has `"email_verified": true`.

To try it locally, `make identity-provider` installs Keycloak in dev mode in
the local cluster, with the realm in `hack/kind/keycloak.yaml`. The realm
has four users, whose passwords are their names: `alice` is an
administrator, `bob` and `carol` may use the API, `carol` is also in the
group `team-a`, and `dave` is in no group. Aviary uses this provider when it
is installed with `make deploy VALUES=hack/kind/aviary-keycloak-values.yaml`.
The provider is at http://localhost:8081. A caller gets a token from it and
sends it:

```sh
token=$(curl -s localhost:8081/realms/aviary/protocol/openid-connect/token \
  -d grant_type=password -d client_id=aviary -d username=alice -d password=alice | jq -r .access_token)
curl -s -H "Authorization: Bearer $token" localhost:8080/v1/me
```

### Roles

A role is a named set of permissions in a project. Administrators create
projects, manage their members and change the permissions of the roles.
Everything else needs a permission in the project:

| Permission | Allows |
| --- | --- |
| `project.view` | Viewing the project and its agents, model providers and secret stores, without their secrets. Every member has it. |
| `agents.manage` | Creating agents in the project. |
| `connections.manage` | Registering and replacing the project's model providers and secret stores. |
| `sessions.run` | Creating sessions, and in one's own sessions: sending messages, stopping turns, answering approval requests, reading the conversation, changing the title and ending the session. |
| `sessions.view_all` | Reading the sessions of other members. |
| `sessions.control_all` | Stopping the turns of other members, answering their approval requests, and changing the titles of their sessions and ending them. Includes `sessions.view_all`. |

A member is a user, by username, or a group of the identity provider, which
includes everyone that the provider puts in it. A member has a role, or
permissions of its own, shown as `"role": "custom"` with the permissions. A
member with a role has whatever the role allows at the time. A member with
permissions of its own keeps them as they were given.

A user can be made a member before their first request: the user who is
then seen with that username becomes the member. From then on the
membership belongs to that user, not to the username: if the provider gives
the username to someone else, the member is still the original user, and
the other is not a member.

A session belongs to the caller who created it, and only that caller can
send it messages. Reading one's own session, stopping its turns, answering
their approval requests, changing its title and ending it need
`sessions.run`. For the sessions of other members, reading needs
`sessions.view_all`, and stopping, answering, changing the title and ending
need `sessions.control_all`.

There are exactly five roles. An administrator can change the permissions
of a role with `PUT /v1/roles/{name}`, which applies to everyone with the
role from then on. The defaults are:

| Role | Permissions |
| --- | --- |
| `owner` | All of them. |
| `editor` | `project.view`, `agents.manage`, `connections.manage`, `sessions.run` |
| `user` | `project.view`, `sessions.run` |
| `viewer` | `project.view` |
| `auditor` | `project.view`, `sessions.view_all` |

### Model providers

The turns of an agent send their model requests to the endpoint of the
agent's provider, for the agent's model.

- **Keys are write-only.** A key is stored encrypted with a key of the
  installation, which the database does not hold. A provider is returned
  with `"api_key": {"stored": true}` in place of its key.
- **A key can stay in a secret store of the project.** Aviary then stores
  where the key is, not the key, and reads it from the store when a model
  request needs it. See [Secret stores](#secret-stores).
- **A key goes only to its endpoint.** The Egress Proxy adds it to the model
  requests of the turns that use the provider, and forwards them to the
  host of the provider's URL, under the URL's path. Nothing else that a
  sandbox sends carries a key, and a sandbox cannot choose where its model
  requests go.
- **A provider can be replaced while turns use it.** Their next model
  request uses the new endpoint and key.
- **The host of a provider is subject to `egressProxy.allowedHosts`** like
  any other host: if the list names hosts, it must allow the provider's.

### Secret stores

A project that keeps its secrets in a store of its own can register the
store, and then give the location of a secret in the store instead of the
secret itself. The key of a model provider is such a secret.

```sh
store=$(curl -s -X POST localhost:8080/v1/projects/$project/secret-stores \
  -d '{"name": "team store", "kind": "kv-v2", "credential": {"value": "..."},
       "settings": {"address": "https://secrets.example.com:8200", "mount": "secret", "role_id": "..."}}' | jq -r .id)
provider=$(curl -s -X POST localhost:8080/v1/projects/$project/providers \
  -d '{"name": "a model server", "url": "https://api.example.com/v1",
       "api_key": {"store": "'$store'", "path": "team/model", "key": "api_key"}}' | jq -r .id)
```

- **The kind says how the store is accessed.** `kv-v2` is a store that
  serves the KV version 2 HTTP API and accepts AppRole logins, such as
  OpenBao. Its settings are its address, the mount of its secrets and the
  role ID, and its credential is the role's secret ID. A secret is at a path
  below the mount and holds values under keys. Another kind of store needs a
  [plugin](PLUGINS.md).
- **The installation decides where stores may be.** The Supervisor connects
  to the stores itself, so a project can register a store only at a host
  that `supervisor.secretStores.allowedHosts` lists. By default it lists
  none.
- **Errors are reported at once.** The Supervisor logs in to a store before
  it accepts the store, and reads a key from its location before it accepts
  the provider.
- **Values are read when they are used.** A Supervisor caches a value for
  `supervisor.secretStores.cacheSeconds`, 60 by default, and never uses it
  for longer: a key that changes in the store reaches the model requests
  within that time, and a key that is removed from the store stops being
  used within that time too. The cache is bounded in size, and a value
  larger than 8 KiB is not accepted as a secret.
- **A key that cannot be read fails the request.** The model request gets a
  503 with the reason, and the turns of other providers continue.
- **The credential is stored encrypted**, like the key of a provider. A
  store is returned with `"credential": {"stored": true}`.
- **Give the role no more access than Aviary needs.** Anyone who can create
  a provider in the project can have any value that the role can read sent
  to an endpoint of their choice.

To try it locally, `make secret-store` installs OpenBao in dev mode in the
local cluster, which keeps its data in memory. It is at
`http://openbao.aviary-data:8200`, a host that
`supervisor.secretStores.allowedHosts` must then list, and its root token is
in the secret `openbao-root` in the namespace `aviary-data`.

## Configuration

Aviary is installed with the Helm chart in `deploy/charts/aviary`. The
cluster needs:

- **agent-sandbox** v1.0.11, which the chart does not install.
  `make cluster-up` installs it from its release.
- **A database and an event log**, which the chart does not install either.
- **A network plugin that enforces network policies.** The policies are what
  isolate the sandboxes.

The sandboxes run in the namespace `aviary-runtime`, which enforces the
restricted Pod Security Standard. The local cluster runs Kubernetes 1.37,
and `make deploy VALUES=<file>` installs the chart there with a values file
of your own.

| Value | Description |
| --- | --- |
| `supervisor.replicas`, `egressProxy.replicas` | Number of Supervisor and Egress Proxy replicas. Any replica can serve any request. Default 1. |
| `supervisor.resources`, `egressProxy.resources` | Resource requests and limits of the Supervisor and Egress Proxy pods. |
| `egressProxy.concurrency` | Worker threads of each Egress Proxy. Default 2. 0 lets Envoy choose: as many as the CPUs of its node, or of its CPU limit if it has one. |
| `supervisor.database.secretName`, `.secretKey` | Secret in the release namespace that holds the database URL. The URL scheme selects the plugin: `postgresql://…` for PostgreSQL, `mysql://…` for MySQL. |
| `supervisor.events.secretName`, `.secretKey` | Secret in the release namespace that holds the event log URL: `valkey://…` for Valkey, `valkeys://…` for Valkey with TLS. |
| `supervisor.encryption.secretName`, `.key` | Secret with the keys that encrypt secrets, such as model provider API keys, before they are stored, and the entry that encrypts new secrets. Each entry is 32 random bytes in base64. To rotate, add an entry and name it in `key`, and keep the old entries. |
| `supervisor.secretStores.allowedHosts` | Hosts at which projects may register secret stores: `*` for any host, a host name for the host on the default HTTPS and HTTP ports, or a host name with a port. Empty, the default, disables secret stores. |
| `supervisor.secretStores.cacheSeconds` | Seconds that a value read from a secret store is cached. Default 60. 0 reads the store for every model request. |
| `supervisor.auth.provider` | The identity provider that authenticates API requests: its `kind`, and `settings` for a provider of that kind. If `kind` is empty, the default, requests are not authenticated. See [Authentication](#authentication). |
| `supervisor.auth.admins`, `.users` | Groups of the identity provider whose members administer the installation, and groups whose members may use the API besides the administrators. Both are empty by default: nobody is an administrator, and every authenticated user may use the API. |
| `egressProxy.allowedHosts` | Hosts that the sandboxes may reach, including those of model providers: `*` for any host, a host name for the host on the default HTTPS and HTTP ports, or a host name with a port. Subdomains are separate hosts. Default `*`. |
| `egressProxy.insideCIDRs`, `.extraEgress` | Address ranges that a NetworkPolicy blocks the Egress Proxy from connecting to, so that the sandboxes cannot use it to reach into the cluster, and additional NetworkPolicy egress rules for the proxy. A model provider or an allowed host that is in the cluster or at a private address needs such a rule. |
| `sandboxes.idleSeconds` | Seconds without a running turn after which a session is put to sleep: its pod is deleted and its volume is kept, and its next turn wakes it in a new pod. Default 300. 0 disables sleeping. |
| `sandboxes.retentionSeconds` | Seconds after its last turn after which a session ends: its sandbox is deleted with its volume, and its conversation stays readable. A session that ends this way has the `end_reason` `expired`. Default 0, which keeps the sandbox until the session is ended. |
| `runtime.maxConcurrentTurns` | Maximum number of turns that run at once. Default 50. |
| `runtime.maxQueuedTurns` | Maximum number of queued turns. Beyond it, new messages get 429. Default 500. 0 means no limit. |
| `runtime.maxQueueWaitSeconds` | Seconds that a turn may stay queued before it expires. Default 120. 0 means no limit. |
| `runtime.maxStartupSeconds` | Seconds that a turn may take to reach its harness, including creating or waking the sandbox. A turn that takes longer fails. Default 120. 0 means no limit. |
| `runtime.maxTurnSeconds` | Seconds that a turn may run after it leaves the queue, including the time that it waits for approval. A turn that runs longer fails, and its sandbox is put to sleep. Default 1800. |
| `runtime.maxToolOutputBytes` | Maximum bytes of the input or output of a tool call that turn events report. Longer text is truncated in the events. Default 65536. 0 means no limit. |
| `runtime.harnesses` | Harnesses that agents can use, by name, each with its image and the `resources` of its sandbox pods, in the format of Kubernetes container resources. A change applies to a sandbox the next time it wakes. |
| `runtime.volume.size`, `.storageClass` | The session volume, mounted at `/home`: the state of the harness, the files of the agent and what the agent installs with npm or pip. Default 10Gi, in the default storage class of the cluster. The volume is deleted with its sandbox when its session is ended or expires. Whether its data is deleted too depends on the storage class: the default class of the local cluster deletes it, a class with `reclaimPolicy: Retain` keeps it, and some file storage drivers keep it unless they are configured not to. |

Sandbox pods exist for the running turns and for the sessions that were in
use within the last `sandboxes.idleSeconds`. Volumes exist for the sessions
that have not ended. Aviary sets no other limit on them; to set one, use a
ResourceQuota on `aviary-runtime`.

Supervisors and Egress Proxies do not wait for turns when they stop, as when
their deployment is scaled in or rolled out. Another Supervisor continues
the turns from what was recorded, and the requests that a stopping proxy was
forwarding fail.

To run a harness that is not in this repository, see
[Harness images](images/README.md). To connect Aviary to a database, an
event log or a secret store that it has no plugin for, see
[Plugins](PLUGINS.md).

## Metrics

A Supervisor serves Prometheus metrics on port 9092 at `/metrics`. The port
is not part of the Supervisor's Service, so the metrics are scraped from
each pod.

| Metric | Description |
| --- | --- |
| `aviary_turn_queue_length` | Number of queued turns when a turn was accepted, that turn included. 0 for a turn that did not have to wait. |
| `aviary_turns_running` | Running turns that this Supervisor follows, by `harness`. |
| `aviary_turns_ended_total` | Ended turns, by `harness`, `state` and, for a failed turn, `reason`. |
| `aviary_turn_queue_duration_seconds` | Time that a turn waited to run, from its message until it left the queue, by `harness`. |
| `aviary_turn_startup_duration_seconds` | Time that a turn took to start, from leaving the queue until its harness had the message, by `harness`. |
| `aviary_turn_run_duration_seconds` | Time that a turn ran, from its harness having the message until the turn ended, by `harness` and `state`. |
| `aviary_turn_tokens_total` | Token usage that the harnesses reported, by `harness` and `kind`. Not a measurement: see [Turn records](#turn-records). |
| `aviary_approval_wait_seconds` | Time that an approval request waited, from the harness asking until the harness had its answer, by `harness` and `outcome`: the kind of the selected option, or `cancelled` if the turn was stopped first. |
| `aviary_messages_refused_total` | Messages that were not accepted as turns, by `reason`: `queue_full` or `session_busy`. |
| `aviary_secret_read_seconds` | Time that a secret store of a project took to return a value, by the store's `kind` and `outcome`: `read` or `failed`. Cached values are not read and not counted. |

Each Supervisor reports its own work and state, so the metrics of all
Supervisors add up: the sum of `aviary_turns_running` is the number of
running turns. No metric reports the current length of the queue, because
no Supervisor holds the queue: `aviary_turn_queue_length` measures it
whenever a turn is accepted. Every series of the configured harnesses and
secret store kinds starts at zero, so that `rate()` and `increase()` count
its first event. Go runtime and process metrics are served as well.

An Egress Proxy serves the metrics of Envoy on the same port and path, among
them:

| Metric | Description |
| --- | --- |
| `envoy_http_downstream_rq_total`, `envoy_http_downstream_rq_xx` | Requests, and the classes of their response codes. The label `envoy_http_conn_manager_prefix` is `llm` for model requests, which include the Kubernetes probes, and `forward` for all others. |
| `envoy_http_downstream_rq_time` | Request duration in milliseconds. |
| `envoy_http_ext_authz_ok`, `envoy_http_ext_authz_denied`, `envoy_http_ext_authz_error` | What the Supervisor answered when the proxy asked for the turn of a request. |
| `envoy_http_rbac_denied` | Requests to hosts that `egressProxy.allowedHosts` does not allow. Present only if the list names hosts. |

Of Envoy's admin interface, only the metrics can be reached. A sandbox can
read them too, by requesting the proxy's own address through the forward
proxy, if `egressProxy.allowedHosts` allows that address, as the default
does.

The Grafana dashboard in `deploy/grafana/aviary.json` charts these metrics,
and can be imported into any Grafana with a Prometheus data source.

## Tests

```sh
make lint          # formatters, linters and generated code
make test          # each component on its own, without a cluster
make test-harness  # the harness images, in containers with a session's runner
make e2e           # all components together, in a kind cluster of its own
```

None of them needs a model: a mock model server answers from a script. All
but `make e2e` also need Rust, which rustup installs in the version that
`rust-toolchain.toml` names. `make e2e` creates its own cluster and deletes
it when the tests pass, so it does not touch the cluster of
`make cluster-up`. [test/README.md](test/README.md) describes what each of
them checks, how to run some of the tests, and where a new test goes.

## Repository

| Path | Contents |
| --- | --- |
| `supervisor/` | The Supervisor, in Go: the API, the queue, the session sandboxes and their runners, the event streams, and the authorization of Egress Proxy requests. Its plugins are in `store/`, `events/`, `secret/` and `identity/`, and `plugins.go` lists those of the build. |
| `runner/` | The runner, in Rust. |
| `images/` | The images of the Supervisor, the runner and the harnesses. |
| `deploy/charts/aviary/` | The Helm chart. |
| `deploy/grafana/` | A Grafana dashboard of the metrics. |
| `hack/kind/` | The local cluster. |
| `test/` | The tests of the chart, of the Egress Proxy and of all components together, the mock model server, and a Supervisor build with plugins of its own. The tests of the Supervisor and of the runner are next to their code. |

## Limitations

- **No inspection of what an agent sends.** The Egress Proxy logs which
  host a turn connects to, but does not read what is sent over HTTPS.
- **No measured token usage.** The usage of a turn is what its harness
  reports. The Egress Proxy does not count tokens.
- **No copy of the files of an ended session.** Its volume is deleted with
  its sandbox. Aviary keeps no copy, and only a storage class that keeps the
  data of deleted volumes does.
- **Tested only on a local cluster.** Aviary has not run on a cloud
  provider's Kubernetes or under load.

## License

[Apache License 2.0](LICENSE)
