---
title: Testing
description: What each level of tests checks, how to run some of them, and where a new test goes.
---

Each behavior is tested at the lowest level that can show it, so that a
mistake is caught by the fastest run that can catch it. The e2e covers only
what needs a cluster.

```sh
make lint          # formatters, linters and generated code
make test          # each component on its own, without a cluster
make test-harness  # the harness images, in containers with a session's runner
make e2e           # all components together, in a kind cluster of its own
```

| Level | Command | What runs | What it checks |
| --- | --- | --- | --- |
| Lint | `make lint` | gofmt, go vet, rustfmt, clippy, the Go code generator, for `web/` Prettier, oxlint and the TypeScript compiler, and Prettier for the code in `docs/`. | The code is formatted, the linters pass, and the generated code is up to date. |
| Unit and component | `make test` | Each part on its own, with the test playing what it talks to. | What each part does: the Supervisor, the runner, the chart, the mock model server, the code in `web/`, the server of the web UI's image, and the documentation: its links, and the requests and metrics of its reference. |
| Harness | `make test-harness` | The harness images in containers, each with the runner, against the mock model server. The tests are in `runner/tests/harness.rs`. | A harness and the runner work together: sessions, tools, the tools of MCP servers, cancellation, failures, what the agent installs, and what the harness requests from outside. |
| e2e | `make e2e` | Aviary as the chart installs it, on Agent Substrate, in a kind cluster that the run creates and deletes. The tests are in `test/e2e/`. | The parts work together: a turn from the API to its session's sandbox and back, sessions that sleep and wake, what a sandbox can reach and the secrets that the egress gateway puts into its requests, the tools of MCP servers, ending a session, losing a sandbox, and turns while the Supervisors are replaced. |

No test needs a model. The mock model server in `test/mock-llm` follows a
script: each message to a session is an instruction, such as `say done` or
`run ls`. It is also the MCP server of the tests, whose tool a message calls
with `use`.

All but `make e2e` also need Rust, which rustup installs in the version that
`rust-toolchain.toml` names. `make lint` and `make test` also need Node.js
for the code in `web/` and `docs/`, and are tested with Node.js 26.
`make e2e` creates its own cluster and deletes it when the tests pass, so it
does not touch the cluster of `make cluster-up`.

## `make test`

`make test` runs these targets in parallel. Each can also be run on its own.

| Target | Directory | What the tests run against |
| --- | --- | --- |
| `test-supervisor` | `supervisor/` | Session sandboxes from `internal/sandbox/sandboxtest`, kept in memory as Substrate keeps them, each with a runner that the test plays. The client of Substrate is tested against an API of Substrate in memory. The test plays the API client and the egress gateway, and serves the endpoints of providers and MCP servers. The tests in `supervisor/app` run the whole Supervisor, several replicas where it matters, with the store and the event log in memory; the other directories test single packages. Plugins are tested against the real products, in containers that the run starts and removes: PostgreSQL and MySQL, Valkey and Redis, and OpenBao as a secret store. `supervisor/store/storetest` and `supervisor/events/eventstest` are the suites that every store and every event log passes, the in-memory ones included. |
| `test-plugin` | `test/plugin/` | A build of the Supervisor outside its module, with in-memory plugins of its own. It passes the store and event log suites, and has exactly the plugins that it lists. |
| `test-runner` | `runner/` | `tests/turn.rs` runs the runner with `fake-harness`, a harness that does what its message says, and sends it the turns of a session as the Supervisor does. The tests in `src/main.rs` test its functions. |
| `test-chart` | `test/chart/` | The chart rendered with its default values and with the values files in the directory. |
| `test-mock-llm` | `test/mock-llm/` | The mock model server. |
| `test-web` | `web/` | Node.js, with the API answered in memory, and the screens in a DOM without a browser. The tests are next to the code, in `*.test.ts` and `*.test.tsx`. |
| `test-docs` | `docs/`, `test/docs/` | The pages, built into the site: the build fails if a link between them leads to a page or a heading that is not there. The tests compare the requests and the metrics that the reference names with those of the Supervisor. |
| `test-web-server` | `test/web/` | The server of the web UI's image, nginx, with the configuration that the image starts it with: once in front of an API and once alone. The test plays the browser and the API. |

