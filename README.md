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
> a turn, sessions continue across turns, and turns beyond a limit wait their
> turn. The pod of a turn reaches the outside only through a proxy that
> keeps a record. It has only run on a local cluster, and the API has no
> authentication. See [What is missing](#what-is-missing).

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
   stream of events. The harness reaches the model through the **Egress
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
make cluster-up            # a kind cluster with an NFS volume, PostgreSQL and Valkey
make images                # builds the images and loads them into the cluster
make deploy                # installs Aviary with its Helm chart
make e2e HARNESS=opencode  # or HARNESS=codex
```

`make e2e` creates sessions through the API and checks that turns run, that
a session continues, that sessions do not see each other, that the agent
does not hold the model's key and reaches the outside only through the
Egress Proxy, that turns queue, can be cancelled and expire, that a failed
turn says why, that the conversation can be read back, also of a turn that
was cut off, that an agent is told its instructions, and that the metrics
tell of the turns and the requests. It needs no model: it uses a mock model
server that answers by a script. `make cluster-down` deletes the cluster.

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
| `POST /v1/projects/{id}/agents` | Creates an agent. Body: `{"harness": "...", "instructions": "..."}`. The harness is one of the configured harnesses. The instructions are what the agent is told before every turn, and may be left out. |
| `POST /v1/agents/{id}/sessions` | Creates a session. |
| `POST /v1/sessions/{id}/messages` | Sends a message. Body: `{"text": "..."}`. Answers 202 with the turn, 409 if a turn of the session has not ended, or 429 with `Retry-After` if too many turns wait to run. |
| `POST /v1/turns/{id}/cancel` | Cancels a turn. A turn that waits to run ends at once: 200 with the turn. A turn that runs is told to stop and ends when its pod has stopped: 202 with the turn. Answers 409 if the turn has ended in another way. |
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
| `supervisor_lost` | The runner lost its connection to the Supervisor. |
| `runner_error` | The runner failed in another way. |

A turn that has ended has its reason too, as `failure`. What makes a turn
fail is up to the harness: when the model endpoint answers with an error,
OpenCode fails the turn, and Codex mostly ends it with the error as what the
agent says.

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

A turn that did not complete keeps what there was until then: the text that
the agent had written so far, and a tool call with the status that it had.
How the turn ended is told by its `state`, its `failure` and its
`stop_reason`. A turn has a `stop_reason` if its harness ended it by itself.
A turn without one was stopped from outside, or never reached its harness:
a turn has `started_at` once its harness has been given the message.

| `state` | `stop_reason` | |
| --- | --- | --- |
| `completed` | `end_turn`, `max_tokens`, `max_turn_requests` or `refusal` | The harness ended the turn. |
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
| `supervisor.database.secretName`, `.secretKey` | A secret with the PostgreSQL connection URL. |
| `supervisor.valkey.secretName`, `.secretKey` | A secret with the Valkey connection URL. |
| `llm.url`, `llm.model` | The base URL of the model endpoint and the model that the harnesses ask it for. |
| `llm.apiKey.secretName`, `.secretKey` | A secret with the API key of the model endpoint. Only the Egress Proxy reads it. |
| `egressProxy.allowedHosts` | The hosts that the Egress Proxy lets requests through to, the model endpoint among them. The default, `*`, is every host. |
| `egressProxy.insideCIDRs`, `.extraEgress` | A network policy keeps the Egress Proxy itself out of these address ranges, so that it is no way into the cluster. `extraEgress` names what it may still reach there, such as a model endpoint that is a service in the cluster. |
| `runtime.maxConcurrentTurns` | The most turns that run at once. Default 50. |
| `runtime.maxQueuedTurns` | The most turns that may wait to run. A message beyond it is answered with 429. Default 500, and 0 is no limit. |
| `runtime.maxQueueWaitSeconds` | How long a turn may wait to run before it ends as expired. Default 120, and 0 is no limit. |
| `runtime.maxStartupSeconds` | How long the pod of a turn may take to start before the turn fails. Default 90, and 0 is no limit. |
| `runtime.maxTurnSeconds` | How long a turn may run before it is stopped and fails. Default 1800. |
| `runtime.maxToolOutputBytes` | The longest text of a tool call that the events carry as it is. A longer text is cut. Default 65536, and 0 is no limit. |
| `runtime.harnesses` | The harnesses that agents can use, each with its image. |
| `runtime.store.storageClassName`, `.size` | The volume for the session homes. Its storage class must offer ReadWriteMany. |

The turn pods mount the runner as an image volume, so the cluster's
Kubernetes and container runtime must support image volumes, and its
network plugin must enforce network policies: they are what cuts the turn
pods off. The local cluster runs Kubernetes 1.37.

To run a harness that is not in this repository, see
[Harness images](images/README.md).

## Repository

| Path | |
| --- | --- |
| `supervisor/` | The Supervisor, in Go: the API, the queue, the Jobs, the connection to the runners, the event streams. |
| `runner/` | The runner, in Rust. |
| `proto/` | The protocol between the runner and the Supervisor. |
| `images/` | The images of the Supervisor, the runner and the harnesses. |
| `deploy/charts/aviary/` | The Helm chart. |
| `hack/kind/` | The local cluster. |
| `test/` | The end-to-end test and its mock model server. |

## What is missing

- **Authentication.** Anyone who reaches the API can use it.
- **Looking into what an agent sends.** The Egress Proxy records which host
  a turn connects to. It does not read what is sent over HTTPS.
- **Measured token usage.** The usage of a turn is what its harness reports.
  The Egress Proxy does not count tokens.
- **Surviving the loss of a Supervisor.** Several Supervisors can run side
  by side, and one that is stopped first finishes the turns whose runners
  are connected to it. If it is killed, those turns fail.
- **Approval by a person.** When a harness asks for permission to act, the
  runner allows it.
- **Anything but a local cluster.** Aviary has not run on a cloud provider's
  Kubernetes or under load.

## License

[Apache License 2.0](LICENSE)
