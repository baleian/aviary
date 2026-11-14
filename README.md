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
   turn and deletes the Job. Only then does the session take its next
   message, so two pods never work on the same session home.

The Supervisor keeps its records in PostgreSQL and the events of running
turns in Valkey. It only needs their addresses, so both can be managed
services. The Egress Proxy is [Envoy](https://www.envoyproxy.io), which the
chart installs. The model endpoint is a URL and a key, so it can be the API
of a provider, an AI gateway or a model server of your own.

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
Egress Proxy, and that turns queue. It needs no model: it
uses a mock model server that answers by a script. `make cluster-down`
deletes the cluster.

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
| `POST /v1/projects/{id}/agents` | Creates an agent. Body: `{"harness": "..."}`, one of the configured harnesses. |
| `POST /v1/agents/{id}/sessions` | Creates a session. |
| `POST /v1/sessions/{id}/messages` | Sends a message. Body: `{"text": "..."}`. Answers 202 with the turn, or 409 if a turn of the session has not ended. |
| `POST /v1/turns/{id}/cancel` | Cancels a turn. A turn that waits to run ends at once: 200 with the turn. A turn that runs is told to stop and ends when its pod has stopped: 202 with the turn. Answers 409 if the turn has ended in another way. |
| `GET /v1/turns/{id}/events` | The events of the turn, as server-sent events. A client that reconnects sends `Last-Event-ID` and gets the events after it. |
| `GET /v1/projects/{id}`, `/v1/agents/{id}`, `/v1/sessions/{id}`, `/v1/turns/{id}` | Reads what was created. |

The events of a turn:

| Event | Data |
| --- | --- |
| `turn.queued` | `position`: the turn's place in the queue, 1 for the turn that runs next. |
| `turn.started` | The harness has been given the message. |
| `message.delta`, `thought.delta` | `text`: the next piece of what the agent says or thinks. `message_id`: pieces with the same ID belong together. |
| `tool.call` | `id`, `title`, `kind`, `status`, `input`: the agent started to use a tool. |
| `tool.update` | `id` and what changed of `title`, `kind`, `status`, `input`, `output`. |
| `plan.update` | `entries`: the agent's plan, each entry with `content`, `priority` and `status`. |
| `turn.completed` | `stop_reason`: why the harness ended the turn. `usage`: the token usage that the harness reported, if it did. The last event of the turn. |
| `turn.failed` | The turn did not complete. The last event of the turn. |
| `turn.cancelled` | The turn was cancelled. The last event of the turn. |

The fields of the events that come from the agent are defined in
[`proto/aviary/runner/v1/runner.proto`](proto/aviary/runner/v1/runner.proto).

`usage` holds `total_tokens`, `input_tokens` and `output_tokens`, and
`thought_tokens`, `cached_read_tokens` and `cached_write_tokens` if the
harness gives them. A turn that has ended has it too. It is what the harness
says, not a measurement, and harnesses count differently: Codex reports the
sum of the model requests of a turn, OpenCode only the last one. What the
Egress Proxy logs can be relied on: every model request, with its turn.

## Configuration

Aviary is installed with the Helm chart in `deploy/charts/aviary`. The chart
does not install PostgreSQL or Valkey.

| Value | |
| --- | --- |
| `supervisor.database.secretName`, `.secretKey` | A secret with the PostgreSQL connection URL. |
| `supervisor.valkey.secretName`, `.secretKey` | A secret with the Valkey connection URL. |
| `llm.url`, `llm.model` | The base URL of the model endpoint and the model that the harnesses ask it for. |
| `llm.apiKey.secretName`, `.secretKey` | A secret with the API key of the model endpoint. Only the Egress Proxy reads it. |
| `egressProxy.allowedHosts` | The hosts that the Egress Proxy lets requests through to, the model endpoint among them. The default, `*`, is every host. |
| `egressProxy.insideCIDRs`, `.extraEgress` | A network policy keeps the Egress Proxy itself out of these address ranges, so that it is no way into the cluster. `extraEgress` names what it may still reach there, such as a model endpoint that is a service in the cluster. |
| `runtime.maxConcurrentTurns` | The most turns that run at once. Default 50. |
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
- **Reading a conversation back.** The events of a turn are kept for 24
  hours after it ended.
- **The reason for a failed turn.** `turn.failed` does not say why, for
  example that the model server did not answer.
- **More than one Supervisor.** A turn that is running when the Supervisor
  restarts fails.
- **Approval by a person.** When a harness asks for permission to act, the
  runner allows it.
- **Anything but a local cluster.** Aviary has not run on a cloud provider's
  Kubernetes or under load.

## License

[Apache License 2.0](LICENSE)
