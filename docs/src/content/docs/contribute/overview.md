---
title: Contributing
description: Where the parts of Aviary are in the repository, and where it is meant to be extended.
---

This section is for those who change Aviary. It assumes
[How it works](../start/how-it-works.md).

## The repository

| Path | Contents |
| --- | --- |
| `supervisor/` | The Supervisor, in Go: the API, the queue, the session sandboxes on Agent Substrate and their runners, the event streams, and the secrets that the egress gateway asks for. Its plugins are in `store/`, `events/`, `secret/` and `identity/`, and `plugins.go` lists those of the build. The depot is a command of the same program, in `internal/depot`. |
| `runner/` | The runner, in Rust. |
| `web/` | The web UI, in TypeScript with React. |
| `images/` | The images of the Supervisor, the runner, the harnesses and the web UI. |
| `deploy/charts/aviary/` | The Helm chart. |
| `deploy/grafana/` | A Grafana dashboard of the metrics. |
| `hack/kind/` | The local cluster. |
| `test/` | The tests of the chart and of all components together, the mock model server, and a Supervisor build with plugins of its own. The tests of the Supervisor and of the runner are next to their code. |
| `docs/` | The documentation, which this site is built from. |

## Where to begin

- [Development environment](./development.md): run Aviary
  from a checkout, and see a change in it.
- [Testing](./testing.md): what each level of tests checks,
  how to run some of them, and where a new test goes.
- [Database migrations](./database-migrations.md): the rules
  for a change of the schema.
- [Documentation](./documentation.md): how these pages are
  written and built.

## Where Aviary is extended

Two parts are made to be replaced without a change to the rest:

- **Harnesses.** A harness image packages an agent program for Aviary. The
  runner talks to it over the Agent Client Protocol, so it does not depend
  on which harness it runs. [Harness images](../operate/harness-images.md)
  states what an image must provide and how it is tested.
- **Plugins.** The Supervisor reaches its database, its event log, the
  secret stores of projects and the identity provider through interfaces,
  and a plugin implements one of them for a kind of product.
  [Plugins](../operate/plugins.md#writing-a-plugin) describes how to
  write one, with the test suites that it must pass.
