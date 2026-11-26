---
title: Observability
description: Where the metrics, the logs and the records of an installation are.
---

## Metrics

The Supervisors, the state of the installation and the depots serve
Prometheus metrics, each pod on port 9092 at `/metrics`. Agent Substrate
serves metrics of its own.
[Metrics](../reference/metrics.md) lists them.

The Grafana dashboard in `deploy/grafana/aviary.json` charts them, and can
be imported into any Grafana with a Prometheus data source. In the local
cluster, `make grafana` installs [Prometheus](https://prometheus.io), which
scrapes the metrics of Aviary, and [Grafana](https://grafana.com) with the
dashboard, at http://localhost:3000.

## Requests that leave the sandboxes

The egress gateway of Substrate logs every request that a sandbox sends out.
A Supervisor logs every secret that it gives the gateway, as a line of JSON
with the turn, the session, the agent, the project and the user that it came
from: see [The log](./egress.md#the-log).

## Turns

What an agent said and did in a turn is kept with the turn, and so is how
the turn ended: see [Turn records](../reference/api/turns.md#turn-records).
The token usage of a turn is what its harness reports, not a measurement.

## The cluster

In the local cluster, `make headlamp` installs
[Headlamp](https://headlamp.dev), a web UI for the resources, logs and
events of the cluster, at http://localhost:4466.
