---
title: Observability
description: Where the metrics, the logs and the records of an installation are.
---

## Metrics

The Supervisors and the state of the installation serve Prometheus
metrics, each pod on port 9092 at `/metrics`.
[Metrics](../reference/metrics.md) lists them. The components of Agent
Substrate in `ate-system` serve metrics of their own at `/metrics`: the API
servers, atelet and the router on their ports named `prometheus` or
`metrics`, 9090, the egress gateway on 9090 and the controller on 8080. The
registry of the release, `docs/metrics/registry/metrics.yaml`, describes
them.

The Grafana dashboard in `deploy/grafana/aviary.json` charts them, and can
be imported into any Grafana with a Prometheus data source. The dashboard in
`deploy/grafana/substrate.json` charts the metrics that the components of
Agent Substrate serve: its workers and their placement, actors and their
snapshots, the store, the resources of the sandboxes, the caches of the
nodes, the router and the egress gateway, and the calls between them. In the
local cluster, `make grafana` installs [Prometheus](https://prometheus.io),
which scrapes the metrics of Aviary and of Substrate,
[Loki](https://grafana.com/oss/loki/) with
[Alloy](https://grafana.com/oss/alloy/), which collects the logs of their
pods, and [Grafana](https://grafana.com) with the two dashboards, at
http://localhost:3000.

## Requests that leave the sandboxes

The egress gateway of Substrate logs every connection that a sandbox opens
and every request that it reads, with the turn, the session, the agent, the
project and the user that it is for, and a Supervisor logs every secret
that it gives the gateway: see [The log](./egress.md#the-log). The Aviary
dashboard lists what the gateway logs in a table, from a Loki data source
whose lines have the `app` label of their pod and the name of their
`container`, as Alloy labels them in the local cluster.

## Turns

What an agent said and did in a turn is kept with the turn, and so is how
the turn ended: see [Turn records](../reference/api/turns.md#turn-records).
The token usage of a turn is what its harness reports, not a measurement.

## The cluster

In the local cluster, `make headlamp` installs
[Headlamp](https://headlamp.dev), a web UI for the resources, logs and
events of the cluster, at http://localhost:4466.
