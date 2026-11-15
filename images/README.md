# Harness images

A harness is the agent program that Aviary runs for a turn, such as OpenCode
or Codex. A harness image packages one harness. This page describes what
Aviary does with a harness image and what the image must provide, so that you
can build an image for a harness that this repository does not ship.

## How a turn uses the image

For every turn the Supervisor creates a Job with one pod. The pod's `agent`
container runs the harness image, but not the image's own command: it runs
the runner, `aviary-runner`, which is mounted from its own image at
`/aviary/bin`. The runner takes the user's message from the Supervisor,
starts the harness, and passes on what the harness does.

The container is set up like this:

| | |
| --- | --- |
| User | UID 10000, GID 10000. Not root, no privilege escalation, all capabilities dropped. |
| Home | `/home`, with `HOME` set to it. It is the home of the session, on a volume. It is the only part of the file system that the next turn of the session sees again. |
| Working directory | `/home/workspace` |
| Model endpoint | `AVIARY_LLM_BASE_URL`, `AVIARY_LLM_API_KEY` and `AVIARY_LLM_MODEL`. The address is the Egress Proxy and the key is the token of the turn. The proxy puts the real key in its place, so the pod never holds it. |
| Network | The pod connects to the Supervisor and the Egress Proxy and to nothing else, and it has no DNS. `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY`, also in lower case, point to the proxy, which lets requests through to the hosts that the installation allows. |
| Kubernetes API | No service account token is mounted. |
| Time | The pod is stopped when it has run for as long as the installation lets a turn run, which is 30 minutes unless it is set otherwise. |

The runner talks to the harness over its standard input and output with the
[Agent Client Protocol](https://agentclientprotocol.com) (ACP). In a turn it
sends `initialize`, then `session/new` for the first turn of a session or
`session/resume` for a later one, then `session/prompt` with the message as
text. From the harness's `session/update` notifications it passes on the
agent's message and thought text, tool calls and their updates, and plans.
It answers `session/request_permission` by allowing the action once. When the
prompt returns, the turn is over and the harness is stopped. To stop a turn
before that, because it was cancelled or because its pod is about to be
stopped, the runner sends `session/cancel`, and a harness that ends the turn
then answers the prompt with the stop reason `cancelled`. A turn that is
stopped before the prompt was sent gets none.

## What the image must provide

1. **The command that starts the harness as an ACP agent**, in the
   environment variable `AVIARY_HARNESS_COMMAND`. The runner runs it and
   speaks ACP on its standard input and output.
2. **It runs as UID 10000 and GID 10000.** Everything the harness needs to
   read or execute must be usable by that user. `images/harness-base` creates
   the user.
3. **Its state is under `/home`.** Whatever the harness must find again in
   the next turn, above all its record of the conversation, has to be written
   below `/home`. Everything else is lost with the pod.
4. **It takes the model endpoint from the environment.** The image
   configures the harness so that it reads `AVIARY_LLM_BASE_URL`,
   `AVIARY_LLM_API_KEY` and `AVIARY_LLM_MODEL`, and sends the key as a bearer
   token (`Authorization: Bearer ...`). The Egress Proxy looks for the key
   there.
5. **It supports `session/resume`.** A later turn of a session continues the
   harness session that the first turn started, in a new process.
6. **The file that the harness reads its instructions from**, in the
   environment variable `AVIARY_INSTRUCTIONS_FILE`. Before it starts the
   harness, the runner writes there what the agent is to be told in every
   turn, or nothing for an agent without instructions. The user of the
   harness must be able to write the file. Most harnesses have such a file
   for instructions that hold in every session, often named `AGENTS.md`. An
   image that names none runs only agents without instructions.

Environment variables that begin with `AVIARY_` are reserved for Aviary. The
harness also sees the ones that the runner uses, such as `AVIARY_GATEWAY_URL`.

A harness may report the token usage of a turn in its answer to
`session/prompt`, as `usage`. Aviary records it with the turn as the harness
gives it. ACP marks this field as unstable.

## What to look out for

A harness is usually written to be started once and to stay up. Aviary starts
it again for every turn, with a home that was empty when the session began.
The images here ran into these cases:

- **Work that the harness repeats for every new home.** Codex downloaded its
  plugin catalog, about 90 MB, into each new session home. The image turns
  the feature off. OpenCode fetches a list of models at startup, which the
  image also turns off.
- **Configuration that the harness writes next to its state.** OpenCode
  installs packages into its configuration directory, so the image keeps the
  configuration in the image, at `/etc/xdg`, and not in the session home.
- **The harness's own sandbox.** Codex's sandbox creates namespaces, which a
  pod does not allow. The pod is the sandbox, so the image starts Codex with
  full access.
- **Settings that the harness does not read from the environment.** Codex
  takes the model and the server address from its configuration. The image
  starts it through a small script that puts the variables there.
- **Programs that do not use the proxy variables.** What a harness or its
  tools fetch from outside has to go through the proxy. git, npm and curl
  use the variables. Node's `fetch` does not, unless `NODE_USE_ENV_PROXY=1`
  is set, which `images/harness-base` does.
- **Time to start.** The harness starts on every turn, so its startup time
  is part of every answer.

## The images in this repository

| Image | Contents |
| --- | --- |
| `harness-base` | Node.js, git and the user with UID 10000. The base of the two below. |
| `harness-opencode` | [OpenCode](https://opencode.ai), started with `opencode acp`. It talks to the model server with the Chat Completions API. |
| `harness-codex` | [Codex](https://github.com/openai/codex) behind its ACP adapter, `codex-acp`. It talks to the model server with the Responses API. |

## Adding a harness

1. Write a Dockerfile that meets the six points above. Building on
   `aviary-harness-base` gives you the user.
2. Build the image and test it:

   ```sh
   make test-harness HARNESS_IMAGES=registry.example.com/aviary-harness-myharness:1.0
   ```

   The tests run turns of sessions in containers of the image, set up as
   the pods of turns are, and need no cluster. They use a mock model server
   that speaks the Chat Completions API and the Responses API, and they have
   the agent run shell commands, so they need the harness to offer the model
   a tool with a `command` or a `cmd` parameter.
3. Make the image available to the cluster. For the local cluster that is
   `kind load docker-image --name aviary <image>`.
4. Add the harness to the chart's values under a name of your choice:

   ```yaml
   runtime:
     harnesses:
       myharness:
         image: registry.example.com/aviary-harness-myharness:1.0
         # What the turns of an agent get that has no resources of its own.
         resources:
           requests: {cpu: 250m, memory: 512Mi}
           limits: {memory: 4Gi}
   ```

5. Create an agent with that name: `{"harness": "myharness"}`.
