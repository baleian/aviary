---
title: Metrics
description: The Prometheus metrics of the Supervisors and of the state of the installation.
---

A Supervisor serves Prometheus metrics on port 9092 at `/metrics`. The port
is not part of the Supervisor's Service, so the metrics are scraped from
each pod.

## Supervisor

| Metric | Description |
| --- | --- |
| `aviary_turn_queue_length` | Number of queued turns when a turn was accepted, that turn included. |
| `aviary_turns_followed` | Turns that this Supervisor follows, by `harness`. |
| `aviary_turns_ended_total` | Ended turns, by `harness`, `state` and, for a failed turn, `reason`. |
| `aviary_turn_queue_duration_seconds` | Time that a turn waited to run, from its message until it left the queue, by `harness`. |
| `aviary_turn_startup_duration_seconds` | Time that a turn took to start, from leaving the queue until its harness had the message, by `harness`. |
| `aviary_turn_run_duration_seconds` | Time that a turn ran, from its harness having the message until the turn ended, by `harness` and `state`. |
| `aviary_turn_tokens_total` | Token usage that the harnesses reported, by `harness` and `kind`. Not a measurement: see [Turn records](./api/turns.md#turn-records). |
| `aviary_approval_wait_seconds` | Time that an approval request waited, from the harness asking until the harness had its answer, by `harness` and `outcome`: the kind of the selected option, or `cancelled` if the turn was stopped first. |
| `aviary_messages_refused_total` | Messages that were not accepted as turns, by `reason`: `queue_full` or `session_busy`. |
| `aviary_secret_read_seconds` | Time that a secret store of a project took to return a value, by the store's `kind` and `outcome`: `read` or `failed`. Cached values are not read and not counted. |

Each Supervisor reports its own work, so the metrics of all Supervisors add
up. `aviary_turns_followed` shows how the turns are spread over the
Supervisors; how many turns there are, also in the queue, is among the state
metrics below. No Supervisor holds the queue, so `aviary_turn_queue_length`
measures it whenever a turn is accepted. Every series of the configured
harnesses and secret store kinds starts at zero, so that `rate()` and
`increase()` count its first event. Go runtime and process metrics are
served as well.

## State of the installation

The state of the installation, such as the number of its sandboxes, is not
the work of one Supervisor, and every Supervisor would report the same. A
single pod, `aviary-state-metrics`, reports it instead, on the same port and
path, so that these metrics add up like the others. It runs the image of the
Supervisor with the command `state-metrics` and reads the database at every
scrape; a scrape fails while it cannot. Setting `stateMetrics.enabled` to
false leaves the pod out.

| Metric | Description |
| --- | --- |
| `aviary_sandboxes` | Sandboxes of the sessions that have not ended, by `state`: `awake`, `asleep` or `lost`, as the sessions say of their `sandbox`. |
| `aviary_turns` | Turns that have not ended, by `harness` and `state`: `queued`; `starting`, from leaving the queue until the harness has the message; `running`; or `awaiting_approval`, while a request of the turn waits for a person. |
| `aviary_turns_oldest_age_seconds` | Age of the oldest turn of each `harness` and `state`: the time since its message for a queued turn, and since it left the queue for the others. 0 without such a turn. An age well beyond the limit of its state, `runtime.maxQueueWaitSeconds`, `runtime.maxStartupSeconds` or `runtime.maxTurnSeconds`, means that no Supervisor is ending the turn. |
| `aviary_approvals_waiting` | Approval requests that wait for a person's answer, by `harness`. A turn can have several. |

## Dashboard

The Grafana dashboard in `deploy/grafana/aviary.json` charts the metrics of
the Supervisors and of the state, and can be imported into any
Grafana with a Prometheus data source. `deploy/grafana/substrate.json`
charts those of Agent Substrate.
