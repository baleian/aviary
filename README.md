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
> outside only through a proxy that keeps a record. An agent can be made to
> ask a person before it acts. Supervisors can be replaced while turns run.
> It has only run on a local cluster, and the API has no authentication. See
> [What is missing](#what-is-missing).

## How it works

```
            messages, events                     one Job per turn
  client ───────────────────▶ Supervisor ─────────────────────────▶ turn pod
                               │      ▲                             ┌─────────────────┐
                    PostgreSQL │      │ gRPC: the message down,     │ runner          │
                    Valkey     │      └─ what the agent does up ────│   │ ACP         │
                                                                    │ harness         │
                                                                    │   /home ── session volume
                                                                    └────────┬────────┘
                                       model requests, with the turn's token │
              model endpoint ◀─────────────── Egress Proxy ◀─────────────────┘
                              with the API key
```

1. A client creates a project, an agent in it and a session of the agent,
   and sends the session a message.
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
   puts the key in its place and records the request. Whatever else the
   agent fetches goes through the same proxy, which lets a request through
   to the hosts that the installation allows and records it with its turn.
   A network policy keeps the pod from connecting to anything but the
   Supervisor and the proxy, and it has no DNS.
5. When Kubernetes reports that the pod has stopped, the Supervisor ends the
   turn, keeps what the agent said and did in it, and deletes the Job. Only
   then does the session take its next message, so two pods never work on
   the same session home.

The Supervisor keeps its records in PostgreSQL and the events of running
turns in Valkey. It only needs their addresses, so both can be managed
services. The Egress Proxy is [Envoy](https://www.envoyproxy.io), which the
chart installs. The model endpoint is a URL and a key, so it can be the API
of a provider, an AI gateway or a model server of your own.

The record of the Egress Proxy is a line of JSON for every request, on its
standard output. A line names the turn and the session, the agent and the
project that the turn belongs to, so it tells by itself where a request came
from. It also has the time, the host, the path if the request was not for an
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

`make cluster-down` deletes the cluster.

### Sending a message yourself

To have a real model answer, write the base URL of a model endpoint, its key
and the model's name to a file named `.env` in the repository, and run
`make deploy` again. The endpoint must be reachable from the cluster, so
`localhost` will not do. OpenCode needs a server with the Chat
Completions API of OpenAI, Codex one with the Responses API.

```sh
AVIARY_LLM_BASE_URL=http://model-server.example.com/v1
AVIARY_LLM_API_KEY=...
AVIARY_LLM_MODEL=...
```

The API listens inside the cluster. Forward a port to it and send a message:

```sh
kubectl --context kind-aviary --namespace aviary-system \
  port-forward service/aviary-supervisor 8080:80 &

project=$(curl -s -X POST localhost:8080/v1/projects -d '{"name": "demo"}' | jq -r .id)
agent=$(curl -s -X POST localhost:8080/v1/projects/$project/agents -d '{"harness": "opencode"}' | jq -r .id)
session=$(curl -s -X POST localhost:8080/v1/agents/$agent/sessions | jq -r .id)

turn=$(curl -s -X POST localhost:8080/v1/sessions/$session/messages \
  -d '{"text": "Create a file named hello.txt that says hello."}' | jq -r .id)
curl -N localhost:8080/v1/turns/$turn/events
```

The last command prints the events of the turn as they happen and returns
when the turn has ended. Send the session another message, and the agent
still knows what was said and finds the file.

## API

| Request | |
| --- | --- |
| `POST /v1/projects` | Creates a project. Body: `{"name": "..."}`. |
| `POST /v1/projects/{id}/agents` | Creates an agent. Body: `{"harness": "...", "instructions": "...", "resources": {...}, "approval": "ask"}`. The harness is one of the configured harnesses. The instructions are what the agent is told before every turn, and may be left out. The resources are what the pod of each turn asks for and may use at most, as they are written for Kubernetes: `{"requests": {"cpu": "250m", "memory": "1Gi"}, "limits": {"memory": "4Gi"}}`. Given, they are taken as they are. Left out or empty, the agent has none of its own, and each of its turns gets the default of the harness as it then is. With `"approval": "ask"`, the agent asks a person before it acts: see [Approval](#approval). Left out, it asks nobody. |
| `POST /v1/agents/{id}/sessions` | Creates a session. |
| `POST /v1/sessions/{id}/messages` | Sends a message. Body: `{"text": "..."}`. Answers 202 with the turn, 409 if a turn of the session has not ended, or 429 with `Retry-After` if too many turns wait to run. |
| `POST /v1/turns/{id}/cancel` | Cancels a turn. A turn that waits to run ends at once: 200 with the turn. A turn that runs is told to stop and ends when its pod has stopped: 202 with the turn. Answers 409 if the turn has ended in another way. |
| `POST /v1/turns/{id}/approvals/{request}` | Answers a request for approval of the turn. Body: `{"option_id": "..."}`, the `id` of one of the options of the request. Answers 202 when the answer is on its way to the harness, 400 if the request has no such option, and 409 if the request has its answer or the turn has ended. |
| `GET /v1/turns/{id}/events` | The events of the turn, as server-sent events. A client that reconnects sends `Last-Event-ID` and gets the events after it. |
| `GET /v1/sessions/{id}/turns` | The turns of the session, newest first: `{"turns": [...]}`. `?limit=` is how many, 20 if it is not given and at most 100. `?before=` is the `seq` of a turn, and the answer then holds the turns before that one: that is how to read on from the last turn of an answer. The first turn of a session has `seq` 1. |
| `GET /v1/projects/{id}`, `/v1/agents/{id}`, `/v1/sessions/{id}`, `/v1/turns/{id}` | Reads what was created. |

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
does not install PostgreSQL or Valkey.

| Value | |
| --- | --- |
| `supervisor.replicas`, `egressProxy.replicas` | How many Supervisors and Egress Proxies run. Each of them serves any request. Default 1. |
| `supervisor.resources`, `egressProxy.resources` | What the pod of a Supervisor and of an Egress Proxy asks for and may use at most. |
| `supervisor.database.secretName`, `.secretKey` | A secret with the PostgreSQL connection URL. |
| `supervisor.valkey.secretName`, `.secretKey` | A secret with the Valkey connection URL. |
| `supervisor.encryption.secretName`, `.key` | A secret with the keys that the Supervisor seals secrets with before it stores them, and the entry of it that seals. An entry is 32 random bytes in base64. To replace the key, add an entry and name it, and keep the entries from before. |
| `llm.url`, `llm.model` | The base URL of the model endpoint and the model that the harnesses ask it for. |
| `llm.apiKey.secretName`, `.secretKey` | A secret with the API key of the model endpoint. Only the Egress Proxy reads it. |
| `egressProxy.allowedHosts` | The hosts that the Egress Proxy lets requests through to, the model endpoint among them. The default, `*`, is every host. |
| `egressProxy.insideCIDRs`, `.extraEgress` | A network policy keeps the Egress Proxy itself out of these address ranges, so that it is no way into the cluster. `extraEgress` names what it may still reach there, such as a model endpoint that is a service in the cluster. |
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
[Harness images](images/README.md).

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
| `supervisor/` | The Supervisor, in Go: the API, the queue, the Jobs, the connection to the runners, the event streams. |
| `runner/` | The runner, in Rust. |
| `proto/` | The protocol between the runner and the Supervisor. |
| `images/` | The images of the Supervisor, the runner and the harnesses. |
| `deploy/charts/aviary/` | The Helm chart. |
| `hack/kind/` | The local cluster. |
| `test/` | The tests of the chart, of the Egress Proxy and of the parts together, and the mock model server. The tests of the Supervisor and of the runner are with their code. |

## What is missing

- **Authentication.** Anyone who reaches the API can use it.
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
