# Aviary

Aviary runs AI agent harnesses, such as OpenCode and Codex, on Kubernetes.
Every message that a user sends is a turn, and every session runs its turns
in a sandbox of its own, which keeps the harness and the files of the
session from one turn to the next. A session that is not at work is put to
sleep, sandbox and all, and goes on from where it was when it is woken. The
sandboxes are those of [OpenSandbox](https://github.com/alibaba/OpenSandbox).

Aviary is meant as a reference for organizations that want to run agents for
many users on their own cluster: an agent reaches only the hosts that the
installation allows and never holds the key of its model, every request that
leaves a sandbox is recorded with the turn and the user that it came from, a
session costs no CPU or memory while nobody talks to it, and the number of
turns that run at once is a setting.

> **Status.** Aviary is at an early stage. A message sent to the API runs as
> a turn in the sandbox of its session, sessions continue across turns,
> turns beyond a limit wait their turn, and what was said can be read back.
> A sandbox reaches only the hosts that are allowed, and its requests are
> recorded. Every agent names its model, at an endpoint that its project
> registers with a key that no sandbox holds. An agent can be made to ask a
> person before it acts, and its session sleeps while it waits. Supervisors
> can be replaced while turns run.
> With an identity provider, it knows its callers and holds each to what
> they may do in a project. It has only run on a local cluster. See
> [What is missing](#what-is-missing).

## How it works

```
            messages, events                  makes, wakes and deletes
  client ───────────────────▶ Supervisor ─────────────────────────▶ OpenSandbox
                               │  ▲   ▲                                  │
                    database   │  │   │ HTTP: the turn down,             ▼
                    event log  │  │   └─ what the agent does up ──── sandbox of the session
                                  │                                ┌─────────────────┐
                                  │                                │ runner          │
                                  │                                │   │ ACP         │
                                  │                                │ harness, /home  │
                                  │                                └────────┬────────┘
                                  │ asks about each request:                │ what it sends out
                                  └─ record it, give the model's key ─── egress ──▶ allowed hosts
```

1. A client creates a project, in it a model provider and an agent that
   uses a model of that provider, and a session of the agent, and sends the
   session a message.
2. The **Supervisor** records the message as a turn. If fewer turns are
   running than the limit allows, a Supervisor takes the turn and follows it
   until it has ended. Otherwise the turn waits, and turns run in the order
   in which they were sent.
3. The Supervisor has OpenSandbox make the **sandbox** of the session from
   the template of the agent's harness, or wake it if it sleeps, and gives
   the turn to the **runner** in it. The runner starts the **harness** for
   the first turn and talks to it with the
   [Agent Client Protocol](https://agentclientprotocol.com), so it does not
   know which harness it is.
4. The harness works on the message in the sandbox, with its home at
   `/home`. The runner tells what it does, the Supervisor records that, and
   the client reads it as a stream of events. If the Supervisor goes,
   another follows the turn on from what was recorded.
5. When the runner tells that the turn has ended, the Supervisor ends the
   turn and keeps what the agent said and did in it. The harness and the
   files stay in the sandbox for the next turn. A session that runs no
   turn, or whose turn waits for a person, is put to sleep after a while,
   and woken for its next message or for the answer.
6. What the sandbox sends out passes its **egress**, which lets it reach the
   hosts that the installation allows and no others. The egress asks the
   Supervisors about the requests: they record each with its turn, and give
   a request to the model endpoint of the agent its key.

The Supervisor keeps its records in a database and the events of running
turns in an event log, and reaches each through a plugin. It has plugins for
PostgreSQL, for MySQL and for Valkey and only needs their addresses, so both
can be managed services. [Plugins](PLUGINS.md) lists the products and
versions that it is tested with, and tells how to connect it to another
product. A model provider is a URL and a key, so it can be the API of a
provider, an AI gateway or a model server of your own.

A sandbox may send requests to the hosts of `sandboxes.allowedHosts` and to
no others, and the installation of OpenSandbox is to keep the sandboxes out
of the cluster. Every request over HTTPS to a host that is named there, or
to the model endpoint of the agent, is recorded by the Supervisors, and only
a request to the model endpoint gets its key. A request of a sandbox in
which no turn runs is refused.

The record of a request is a line of JSON on the standard output of a
Supervisor, with the `msg` `request`, or `request refused` and the reason
as `why`. It names the `sandbox`, the `turn`, and the `session`, the `agent`
and the `project` of the turn, so it tells by itself where a request came
from. In an installation with an identity provider it also names who sent
the message of the turn: the `user`, by the ID that `GET /v1/me` answers
that user, and the `subject` that the identity provider knows the user by.
A request to the model endpoint names its `provider`. Then come the
`method`, the `scheme`, the `host`, the `port` and the `path` of the
request, without its query. How long the lines are kept is up to what
collects the logs of the cluster.

## Try it locally

You need Docker, [kind](https://kind.sigs.k8s.io), kubectl, Helm, jq, make,
curl and bash, and a host with KVM (`/dev/kvm`), which the sandboxes of the
cluster run on. The commands were tested with kind 0.33, Helm 4.3 and
Docker 29 on Linux.

```sh
make cluster-up   # a kind cluster with PostgreSQL, Valkey, an object store
                  # and OpenSandbox
make images       # builds the images and loads them into the cluster
make deploy       # installs Aviary with its Helm chart
```

`make cluster-down` deletes the cluster. `make cluster-up DATABASE=mysql`
makes it with MySQL in the place of PostgreSQL.

### Sending a message yourself

The API listens inside the cluster. Forward a port to it, tell the project
where its model is, and send a message. A model provider is the base URL of
a model endpoint and its key. The endpoint must be reachable from the
cluster, so `localhost` will not do, and a request to it gets the key only
over HTTPS on port 443. OpenCode needs a server with the Chat Completions
API of OpenAI, Codex one with the Responses API.

```sh
kubectl --context kind-aviary --namespace aviary-system \
  port-forward service/aviary-supervisor 8080:80 &

project=$(curl -s -X POST localhost:8080/v1/projects -d '{"name": "demo"}' | jq -r .id)
provider=$(curl -s -X POST localhost:8080/v1/projects/$project/providers \
  -d '{"name": "a model server", "url": "https://model-server.example.com/v1", "api_key": {"value": "..."}}' | jq -r .id)
agent=$(curl -s -X POST localhost:8080/v1/projects/$project/agents \
  -d '{"harness": "opencode", "model": {"provider": "'$provider'", "name": "..."}}' | jq -r .id)
session=$(curl -s -X POST localhost:8080/v1/agents/$agent/sessions | jq -r .id)

turn=$(curl -s -X POST localhost:8080/v1/sessions/$session/messages \
  -d '{"text": "Create a file named hello.txt that says hello."}' | jq -r .id)
curl -N localhost:8080/v1/turns/$turn/events
```

The last command prints the events of the turn as they happen and returns
when the turn has ended. Send the session another message, and the agent
still knows what was said and finds the file.

## API

An installation with an identity provider takes a request only with a token
of the provider, in the header `Authorization: Bearer <token>`: see
[Callers](#callers). A caller may do in a project what its members that are
the caller, or one of the groups of the caller, may do: see [Roles](#roles).
A project of which the caller is no member is answered as one that there is
not, with 404, and a request that takes a permission that the caller lacks
with 403. An administrator may do everything. An installation without an
identity provider asks nobody, and every caller is an administrator.

| Request | |
| --- | --- |
| `POST /v1/projects` | Creates a project. Body: `{"name": "..."}`. |
| `POST /v1/projects/{id}/providers` | Creates a model provider of the project. Body: `{"name": "...", "url": "...", "api_key": {"value": "..."}}`. The URL is the base URL of a model endpoint, and the key is sent to it as a bearer token. A key that is in a secret store of the project is given as where it is: `"api_key": {"store": "...", "path": "...", "key": "..."}`. An endpoint that takes no key has no `api_key`. See [Model providers](#model-providers). |
| `PUT /v1/providers/{id}` | Replaces the name, the URL and the key of a provider, all three at once: a provider that is given no key has none from then on. |
| `POST /v1/projects/{id}/secret-stores` | Registers a secret store of the project. Body: `{"name": "...", "kind": "...", "settings": {...}, "credential": {"value": "..."}}`. See [Secret stores](#secret-stores). |
| `PUT /v1/secret-stores/{id}` | Replaces the name, the kind, the settings and the credential of a secret store, all at once. |
| `POST /v1/projects/{id}/agents` | Creates an agent. Body: `{"harness": "...", "model": {"provider": "...", "name": "..."}, "instructions": "...", "approval": "ask"}`. The harness is one of the configured harnesses. The model is a provider of the project, by its ID, and the name of a model as that provider knows it. The instructions are what the agent is told before every turn, and may be left out. With `"approval": "ask"`, the agent asks a person before it acts: see [Approval](#approval). Left out, it asks nobody. |
| `POST /v1/agents/{id}/sessions` | Creates a session. The session is that of the caller: it has the ID of that user as `created_by`, if the installation asks its callers who they are. |
| `POST /v1/sessions/{id}/messages` | Sends a message. Body: `{"text": "..."}`. The turn has the ID of the caller as `created_by`, as a session has. Answers 202 with the turn, 409 if a turn of the session has not ended or the session has, or 429 with `Retry-After` if too many turns wait to run. |
| `DELETE /v1/sessions/{id}` | Ends a session: 200 with the session, which then has `ended_at`. Its sandbox is deleted, with all that the agent had in it. What was said in the session stays to be read, and the session takes no more messages. Answers 409 if a turn of the session has not ended. |
| `POST /v1/turns/{id}/cancel` | Cancels a turn. A turn that waits to run ends at once: 200 with the turn. A turn that runs is told to stop and ends when its harness has stopped: 202 with the turn. Answers 409 if the turn has ended in another way. |
| `POST /v1/turns/{id}/approvals/{request}` | Answers a request for approval of the turn. Body: `{"option_id": "..."}`, the `id` of one of the options of the request. Answers 202 when the answer is on its way to the harness, 400 if the request has no such option, and 409 if the request has its answer or the turn has ended. |
| `GET /v1/turns/{id}/events` | The events of the turn, as server-sent events. A client that reconnects sends `Last-Event-ID` and gets the events after it. |
| `GET /v1/sessions/{id}/turns` | The turns of the session, newest first: `{"turns": [...]}`. `?limit=` is how many, 20 if it is not given and at most 100. `?before=` is the `seq` of a turn, and the answer then holds the turns before that one: that is how to read on from the last turn of an answer. The first turn of a session has `seq` 1. |
| `GET /v1/projects/{id}`, `/v1/providers/{id}`, `/v1/secret-stores/{id}`, `/v1/agents/{id}`, `/v1/sessions/{id}`, `/v1/turns/{id}` | Reads what was created. |
| `GET /v1/me` | Who sends the request, as the identity provider of the installation tells it. See [Callers](#callers). |
| `GET /v1/projects` | The projects of the caller, oldest first: `{"projects": [...]}`. They are the projects of which the caller, or one of the groups of the caller, is a member, and for an administrator all of them. |
| `GET /v1/projects/{id}/agents` | The agents of the project, in the order in which they were created: `{"agents": [...]}`. |
| `GET /v1/agents/{id}/sessions` | The sessions of the agent that the caller may read, newest first: `{"sessions": [...]}`. They are the caller's own with `sessions.run`, and those of everyone with `sessions.view_all`. `?limit=` is how many, 20 if it is not given and at most 100. `?before=` is the ID of a session, and the answer then holds the sessions before that one: that is how to read on from the last session of an answer. |
| `GET /v1/projects/{id}/members` | The members of the project, in the order in which they were added: `{"members": [...]}`. See [Roles](#roles). |
| `POST /v1/projects/{id}/members` | Makes a member of the project of a user or of a group. Body: `{"user": "..."}`, the username of the user, or `{"group": "..."}`, the name of a group of the identity provider, and with it `{"role": "..."}` or `{"permissions": [...]}`. Only an administrator may. Answers 409 if the project has that member already. |
| `PUT /v1/members/{id}` | Gives a member another role, or other permissions of its own. Body: `{"role": "..."}` or `{"permissions": [...]}`. Only an administrator may. |
| `DELETE /v1/members/{id}` | Makes the member no member of its project: 204. Only an administrator may. |
| `GET /v1/roles` | The roles, and what each of them may do: `{"roles": [{"name": "editor", "permissions": [...]}, ...]}`. See [Roles](#roles). |
| `PUT /v1/roles/{name}` | Gives a role its permissions. Body: `{"permissions": [...]}`. Only an administrator may: another caller is answered with 403. Answers 400 for a name that is no permission, and 404 for a role that there is not. |

The events of a turn:

| Event | Data |
| --- | --- |
| `turn.queued` | `position`: the turn's place in the queue, 1 for the turn that runs next. |
| `turn.started` | The harness has been given the message. |
| `message.delta`, `thought.delta` | `text`: the next piece of what the agent says or thinks. `message_id`: pieces with the same ID belong together. |
| `tool.call` | `id`, `title`, `kind`, `status`, `input`: the agent started to use a tool. |
| `tool.update` | `id` and what changed of `title`, `kind`, `status`, `input`, `output`. A text in `input` or `output` that is longer than the installation allows is cut, and says so at its end. |
| `plan.update` | `entries`: the agent's plan, each entry with `content`, `priority` and `status`. |
| `approval.requested` | The harness asks a person before it acts, and waits. `id`: the request. `tool_call`: what it is about to do, with the fields of `tool.call`. `options`: what a person can answer, each with `id`, `name` and `kind`, which is `allow_once`, `allow_always`, `reject_once` or `reject_always`. |
| `approval.resolved` | The harness has its answer to the request `id`. `outcome`: `selected`, with the `option_id` that it was given, or `cancelled` if the turn was stopped before an answer came. `answered_by`: the ID of the user whose answer it was, if the installation asks its callers who they are. |
| `turn.completed` | `stop_reason`: why the harness ended the turn. `usage`: the token usage that the harness reported, if it did. The last event of the turn. |
| `turn.failed` | The turn did not complete. `reason`: one of the words below. `message`: what is known of the failure. The last event of the turn. |
| `turn.cancelled` | The turn was cancelled. The last event of the turn. |
| `turn.expired` | The turn waited to run for longer than it may. The last event of the turn. |

The events that come from the agent are as the runner tells them:
[`runner/src/main.rs`](runner/src/main.rs).

The reasons for a failed turn:

| Reason | |
| --- | --- |
| `harness_error` | The harness could not be started or answered with an error. That is also how a harness reports that the model endpoint refused it. |
| `resume_failed` | The harness could not continue the session: what it had kept of the conversation is gone or cannot be read. The next turn of the session tries again, so a session whose harness cannot recover fails every turn. |
| `startup_timed_out` | The harness did not get the message in time, for example because the sandbox of the session could not be made. The message says what the turn waited for. A sandbox that was still being made or woken stays, with what the agent had in it, and the next turn of the session goes to it. |
| `timed_out` | The turn ran for longer than a turn may. |
| `sandbox_lost` | The sandbox of the session is gone, and what the agent had in it. The next turn of the session gets a new one. |
| `session_expired` | The session was not used for longer than `sandboxes.retentionSeconds`. Its sandbox is gone, and the session has ended. |
| `runner_error` | The runner failed in another way. |

A turn that has ended has its reason too, as `failure`. What makes a turn
fail is up to the harness: when the model endpoint answers with an error,
OpenCode fails the turn, and Codex mostly ends it with the error as what the
agent says.

### Callers

Aviary logs nobody in and keeps no passwords. An installation names an
identity provider, `supervisor.auth.provider`, and a caller gets a token
from that provider and sends it with every request. A request without a
token that the provider issued for this installation is answered with 401.
A caller is a user of the installation from the first request on.
`GET /v1/me` answers the ID of that user, what the installation read from
the token of the request, and whether it takes the caller for an
administrator: `{"id": "...", "subject": "...", "username": "...",
"display_name": "...", "groups": [...], "admin": false}`.

The groups that the provider gives a caller say what the caller is to the
installation. Aviary sets no names for them: an installation names the
groups of its provider. The members of the groups in `supervisor.auth.admins`
are its administrators. The members of the groups in `supervisor.auth.users`
may use the API, as the administrators may, and with no group named there
everyone whom the provider knows may. A caller that may not is answered with
403, and can still ask `GET /v1/me`. In an installation without a provider
every caller is an administrator.

### Roles

A role is what a member of a project may do there: a set of permissions
under a name.

An administrator creates the projects, gives them their members and changes
what the roles may do. Everything else takes a permission in the project:

| Permission | |
| --- | --- |
| `project.view` | See the project and what it is made of: its agents, its model providers and its secret stores, without their secrets. Every role has it. |
| `agents.manage` | Create the agents of the project. |
| `connections.manage` | Register and replace the model providers and the secret stores of the project. |
| `sessions.run` | Create sessions, and in one's own send messages, stop the turns, answer their requests for approval, read what was said and end it. |
| `sessions.view_all` | Read the sessions of the other members. |
| `sessions.control_all` | Stop the turns of the other members, answer their requests for approval and end their sessions. A role with it has `sessions.view_all` too. |

An administrator gives a project its members. A member is a user, by the
username, or a group of the identity provider, which is everyone whom the
provider says to be in it. A member has a role, or permissions of its own:
`"role": "custom"` and the permissions. A member with a role may do what the
role may now. One with permissions of its own keeps them as they were given.

A user can be made a member before they have ever sent a request: the user
who is then seen with that username is the member. Who a user is does not
hang on the username after that. If the provider gives the username to
someone else, the member stays the user that it was, and the other is none.

A session is that of the caller who created it, and only that caller sends
messages to it. To read a session of one's own, to stop its turns, to
answer their requests for approval and to end it takes `sessions.run`. With
a session of another member the same takes `sessions.view_all` to read and
`sessions.control_all` to stop, to answer and to end.

An installation has five roles, and no others. An administrator changes
what a role may do, with `PUT /v1/roles/{name}`, and that holds from then on
for everyone who has the role. At the start they are:

| Role | Permissions |
| --- | --- |
| `owner` | All of them. |
| `editor` | `project.view`, `agents.manage`, `connections.manage`, `sessions.run` |
| `user` | `project.view`, `sessions.run` |
| `viewer` | `project.view` |
| `auditor` | `project.view`, `sessions.view_all` |

### Identity providers

A provider of the kind `oidc` is an OpenID Connect issuer. Its settings:

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

- **The issuer** is the URL below which the provider publishes its
  configuration and the keys that it signs with. A Supervisor reads the
  configuration when it starts, and does not start if it cannot.
- **The audience** is whom a token must be meant for. A token is a JWT that
  the issuer signed for that audience and that has not expired, an ID token
  or an access token.
- **The claims** say where a token tells of its caller. The subject is what
  the provider knows the caller by and never changes, `sub` unless the
  settings say otherwise. The username is a name that a person can read and
  that no other caller has, and a caller has one only if the settings name
  its claim. The display name is `name` and the groups are `groups` unless
  the settings say otherwise. A claim inside another is named by its path,
  such as `realm_access.roles`. An email address is a username only if the
  token says `"email_verified": true`.

To try it in the local cluster, `make identity-provider` installs Keycloak
there in its dev mode, with the realm in `hack/kind/keycloak.yaml`. The realm
has four users, each with its name as its password: `alice` is an
administrator, `bob` and `carol` may use the API, `carol` is in the group
`team-a` too, and `dave` is in no group. Aviary asks that provider when it
is installed with `make deploy VALUES=hack/kind/aviary-keycloak-values.yaml`.
A caller gets a token from the provider and sends it:

```sh
kubectl --context kind-aviary --namespace aviary-data \
  port-forward service/keycloak 8081:8080 &

token=$(curl -s localhost:8081/realms/aviary/protocol/openid-connect/token \
  -d grant_type=password -d client_id=aviary -d username=alice -d password=alice | jq -r .access_token)
curl -s -H "Authorization: Bearer $token" localhost:8080/v1/me
```

### Model providers

The turns of an agent ask the model at the endpoint of the provider that the
agent names, for the model that the agent names.

- **The key goes in and does not come out.** It is stored sealed with a key
  of the installation, which the database does not hold. A provider is read
  with `"api_key": {"stored": true}` in place of its key.
- **Or the key stays in a secret store of the project.** Aviary then stores
  where the key is and not the key, and reads it from the store when a model
  request comes: see [Secret stores](#secret-stores).
- **The key goes to its endpoint and nowhere else.** The egress of a
  sandbox puts it into a request of a turn that uses the provider only if
  the request goes over HTTPS to the host and the port of the provider's
  URL, below the path of the URL. What else a sandbox sends out carries no
  key. The egress looks into requests over HTTPS on port 443 only, so an
  endpoint on another port gets none.
- **A provider can be replaced while turns use it.** Their next model
  request gets the key as it is then, if it goes to the endpoint as it is
  then.
- **The host of a provider is a host like any other.** If the installation
  lists the hosts that a sandbox may reach, `sandboxes.allowedHosts`, the
  list has to allow it.

### Secret stores

A project that keeps its secrets in a store of its own registers the store,
and then says of a secret where it is in the store in place of giving it.
The key of a model provider is such a secret.

```sh
store=$(curl -s -X POST localhost:8080/v1/projects/$project/secret-stores \
  -d '{"name": "team store", "kind": "kv-v2", "credential": {"value": "..."},
       "settings": {"address": "https://secrets.example.com:8200", "mount": "secret", "role_id": "..."}}' | jq -r .id)
provider=$(curl -s -X POST localhost:8080/v1/projects/$project/providers \
  -d '{"name": "a model server", "url": "https://api.example.com/v1",
       "api_key": {"store": "'$store'", "path": "team/model", "key": "api_key"}}' | jq -r .id)
```

- **The kind says how the store is asked.** `kv-v2` is a store that serves
  the HTTP API known as KV version 2 and that a program logs in to as an
  AppRole, such as OpenBao. Its settings are its address, the mount of its
  secrets and the ID of the role, and its credential is the secret ID of the
  role. A secret is at a path below the mount and holds values under keys.
  Another kind of store takes a [plugin](PLUGINS.md).
- **The installation says where a store may be.** The Supervisor itself
  connects to a store, so a project can only register one at a host that
  `supervisor.secretStores.allowedHosts` lists. By default it lists none.
- **What does not work is refused at once.** The Supervisor logs in to a
  store before it takes it, and reads a key from its place before it takes
  the provider.
- **A value is read when it is used.** A Supervisor remembers what it read
  for a minute, and uses no value for longer than that. A key that changes
  in the store is the one that the model requests carry within a minute,
  and a key that is taken out of the store is gone from Aviary as soon.
  The installation sets that time with `supervisor.secretStores.cacheSeconds`.
- **What a Supervisor remembers is bounded.** It holds at most 10,000
  values, 16 MiB of them, and the logins to at most 1,000 stores, and
  forgets what it used longest ago. A value of more than 8 KiB is not taken
  for a secret.
- **A request whose key cannot be read is refused.** Its record tells why,
  and the turns of other providers go on.
- **The credential is stored sealed**, as the key of a provider is. A store
  is read with `"credential": {"stored": true}`.
- **Give the role no more than Aviary is to read.** Whoever can create a
  provider in the project can have any value that the role may read sent to
  an endpoint of their choosing.

To try it in the local cluster, `make secret-store` installs OpenBao there
in its dev mode, which keeps what it is given in memory. It is at
`http://openbao.aviary-data:8200`, a host that
`supervisor.secretStores.allowedHosts` then has to list, and its root token
is in the secret `openbao-root` of the namespace `aviary-data`.

### Approval

An agent that was created with `"approval": "ask"` has a person agree to
what it does. Before its harness acts, the turn gets the event
`approval.requested`, and the harness waits. A client answers with
`POST /v1/turns/{id}/approvals/{request}` and one of the options that the
harness gave. The harness goes on with that answer, and the turn gets
`approval.resolved`, which says who answered. While a request waits, the turn has it in `approvals`
when it is read, on its own or in the turns of its session, so a client that
comes later finds what there is to answer.

- **What is asked is up to the harness**, and to how its image sets it up.
  Aviary passes the requests and the answers on as they are. Of the images
  here, OpenCode asks before it runs a command or changes a file, and Codex
  before a command runs outside its sandbox:
  [`images/README.md`](images/README.md) tells more.
- **A request waits for as long as its turn may run.** The time that a turn
  waits for a person is of its `runtime.maxTurnSeconds` too, because its
  sandbox runs while it waits, and the turn takes its place among the turns
  that run. A turn whose time ends while it waits fails as `timed_out`, and
  the next message of the session goes on with what was said. Cancelling the
  turn ends its requests as `cancelled`.
- **What an option does is up to the harness too.** How long an
  `allow_always` holds differs: in OpenCode until the turn ends, in Codex
  for the rest of the session. After a refusal both end the turn, Codex with
  the stop reason `cancelled`.
- **It is not what keeps an agent in.** The harness that asks runs in the
  sandbox, beside the tools of the agent and as the same user. Approval keeps an
  agent that means well from acting on a mistake. What an agent can reach is
  bounded by `sandboxes.allowedHosts`, whatever was approved.

An agent without `approval` asks nobody. If its harness asks for permission
all the same, the runner allows the action once.

### The record of a turn

A turn, read on its own or in the turns of its session, has `items`: what
the agent said and did, in order. They are put together from the events when
the turn ends, so a turn that has not ended has none yet, and a client
follows its events. The events themselves are kept for an hour after the
turn ended.

| Item `type` | |
| --- | --- |
| `message`, `thought` | `text`: the pieces that followed one another, as one text. |
| `tool_call` | The fields of `tool.call`, as the updates of the call left them. |
| `plan` | `entries`: the plan as it last stood. |
| `approval` | The fields of `approval.requested`, and how the request ended: `outcome` and, if an option was selected, `option_id`. |

A turn that did not complete keeps what there was until then: the text that
the agent had written so far, and a tool call with the status that it had.
How the turn ended is told by its `state`, its `failure` and its
`stop_reason`. A turn has a `stop_reason` if its harness ended it by itself.
A turn without one was stopped from outside, or never reached its harness:
a turn has `started_at` once its harness has been given the message.

| `state` | `stop_reason` | |
| --- | --- | --- |
| `completed` | `end_turn`, `max_tokens`, `max_turn_requests` or `refusal` | The harness ended the turn. |
| `completed` | `cancelled` | The harness ended the turn because a person refused what it asked approval for. Codex does. |
| `cancelled` | `cancelled` | The turn was cancelled, and the harness stopped when it was asked to. |
| `cancelled` | none | The turn was cancelled before its harness was given the message, if it has no `started_at`. Otherwise its harness did not stop in time, and its sandbox was deleted. |
| `failed` | none | The turn failed, for the reason in `failure`. |
| `expired` | none | The turn waited to run for longer than it may. |

The items are what the user saw. What the harness remembers of a turn that
did not complete is up to the harness, and it can be less. A turn whose
sandbox was deleted, as one that ran for too long, leaves the agent nothing:
the next turn of the session has a new sandbox.

`usage` holds `total_tokens`, `input_tokens` and `output_tokens`, and
`thought_tokens`, `cached_read_tokens` and `cached_write_tokens` if the
harness gives them. A turn that has ended has it too. It is what the harness
says, not a measurement, and harnesses count differently: Codex reports the
sum of the model requests of a turn, OpenCode only the last one. What the
Supervisors record can be relied on: every model request over HTTPS, with
its turn.

## Metrics

A Supervisor serves metrics for Prometheus on port 9092, at `/metrics`. The
port is not part of its service: the metrics are read from each pod.

| Metric | |
| --- | --- |
| `aviary_turn_queue_length` | How many turns waited to run when a turn was accepted, that turn among them. It is zero for a turn that did not have to wait. |
| `aviary_turns_running` | The turns that run and that this Supervisor follows, by `harness`. |
| `aviary_turns_ended_total` | The turns that have ended, by `harness`, `state` and, for a failed turn, `reason`. |
| `aviary_turn_queue_duration_seconds` | How long a turn waited to run, from its message to its leaving the queue, by `harness`. |
| `aviary_turn_startup_duration_seconds` | How long a turn took to start, from its leaving the queue to its harness having the message, by `harness`. |
| `aviary_turn_run_duration_seconds` | How long a turn ran, from its harness having the message to the turn having ended, by `harness` and `state`. |
| `aviary_turn_tokens_total` | The token usage that the harnesses reported, by `harness` and `kind`. It is not a measurement, as said above. |
| `aviary_approval_wait_seconds` | How long a request for approval waited, from the harness asking to the harness having its answer, by `harness` and `outcome`: the kind of the option that was picked, or `cancelled` for a request whose turn was stopped first. |
| `aviary_messages_refused_total` | The messages that were not accepted as turns, by `reason`: `queue_full` or `session_busy`. |
| `aviary_secret_read_seconds` | How long a secret store of a project took to give a value, by the `kind` of the store and `outcome`: `read` or `failed`. A value that the Supervisor remembers is not read and not counted. |

Of several Supervisors, each reports what it did and what it holds itself,
so the metrics of all of them add up: the sum of `aviary_turns_running` is
the number of turns that run. No metric tells how many turns wait at this
moment, because no Supervisor holds them. The length of the queue is
measured whenever a turn is accepted instead. The metrics of the Go runtime
and of the process are served as well.

## Configuration

Aviary is installed with the Helm chart in `deploy/charts/aviary`. The chart
does not install a database or an event log. `make deploy VALUES=<file>`
installs it in the local cluster with a file of values of your own.

| Value | |
| --- | --- |
| `supervisor.replicas` | How many Supervisors run. Each of them serves any request. Default 1. |
| `supervisor.resources` | What the pod of a Supervisor asks for and may use at most. |
| `supervisor.database.secretName`, `.secretKey` | A secret with the URL of the database. Its scheme names the kind of the database: `postgresql://…` is PostgreSQL, and `mysql://…` is MySQL. |
| `supervisor.events.secretName`, `.secretKey` | A secret with the URL of the event log. Its scheme names the kind of the log: `valkey://…` is Valkey, and `valkeys://…` is Valkey with TLS. |
| `supervisor.secretStores.allowedHosts` | The hosts at which the projects may have secret stores: `*` for every host, a host name for the host on the ports of HTTPS and HTTP, or a host name with a port. The default allows none. |
| `supervisor.secretStores.cacheSeconds` | For how long a Supervisor uses a value that it read from a secret store before it reads the value again. Default 60, and with 0 the store is read for every model request. |
| `supervisor.auth.provider` | The identity provider whose tokens tell who sends a request to the API: its `kind`, and the `settings` of a provider of that kind. The default is none, and the API then asks nobody. See [Callers](#callers). |
| `supervisor.auth.admins`, `.users` | The groups of the identity provider whose members are the administrators of the installation, and the groups whose members may use the API. The default of both is none: nobody is an administrator, and everyone whom the provider knows may use the API. |
| `supervisor.encryption.secretName`, `.key` | A secret with the keys that the Supervisor seals secrets with before it stores them, and the entry of it that seals. An entry is 32 random bytes in base64. To replace the key, add an entry and name it, and keep the entries from before. |
| `sandboxes.url` | Where the lifecycle API of OpenSandbox is, which runs the sandboxes of the sessions. Default `http://opensandbox-server.opensandbox-system`. |
| `sandboxes.apiKey.secretName`, `.secretKey` | A secret with the key of that API. |
| `sandboxes.allowedHosts` | The hosts that a sandbox may send requests to, those of the model endpoints among them: names, `*.domain`s, or `*` for every host, which is the default. Every request over HTTPS to a host that is named, or to the model endpoint of the agent, is recorded with its turn and the user who sent the message, and only a request to the model endpoint gets its key. A model endpoint over HTTP, or on another port than 443, gets no key. The installation of OpenSandbox keeps the sandboxes out of the cluster. A change holds for the sandboxes that are made after it. |
| `supervisor.resolver.secretName` | A secret with the certificate and the key (`tls.crt`, `tls.key`) with which the Supervisors answer the egress of OpenSandbox, for `aviary-supervisor.<namespace>.svc.cluster.local`. The egress has to trust the certificate. Default `aviary-resolver-tls`. |
| `sandboxes.idleSeconds` | After how many seconds a session that is not at work is put to sleep: one that runs no turn, or whose turn waits for a person. It is woken as it was for its next turn and for the answer. Default 300, and 0 is never. OpenSandbox must be set up to put sandboxes to sleep. |
| `sandboxes.retentionSeconds` | For how many seconds the sandbox of a session is kept after a turn of the session was last given to it. A session that is not used for longer has ended: its sandbox is deleted, and what was said in it stays. Default 0, which is until the session is ended. |
| `sandboxes.templateStore` | Where OpenSandbox keeps the templates that the sandboxes are made from, in its object store. Default `s3://sandbox-images/publish`. |
| `runtime.maxConcurrentTurns` | The most turns that run at once. Default 50. |
| `runtime.maxQueuedTurns` | The most turns that may wait to run. A message beyond it is answered with 429. Default 500, and 0 is no limit. |
| `runtime.maxQueueWaitSeconds` | How long a turn may wait to run before it ends as expired. Default 120, and 0 is no limit. |
| `runtime.maxStartupSeconds` | How long a turn may take until its harness has the message, which for the first turn of a session is after its sandbox was made. A turn that takes longer fails. Its sandbox is deleted if the runner in it had the turn, and stays for the next turn if it was still being made or woken. Default 120, and 0 is no limit. |
| `runtime.maxTurnSeconds` | How long a turn may run before it fails and its sandbox is deleted, the time that it waits for a person included. Default 1800. |
| `runtime.maxToolOutputBytes` | The longest text of a tool call that the events carry as it is. A longer text is cut. Default 65536, and 0 is no limit. |
| `runtime.harnesses` | The harnesses that agents can use, each with its image. `sandbox` is the size of the sandbox of a session: its `cpu`, `memory` and `disk`. A release has OpenSandbox build a template for each harness, from the image and that size, when it is installed and when either has changed. OpenSandbox takes the image from its registry without credentials. |

The chart does not install OpenSandbox either. Aviary runs on OpenSandbox
1.2.1 with its Fast Sandbox runtime, whose sandboxes need nodes with KVM,
and `hack/kind` shows what it takes besides: a pool of sandboxes, an object store for the
templates and for the sandboxes that sleep, the idle pause of the server
with the gateway telling it of the activity of the sandboxes, and an egress
that looks into HTTPS. The egress has to trust the certificate of the
Supervisors and to wait for their answer
(`OPENSANDBOX_EGRESS_CREDENTIAL_RESOLVER_TIMEOUT`), which can take as long
as a secret store takes to give a key. The local cluster runs Kubernetes
1.37.

The size of a sandbox is that of its harness in `runtime.harnesses`, and a
change of it holds for the sandboxes that are made after it. Aviary sets no
bound on how many sandboxes there are: the pool of OpenSandbox does.

A Supervisor that is to stop, as when its deployment is scaled in or rolled
out, does not wait for the turns that it follows. Another Supervisor
follows them on, from what was recorded.

To run a harness that is not in this repository, see
[Harness images](images/README.md). To connect Aviary to a database, an
event log or a secret store that it has no plugin for, see
[Plugins](PLUGINS.md).

## Tests

```sh
make lint          # the formatters, the linters and the generated code
make test          # each part by itself, without a cluster
make test-harness  # the harness images, in containers with the runner of a session
make e2e           # the parts together, in a kind cluster of its own
```

None of them needs a model: a mock model server answers by a script. All but
the last also need Rust, which rustup installs in the version that
`rust-toolchain.toml` names. `make e2e` makes its cluster anew and removes it
when the tests have passed, so it does not touch the cluster of
`make cluster-up`. [test/README.md](test/README.md) tells what each of them
checks, how to run some of the tests and where a new one goes.

## Repository

| Path | |
| --- | --- |
| `supervisor/` | The Supervisor, in Go: the API, the queue, the sandboxes of the sessions and the runners in them, the event streams. Its plugins are in `store/`, `events/` and `secret/`, and `plugins.go` lists those of the build. |
| `runner/` | The runner, in Rust. |
| `images/` | The images of the Supervisor, the runner and the harnesses. |
| `deploy/charts/aviary/` | The Helm chart. |
| `hack/kind/` | The local cluster. |
| `test/` | The tests of the chart and of the parts together, the mock model server, and a build of the Supervisor with plugins of its own. The tests of the Supervisor and of the runner are with their code. |

## What is missing

- **Looking into what an agent sends.** The record of a request tells where
  it went, and not what it sent.
- **Measured token usage.** The usage of a turn is what its harness reports.
  Aviary does not count tokens.
- **A model endpoint that moves.** The egress of a sandbox is told which
  hosts to ask about when the first turn of the session runs in it. A
  provider whose URL is given a host that `sandboxes.allowedHosts` does not
  name gets no key from the sandboxes that its sessions have already.
- **Anything but a local cluster.** Aviary has not run on a cloud provider's
  Kubernetes or under load.

## License

[Apache License 2.0](LICENSE)