`TESTS` selects tests. It is passed to `go test`, `cargo test` or Vitest:

```sh
make test-supervisor TESTS='-run TestQueue .'
make test-supervisor TESTS='-run TestStore/EndTurn ./store/postgres'
make test-runner TESTS=cancel
make test-web TESTS=client
make test-harness TESTS=instructions
make test-harness HARNESS_IMAGES=aviary-harness-codex:dev
```

The last line tests a single harness image. Images built outside this
repository are tested the same way; see [Harness images](../operate/harness-images.md).

## e2e

`make e2e` runs `make e2e-up`, `make e2e-run` and `make e2e-down`. The cluster
is set up like the local cluster, with Agent Substrate, plus the mock model
server and an OpenBao secret store, in which a test keeps a provider key. The
tests read the sandboxes from the API of Substrate.

- `E2E_CLUSTER` names the cluster for all three targets. Defaults to
  `aviary-e2e`.
- The cluster has PostgreSQL and Valkey. For another, name the targets that
  make it before `e2e`, as for the local cluster: `make cluster-up mysql
  redis e2e`. A run that names a target of the e2e works on the cluster of
  the e2e with all of its targets.
- `keycloak` among them installs Keycloak as the identity provider that
  authenticates API requests. The tests then send their requests with the
  tokens of its users, and one more test checks what each user may do.

Clusters with different names are independent, so the e2e can run in several
at once, each configured differently. Each cluster has its own kubeconfig, and
yours is not changed.

```sh
make e2e & make cluster-up mysql valkey keycloak e2e E2E_CLUSTER=aviary-e2e-mysql & wait
```

If a test fails, the cluster is kept, and the logs of the Supervisors, the
state metrics, the depots and of Substrate, the pods, and the cluster events
are written to `.out/e2e/<cluster>`. The
cluster's kubeconfig is there too, until `make e2e-down` deletes the cluster:

```sh
KUBECONFIG=.out/e2e/aviary-e2e/host.kubeconfig kubectl get pods --all-namespaces
```

`make e2e-run` runs the tests again in the existing cluster, or some of them:

```sh
make e2e-run TESTS='-run "TestAviary/(the_installation|turns)/a_turn_is_cancelled"'
make e2e-run TESTS='-run "TestAviary/(the_installation|a_turn_waits)"'
```

Keep the first test in the pattern: it waits until a new cluster runs turns,
and nothing else runs if it fails. The tests that run in parallel are one
level below `turns`. In a cluster that has Keycloak, the tests sign in.

Tests that depend on the harness run for each harness in `E2E_HARNESSES`; the
others run with the first.

A test must end the sessions it creates, which `session` in
`test/e2e/e2e_test.go` does: a session's sandbox and its snapshot stay
until the session is ended, and the workers have room for a limited number.

`test/e2e/values.yaml` shortens the idle time after which a session sleeps,
so that the tests see sessions sleep and wake without a long wait. A turn
that wakes its session waits for its sandbox to be restored from its
snapshot.

## Writing a test

- Put it at the lowest level that can show the behavior. An e2e test is for
  what no single part can show.
- A test creates what it needs and shares nothing: each Supervisor test has
  its own records, each harness test its own network and home, and each e2e
  test its own project. This lets the tests run in parallel, and the
  Supervisor tests in a random order.
- Wait for a condition, with a deadline. Do not sleep for as long as it
  usually takes.
- A test that cannot reach what it needs fails rather than skips: a skipped
  test looks like one that passed.
- A fix comes with a test that fails without it.
