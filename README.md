# Aviary

Aviary runs AI agent harnesses, such as OpenCode and Codex, on Kubernetes.
Every message that a user sends is a turn, and every turn runs in a pod of
its own that is gone when the turn ends. What the session has to keep, its
conversation and its files, lives on a volume, so the pod of the next turn
continues where the last one stopped.

Aviary is meant as a reference for organizations that want to run agents for
many users on their own cluster: the agent has no long-lived process to
attack or to leak into, a session costs nothing while nobody talks to it,
and the number of turns that run at once is a setting.

> **Status.** Aviary is at an early stage. A message sent to the API runs as
> a turn, sessions continue across turns, turns beyond a limit wait their
> turn, and what was said can be read back. The pod of a turn reaches the
> outside only through a proxy that keeps a record. Every agent names its
> model, at an endpoint that its project registers with a key that no pod
> holds. An agent can be made to ask a person before it acts. Supervisors
> can be replaced while turns run.
> It has only run on a local cluster, and a caller whom the API takes may do
> everything with it. See
> [What is missing](#what-is-missing).

## How it works

```
            messages, events                     one Job per turn
  client ───────────────────▶ Supervisor ─────────────────────────▶ turn pod
                               │      ▲                             ┌─────────────────┐
                    database   │      │ gRPC: the message down,     │ runner          │
                    event log  │      └─ what the agent does up ────│   │ ACP         │
                                                                    │ harness         │
                                                                    │   /home ── session volume
                                                                    └────────┬────────┘
                                       model requests, with the turn's token │
              model endpoint ◀─────────────── Egress Proxy ◀─────────────────┘
                              with the API key
```

1. A client creates a project, in it a model provider and an agent that
   uses a model of that provider, and a session of the agent, and sends the
   session a message.
2. The **Supervisor** records the message as a turn. If fewer turns are
   running than the limit allows, it creates a Job for the turn. Otherwise
   the turn waits, and turns run in the order in which they were sent.
3. In the pod, the **runner** connects to the Supervisor, takes the message
   and starts the **harness**. It talks to the harness with the
   [Agent Client Protocol](https://agentclientprotocol.com), so it does not
   know which harness it is.
4. The harness works on the message with the session's home mounted at
   `/home`. The runner reports what it does, and the client reads that as a
   stream of events. A runner that loses its Supervisor goes on with
   another. The harness reaches the model through the **Egress
   Proxy**. The pod holds no key of the model endpoint, only the token of
   its turn: the proxy asks the Supervisor which turn the token belongs to,
   and the Supervisor answers with the endpoint of the agent's provider and
   its key. The proxy puts the key in the token's place, passes the request
   on to that endpoint and records it. Whatever else the
   agent fetches goes through the same proxy, which lets a request through
   to the hosts that the installation allows and records it with its turn.
   A network policy keeps the pod from connecting to anything but the
   Supervisor and the proxy, and it has no DNS.
5. When Kubernetes reports that the pod has stopped, the Supervisor ends the
   turn, keeps what the agent said and did in it, and deletes the Job. Only
   then does the session take its next message, so two pods never work on
   the same session home.

The Supervisor keeps its records in a database and the events of running
turns in an event log, and reaches each through a plugin. It has plugins for
PostgreSQL, for MySQL and for Valkey and only needs their addresses, so both
can be managed services. [Plugins](PLUGINS.md) lists the products and
versions that it is tested with, and tells how to connect it to another
product. The Egress Proxy is [Envoy](https://www.envoyproxy.io), which the
chart installs. A model provider is a URL and a key, so it can be the API
of a provider, an AI gateway or a model server of your own.

The record of the Egress Proxy is a line of JSON for every request, on its
standard output. A line names the turn and the session, the agent and the
project that the turn belongs to, so it tells by itself where a request came
from. The line of a model request also names the provider that it went to.
It also has the time, the host, the path if the request was not for an
HTTPS URL, the status of the answer, the bytes in both directions and how
long the request took. How long the lines are kept is up to what collects
the logs of the cluster.

## Try it locally

You need Docker, [kind](https://kind.sigs.k8s.io), kubectl, Helm, jq, make,
curl and bash. The commands were tested with kind 0.33, Helm 4.3 and
Docker 29 on Linux.

```sh
make cluster-up   # a kind cluster with an NFS volume, PostgreSQL and Valkey
make images       # builds the images and loads them into the cluster
make deploy       # installs Aviary with its Helm chart
```

`make cluster-down` deletes the cluster. `make cluster-up DATABASE=mysql`
makes it with MySQL in the place of PostgreSQL.

### Sending a message yourself

The API listens inside the cluster. Forward a port to it, tell the project
where its model is, and send a message. A model provider is the base URL of
a model endpoint and its key. The endpoint must be reachable from the
cluster, so `localhost` will not do, and an endpoint at a private address
needs an entry in `egressProxy.extraEgress`: see
[Configuration](#configuration). OpenCode needs a server with the Chat
Completions API of OpenAI, Codex one with the Responses API.

```sh
kubectl --context kind-aviary --namespace aviary-system \
  port-forward service/aviary-supervisor 8080:80 &

project=$(curl -s -X POST localhost:8080/v1/projects -d '{"name": "demo"}' | jq -r .id)
provider=$(curl -s -X POST localhost:8080/v1/projects/$project/providers \
  -d '{"name": "a model server", "url": "http://model-server.example.com/v1", "api_key": {"value": "..."}}' | jq -r .id)
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
[Callers](#callers). An installation without one asks nobody.

| Request | |
| --- | --- |
| `POST /v1/projects` | Creates a project. Body: `{"name": "..."}`. |
| `POST /v1/projects/{id}/providers` | Creates a model provider of the project. Body: `{"name": "...", "url": "...", "api_key": {"value": "..."}}`. The URL is the base URL of a model endpoint, and the key is sent to it as a bearer token. A key that is in a secret store of the project is given as where it is: `"api_key": {"store": "...", "path": "...", "key": "..."}`. An endpoint that takes no key has no `api_key`. See [Model providers](#model-providers). |
| `PUT /v1/providers/{id}` | Replaces the name, the URL and the key of a provider, all three at once: a provider that is given no key has none from then on. |
| `POST /v1/projects/{id}/secret-stores` | Registers a secret store of the project. Body: `{"name": "...", "kind": "...", "settings": {...}, "credential": {"value": "..."}}`. See [Secret stores](#secret-stores). |
| `PUT /v1/secret-stores/{id}` | Replaces the name, the kind, the settings and the credential of a secret store, all at once. |
| `POST /v1/projects/{id}/agents` | Creates an agent. Body: `{"harness": "...", "model": {"provider": "...", "name": "..."}, "instructions": "...", "resources": {...}, "approval": "ask"}`. The harness is one of the configured harnesses. The model is a provider of the project, by its ID, and the name of a model as that provider knows it. The instructions are what the agent is told before every turn, and may be left out. The resources are what the pod of each turn asks for and may use at most, as they are written for Kubernetes: `{"requests": {"cpu": "250m", "memory": "1Gi"}, "limits": {"memory": "4Gi"}}`. Given, they are taken as they are. Left out or empty, the agent has none of its own, and each of its turns gets the default of the harness as it then is. With `"approval": "ask"`, the agent asks a person before it acts: see [Approval](#approval). Left out, it asks nobody. |
| `POST /v1/agents/{id}/sessions` | Creates a session. The session is that of the caller: it has the ID of that user as `created_by`, if the installation asks its callers who they are. |
| `POST /v1/sessions/{id}/messages` | Sends a message. Body: `{"text": "..."}`. Answers 202 with the turn, 409 if a turn of the session has not ended, or 429 with `Retry-After` if too many turns wait to run. |
| `POST /v1/turns/{id}/cancel` | Cancels a turn. A turn that waits to run ends at once: 200 with the turn. A turn that runs is told to stop and ends when its pod has stopped: 202 with the turn. Answers 409 if the turn has ended in another way. |
| `POST /v1/turns/{id}/approvals/{request}` | Answers a request for approval of the turn. Body: `{"option_id": "..."}`, the `id` of one of the options of the request. Answers 202 when the answer is on its way to the harness, 400 if the request has no such option, and 409 if the request has its answer or the turn has ended. |
| `GET /v1/turns/{id}/events` | The events of the turn, as server-sent events. A client that reconnects sends `Last-Event-ID` and gets the events after it. |
| `GET /v1/sessions/{id}/turns` | The turns of the session, newest first: `{"turns": [...]}`. `?limit=` is how many, 20 if it is not given and at most 100. `?before=` is the `seq` of a turn, and the answer then holds the turns before that one: that is how to read on from the last turn of an answer. The first turn of a session has `seq` 1. |
| `GET /v1/projects/{id}`, `/v1/providers/{id}`, `/v1/secret-stores/{id}`, `/v1/agents/{id}`, `/v1/sessions/{id}`, `/v1/turns/{id}` | Reads what was created. |
| `GET /v1/me` | Who sends the request, as the identity provider of the installation tells it. See [Callers](#callers). |

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
| `approval.resolved` | The harness has its answer to the request `id`. `outcome`: `selected`, with the `option_id` that it was given, or `cancelled` if the turn was stopped before an answer came. |
| `turn.completed` | `stop_reason`: why the harness ended the turn. `usage`: the token usage that the harness reported, if it did. The last event of the turn. |
| `turn.failed` | The turn did not complete. `reason`: one of the words below. `message`: what is known of the failure. The last event of the turn. |
| `turn.cancelled` | The turn was cancelled. The last event of the turn. |
| `turn.expired` | The turn waited to run for longer than it may. The last event of the turn. |

The fields of the events that come from the agent are defined in
[`proto/aviary/runner/v1/runner.proto`](proto/aviary/runner/v1/runner.proto).

The reasons for a failed turn:

| Reason | |
| --- | --- |
| `harness_error` | The harness could not be started or answered with an error. That is also how a harness reports that the model endpoint refused it. |
| `resume_failed` | The harness could not continue the session: what it had kept of the conversation is gone or cannot be read. The next turn of the session tries again, so a session whose harness cannot recover fails every turn. |
| `startup_timed_out` | The pod of the turn did not start in time, for example because an image could not be pulled. |
| `timed_out` | The turn ran for longer than a turn may. |
| `out_of_memory` | The pod used more memory than it may. |
| `pod_lost` | The pod was stopped from outside, or is gone. |
| `supervisor_lost` | The runner lost its Supervisor and reached none again in time. |
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

### Model providers

The turns of an agent ask the model at the endpoint of the provider that the
agent names, for the model that the agent names.

- **The key goes in and does not come out.** It is stored sealed with a key
  of the installation, which the database does not hold. A provider is read
  with `"api_key": {"stored": true}` in place of its key.
- **Or the key stays in a secret store of the project.** Aviary then stores
  where the key is and not the key, and reads it from the store when a model
  request comes: see [Secret stores](#secret-stores).
- **The key goes to its endpoint and nowhere else.** The Egress Proxy puts
  it into the model requests of the turns that use the provider, which it
  passes on to the host of the provider's URL, below the path of the URL.
  What else a pod sends out carries no key. The pod cannot say where its
  model requests go.
- **A provider can be replaced while turns use it.** Their next model
  request goes to the endpoint as it is then, with the key as it is then.
- **The host of a provider is a host like any other.** If the installation
  lists the hosts that the Egress Proxy lets requests through to, the list
  has to allow it.

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
- **A request whose key cannot be read is refused.** The model request ends
  with 503 and the reason, and the turns of other providers go on.
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
`approval.resolved`. While a request waits, the turn has it in `approvals`
when it is read, on its own or in the turns of its session, so a client that
comes later finds what there is to answer.

- **What is asked is up to the harness**, and to how its image sets it up.
  Aviary passes the requests and the answers on as they are. Of the images
  here, OpenCode asks before it runs a command or changes a file, and Codex
  before a command runs outside its sandbox:
  [`images/README.md`](images/README.md) tells more.
- **A request waits for as long as its turn runs.** It has no time limit of
  its own. A turn that waits for a person is a running turn: it keeps its
  pod, and it is stopped when it has run for `runtime.maxTurnSeconds`. The
  request then ends as `cancelled` and the turn fails as `timed_out`.
  Cancelling the turn ends its requests in the same way.
- **What an option does is up to the harness too.** How long an
  `allow_always` holds differs: in OpenCode until the turn ends, in Codex
  for the rest of the session. After a refusal both end the turn, Codex with
  the stop reason `cancelled`.
- **It is not what keeps an agent in.** The harness that asks runs in the
  pod, beside the tools of the agent and as the same user. Approval keeps an
  agent that means well from acting on a mistake. What a pod can reach is
  bounded by the network policy and the Egress Proxy, whatever was approved.

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
| `cancelled` | none | The turn was cancelled before its harness was given the message, if it has no `started_at`. Otherwise its harness did not stop in time and its pod was stopped. |
| `failed` | none or `cancelled` | The turn failed, for the reason in `failure`. A harness whose pod is about to be stopped, as for `timed_out` and `pod_lost`, is asked to end the turn first. `cancelled` says that it did. |
| `expired` | none | The turn waited to run for longer than it may. |

The items are what the user saw. What the harness remembers of a turn that
did not complete is up to the harness, and it can be less: a harness whose
pod was stopped did not get to write down what it was saying.

`usage` holds `total_tokens`, `input_tokens` and `output_tokens`, and
`thought_tokens`, `cached_read_tokens` and `cached_write_tokens` if the
harness gives them. A turn that has ended has it too. It is what the harness
says, not a measurement, and harnesses count differently: Codex reports the
sum of the model requests of a turn, OpenCode only the last one. What the
Egress Proxy logs can be relied on: every model request, with its turn.

## Metrics

A Supervisor serves metrics for Prometheus on port 9092, at `/metrics`. The
port is not part of its service: the metrics are read from each pod.

| Metric | |
| --- | --- |
| `aviary_turn_queue_length` | How many turns waited to run when a turn was accepted, that turn among them. It is zero for a turn that did not have to wait. |
| `aviary_turns_running` | The turns that run and whose runners are connected to this Supervisor, by `harness`. |
| `aviary_turns_ended_total` | The turns that have ended, by `harness`, `state` and, for a failed turn, `reason`. |
| `aviary_turn_queue_duration_seconds` | How long a turn waited to run, from its message to its pod being asked for, by `harness`. |
| `aviary_turn_startup_duration_seconds` | How long a turn took to start, from its pod being asked for to its harness having the message, by `harness`. |
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

An Egress Proxy serves the metrics of Envoy on the same port and path. Among
them:

| Metric | |
| --- | --- |
| `envoy_http_downstream_rq_total`, `envoy_http_downstream_rq_xx` | The requests and the classes of their response codes. The label `envoy_http_conn_manager_prefix` is `llm` for the requests to the model endpoint, where the probes of Kubernetes count as well, and `forward` for all others. |
| `envoy_http_downstream_rq_time` | How long the requests took, in milliseconds. |
| `envoy_http_ext_authz_ok`, `envoy_http_ext_authz_denied`, `envoy_http_ext_authz_error` | What the Supervisor answered when it was asked for the turn of a request. |
| `envoy_http_rbac_denied` | The requests to hosts that `egressProxy.allowedHosts` does not allow. It is there only if the list names hosts. |

Of the interface that Envoy has for its operator, the metrics are all that
can be reached. A turn pod can read them too, if it asks the forward proxy
for the proxy's own address and `egressProxy.allowedHosts` allows that
address, which the default does.

## Configuration

Aviary is installed with the Helm chart in `deploy/charts/aviary`. The chart
does not install a database or an event log. `make deploy VALUES=<file>`
installs it in the local cluster with a file of values of your own.

| Value | |
| --- | --- |
| `supervisor.replicas`, `egressProxy.replicas` | How many Supervisors and Egress Proxies run. Each of them serves any request. Default 1. |
| `supervisor.resources`, `egressProxy.resources` | What the pod of a Supervisor and of an Egress Proxy asks for and may use at most. |
| `supervisor.database.secretName`, `.secretKey` | A secret with the URL of the database. Its scheme names the kind of the database: `postgresql://…` is PostgreSQL, and `mysql://…` is MySQL. |
| `supervisor.events.secretName`, `.secretKey` | A secret with the URL of the event log. Its scheme names the kind of the log: `valkey://…` is Valkey, and `valkeys://…` is Valkey with TLS. |
| `supervisor.secretStores.allowedHosts` | The hosts at which the projects may have secret stores: `*` for every host, a host name for the host on the ports of HTTPS and HTTP, or a host name with a port. The default allows none. |
| `supervisor.secretStores.cacheSeconds` | For how long a Supervisor uses a value that it read from a secret store before it reads the value again. Default 60, and with 0 the store is read for every model request. |
| `supervisor.auth.provider` | The identity provider whose tokens tell who sends a request to the API: its `kind`, and the `settings` of a provider of that kind. The default is none, and the API then asks nobody. See [Callers](#callers). |
| `supervisor.auth.admins`, `.users` | The groups of the identity provider whose members are the administrators of the installation, and the groups whose members may use the API. The default of both is none: nobody is an administrator, and everyone whom the provider knows may use the API. |
| `supervisor.encryption.secretName`, `.key` | A secret with the keys that the Supervisor seals secrets with before it stores them, and the entry of it that seals. An entry is 32 random bytes in base64. To replace the key, add an entry and name it, and keep the entries from before. |
| `egressProxy.allowedHosts` | The hosts that the Egress Proxy lets requests through to, those of the model providers among them. The default, `*`, is every host. |
| `egressProxy.insideCIDRs`, `.extraEgress` | A network policy keeps the Egress Proxy itself out of these address ranges, so that it is no way into the cluster. `extraEgress` names what it may still reach there, as egress rules of a network policy: the endpoint of a model provider that is a service in the cluster or at a private address needs one. |
| `runtime.maxConcurrentTurns` | The most turns that run at once. Default 50. |
| `runtime.maxQueuedTurns` | The most turns that may wait to run. A message beyond it is answered with 429. Default 500, and 0 is no limit. |
| `runtime.maxQueueWaitSeconds` | How long a turn may wait to run before it ends as expired. Default 120, and 0 is no limit. |
| `runtime.maxStartupSeconds` | How long the pod of a turn may take to start before the turn fails. Default 90, and 0 is no limit. |
| `runtime.maxTurnSeconds` | How long a turn may run before it is stopped and fails. Default 1800. |
| `runtime.maxReconnectSeconds` | How long the runner of a turn goes on without a Supervisor before the turn fails. Default 60, and 0 is no limit. |
| `runtime.maxToolOutputBytes` | The longest text of a tool call that the events carry as it is. A longer text is cut. Default 65536, and 0 is no limit. |
| `runtime.harnesses` | The harnesses that agents can use, each with its image and with the resources for the turns of the agents that have none of their own. |
| `runtime.store.storageClassName`, `.size` | The volume for the session homes. Its storage class must offer ReadWriteMany. |

The turn pods mount the runner as an image volume, so the cluster's
Kubernetes and container runtime must support image volumes, and its
network plugin must enforce network policies: they are what cuts the turn
pods off. The namespace of the turn pods enforces the restricted level of
the Pod Security Standards. The local cluster runs Kubernetes 1.37.

An agent that was created with resources keeps them, and the pods of its
turns get exactly those. An agent without follows the default of its
harness: when `runtime.harnesses` changes, its next turn gets the new
resources. Aviary sets no bound on what an agent may be given.
A LimitRange or a ResourceQuota on the namespace of the turn pods,
`aviary-runtime`, does that: Kubernetes then refuses the pod of a turn that
asks for more, and the turn fails as `startup_timed_out`.

Neither a Supervisor nor an Egress Proxy that is to stop, as when its
deployment is scaled in or rolled out, waits for the turns that use it. The
runners of the Supervisor go on with another, and the requests that the
proxy was passing on break.

To run a harness that is not in this repository, see
[Harness images](images/README.md). To connect Aviary to a database, an
event log or a secret store that it has no plugin for, see
[Plugins](PLUGINS.md).

## Tests

```sh
make lint          # the formatters, the linters and the generated code
make test          # each part by itself, without a cluster
make test-harness  # the harness images, in containers set up as the pods of turns
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
| `supervisor/` | The Supervisor, in Go: the API, the queue, the Jobs, the connection to the runners, the event streams. Its plugins are in `store/`, `events/` and `secret/`, and `plugins.go` lists those of the build. |
| `runner/` | The runner, in Rust. |
| `proto/` | The protocol between the runner and the Supervisor. |
| `images/` | The images of the Supervisor, the runner and the harnesses. |
| `deploy/charts/aviary/` | The Helm chart. |
| `hack/kind/` | The local cluster. |
| `test/` | The tests of the chart, of the Egress Proxy and of the parts together, the mock model server, and a build of the Supervisor with plugins of its own. The tests of the Supervisor and of the runner are with their code. |

## What is missing

- **Permissions.** A caller whom the API takes may do everything with it.
  Without an identity provider that is anyone who reaches the API.
- **Looking into what an agent sends.** The Egress Proxy records which host
  a turn connects to. It does not read what is sent over HTTPS.
- **Measured token usage.** The usage of a turn is what its harness reports.
  The Egress Proxy does not count tokens.
- **Approval that outlasts the turn.** A request for approval waits in a
  running pod, for as long as a turn may run. It cannot be left open for
  days and answered then.
- **Anything but a local cluster.** Aviary has not run on a cloud provider's
  Kubernetes or under load.

## License

[Apache License 2.0](LICENSE)
