# Tests

A fact is checked at the lowest level that can show it, so that a mistake is
found by the fastest run that can find it. The e2e has only what needs a
cluster.

| Level | Command | What runs | What it shows |
| --- | --- | --- | --- |
| Lint | `make lint` | gofmt, go vet, rustfmt and clippy, and the generators of the Go code. | The code is formatted, the linters object to nothing, and the generated code is what the generators make of its sources now. |
| Unit and component | `make test` | Each part by itself, with what it talks to played by the test. Takes about 15 seconds. | What a part does: the Supervisor, the runner, the chart, the Egress Proxy, the mock model server. |
| Harness | `make test-harness` | The harness images in containers, each with the runner that takes the turns of a session, with a mock model server. The tests are in `runner/tests/harness.rs`. Takes under a minute. | A harness and the runner work together: sessions, tools, cancelling, failures, what the agent installs, and what the harness asks of the outside. |
| e2e | `make e2e` | Aviary as the chart installs it, in a kind cluster that the run makes and removes. The tests are in `test/e2e/`. Takes about seven minutes, three of them to make the cluster. | The parts work together: a turn from the API to the sandbox of its session and back, a session that sleeps and wakes, what a sandbox reaches through the Egress Proxy, a session that is ended, a sandbox that is lost, turns while Supervisors are replaced. |

No test needs a model. The mock model server in `test/mock-llm` answers by a
script: a message to a session is an instruction to it, such as `say done`
or `run ls`.

## The parts of `make test`

`make test` runs these at once. Each is a target of its own.

| Target | Where | What the part runs against |
| --- | --- | --- |
| `test-supervisor` | `supervisor/` | The sandboxes of the sessions are those of `internal/sandbox/sandboxtest`, in memory as the cluster keeps them, with a runner in each that the test plays, and the client of agent-sandbox is tested against the fake of its clientset. The test is the API client and the Egress Proxy. The tests in `supervisor/app` run the whole Supervisor, several of them where it matters, with its records and its event log in memory. Those in the other directories test a package. A plugin is tested against what it connects to, in containers that the run starts and removes: a database of each kind (PostgreSQL and MySQL), an event log of each product (Valkey and Redis) and a secret store (OpenBao). What holds of every store of the records is in `supervisor/store/storetest`, and what holds of every event log in `supervisor/events/eventstest`: the plugins pass them, and so do the store and the log in memory. |
| `test-plugin` | `test/plugin/` | A build of the Supervisor outside its module, with plugins of its own that keep everything in memory. It passes the suites of the stores and of the logs, and it is a Supervisor with the plugins that it lists. |
| `test-runner` | `runner/` | The tests in `tests/turn.rs` run the runner with `fake-harness`, a harness that does what its message says, and send it the turns of a session as the Supervisor does. Those in `src/main.rs` test its functions. |
| `test-chart` | `test/chart/` | What the chart renders from its own values and from those in the directory. |
| `test-proxy` | `test/proxy/` | Envoy with what the chart renders. The test is the sandbox, the Supervisor, the model endpoint and the hosts outside. |
| `test-mock-llm` | `test/mock-llm/` | The mock model server. |

`TESTS` narrows a run to some tests. It is passed to `go test` or to
`cargo test`:

```sh
make test-supervisor TESTS='-run TestQueue .'
make test-supervisor TESTS='-run TestStore/EndTurn ./store/postgres'
make test-runner TESTS=cancel
make test-harness TESTS=instructions
make test-harness HARNESS_IMAGES=aviary-harness-codex:dev
```

The last of these tests one harness image. An image that is not in this
repository is tested the same way: see [Harness images](../images/README.md).

## The e2e

`make e2e` is `make e2e-up`, `make e2e-run` and `make e2e-down`. Its
cluster has what the local cluster has, and with it a mock model server and
a secret store, OpenBao, in which a test keeps a key. `E2E_CLUSTER` names
the cluster for each of the three, `aviary-e2e` if it is not given, and
`make e2e-up` makes the cluster with what it is given: with `DATABASE=mysql`
it has MySQL in the place of PostgreSQL. With `IDENTITY=keycloak` it has an
identity provider, Keycloak, and Aviary asks it who sends a request: the
tests then send their requests with tokens of its users, and one more test
checks what each of them may do. Clusters under different names do not touch
each other, so the e2e can run in several at once, each made in its own way.
Each has a kubeconfig of its own, and none of them writes to that of the
user. Two of them at once take about 11 GiB of memory at the most.

```sh
make e2e & make e2e E2E_CLUSTER=aviary-e2e-mysql DATABASE=mysql IDENTITY=keycloak & wait
```

When a test fails the cluster stays, and the logs of the Supervisors, of the
Egress Proxies and of agent-sandbox, the pods, the sandboxes and the volumes,
and the events of the cluster are written to `.out/e2e`, in a directory by
the name of the cluster. The kubeconfig of the cluster is there too, until
`make e2e-down` removes the cluster:

```sh
KUBECONFIG=.out/e2e/aviary-e2e/host.kubeconfig kubectl get pods --all-namespaces
```

`make e2e-run` runs the tests in the cluster again, or some of them:

```sh
make e2e-run TESTS='-run "TestAviary/(the_installation|turns)/a_turn_is_cancelled"'
make e2e-run TESTS='-run "TestAviary/(the_installation|a_turn_waits)"'
```

The first test waits until the new cluster runs turns, and nothing else is
tried if it fails, so keep it in the pattern. The tests that run at once are
one level below `turns`. In a cluster that was made with `IDENTITY`,
`make e2e-run` is given it too.

What does not depend on the harness runs with the first of `E2E_HARNESSES`,
and every harness runs one session.

A test ends the sessions that it made, which `session` in
`test/e2e/e2e_test.go` does for it: the sandbox of a session and its volume
stay until the session is ended, and the cluster has room for only so many.

A session sleeps after half a minute without work in the e2e
(`test/e2e/values.yaml`), so that the tests see sessions sleep and wake
without waiting long for it. A turn that wakes its session waits for the new
pod of its sandbox to start.

## Writing a test

- Put it at the lowest level that shows the fact. A test of the e2e is for
  what no single part can show.
- A test makes what it needs and shares nothing: every test of the Supervisor
  has records of its own, every harness test a network and a home of its
  own, every e2e test its own project. That is what lets the tests run at
  once, and those of the Supervisor in a different order every time.
- Wait for what is to happen, with a deadline. Do not sleep for as long as it
  usually takes.
- A test that cannot reach what it needs fails. It is not skipped: a skipped
  test looks like one that passed.
- A fix comes with a test that fails without it.
