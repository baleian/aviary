# Harness images

A harness is the agent program that Aviary runs for a turn, such as OpenCode
or Codex. A harness image packages one harness. This page describes what
Aviary does with a harness image and what the image must provide, so that you
can build an image for a harness that this repository does not ship.

## How a session uses the image

A session runs in a sandbox that is made from the harness image. The sandbox
runs `aviary-session`, which starts the runner, `aviary-runner`. Both are in
`aviary-harness-base`. The runner takes the turns of the session, starts the
harness for the first of them, and passes on what the harness does.

The harness runs like this:

| | |
| --- | --- |
| User | `agent`, with UID 10000 and GID 10000. The sandbox is what keeps the agent in, so inside it the user may become root with `sudo`, to install what the agent needs. |
| Home | `/home`, with `HOME` set to it. |
| Working directory | `/home/workspace` |
| Model endpoint | `AVIARY_LLM_BASE_URL`, `AVIARY_LLM_API_KEY` and `AVIARY_LLM_MODEL`. |
| Approval | `AVIARY_APPROVAL=ask` for an agent that is to ask a person before it acts. Other agents do not have the variable. |
| What lasts | Everything, for as long as the session has its sandbox: the whole file system, and the harness itself, which stays when a turn has ended. |

The runner talks to the harness over its standard input and output with the
[Agent Client Protocol](https://agentclientprotocol.com) (ACP). When it
starts the harness it sends `initialize` and then `session/new`, or
`session/resume` when it starts the harness of a session again. For a turn
it sends `session/prompt` with the message as text. From the harness's
`session/update` notifications it passes on the agent's message and thought
text, tool calls and their updates, and plans. When the prompt returns, the
turn is over, and the harness stays for the next one. To stop a turn before
that, because it was cancelled, the runner sends `session/cancel`, and a
harness that ends the turn then answers the prompt with the stop reason
`cancelled`. A turn that is stopped before the prompt was sent gets none.

What the runner does with `session/request_permission` depends on the agent.
For an agent that is to ask, it passes the request on with the options that
the harness gave, and answers with the option that a person picked. The
harness waits for that as long as the turn runs. A request that is open when
the turn is cancelled is answered as cancelled. For any other agent the runner
allows the action once, by itself.

## What the image must provide

1. **The command that starts the harness as an ACP agent**, in the
   environment variable `AVIARY_HARNESS_COMMAND`. The runner runs it and
   speaks ACP on its standard input and output.
2. **It has what the sandbox runs.** That is `aviary-session` and
   `aviary-runner`, the user `agent` with UID 10000 and GID 10000, who may
   use `sudo`, and `/home/workspace`, which is the user's. An image that
   builds on `images/harness-base` has them. Everything the harness needs to
   read or execute must be usable by that user.
3. **Its record of the conversation is on disk.** A harness that is started
   again continues its session from there.
4. **It takes the model endpoint from the environment.** The image
   configures the harness so that it reads `AVIARY_LLM_BASE_URL`,
   `AVIARY_LLM_API_KEY` and `AVIARY_LLM_MODEL`, and sends the key as a bearer
   token (`Authorization: Bearer ...`). The egress of the sandbox puts the
   key of the endpoint there.
5. **It supports `session/resume`.** The harness is started for the first
   turn of a session and stays for the turns after it. One that has ended is
   started again for the next turn, and continues the harness session there.
6. **The file that the harness reads its instructions from**, in the
   environment variable `AVIARY_INSTRUCTIONS_FILE`. Before it starts the
   harness, the runner writes there what the agent is to be told in every
   turn, or nothing for an agent without instructions. The user of the
   harness must be able to write the file. Most harnesses have such a file
   for instructions that hold in every session, often named `AGENTS.md`. An
   image that names none runs only agents without instructions.

7. **It asks before it acts if `AVIARY_APPROVAL` is `ask`.** The image
   starts the harness so that it sends `session/request_permission` before
   it does what a person should agree to first. What that covers is for the
   image to decide, with the settings of its harness. An image that does not
   read the variable runs an agent that is to ask like any other: nobody is
   asked.

Environment variables that begin with `AVIARY_` are reserved for Aviary. The
harness also sees the ones that the runner uses, such as
`AVIARY_MAX_TOOL_OUTPUT_BYTES`.

A harness may report the token usage of a turn in its answer to
`session/prompt`, as `usage`. Aviary records it with the turn as the harness
gives it. ACP marks this field as unstable.

## What to look out for

A harness is usually written for one person at one machine, who starts it and
is there to see what it does. Aviary starts it for the first turn of a
session, with a home that was empty when the session began, and nobody is
there. The images here ran into these cases:

- **Work that the harness repeats for every new home.** Codex downloaded its
  plugin catalog, about 90 MB, into each new session home. The image turns
  the feature off. OpenCode fetches a list of models at startup, which the
  image also turns off.
- **What the harness does on its own while it stays.** The ACP adapter of
  Codex has a model of its own choosing make a title for a new session, when
  the first turn has ended. It asks the provider that the configuration
  names for whoever names none, which for a configuration without one is
  OpenAI. The image names a provider there that reaches nobody, and names
  the provider of the agent's model for each session.
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
- **A sandbox that is the only thing that asks.** Codex asks a person only
  when a command is to leave its sandbox, and the sandbox does not start in
  a pod. For an agent that is to ask, the image starts Codex in its
  read-only mode all the same: a command that the model tries inside the
  sandbox fails, and it runs when the model has asked for it to run outside
  and a person has allowed that. The failed try costs one more request to
  the model.
- **What the harness makes of a refusal.** Both harnesses here end the turn
  when a person refuses an action: OpenCode with the stop reason `end_turn`,
  Codex with `cancelled`. Aviary records the turn as the harness ends it.
- **How long "always" lasts.** An option of the kind `allow_always` holds for
  as long as the harness remembers it. OpenCode forgets it with its process,
  so when the harness is started again. Codex writes a rule into its
  directory in the session home, which it reads when it is started again.
- **Time to start.** The harness starts with the first turn of a session, so
  its startup time is part of the first answer.

## The images in this repository

| Image | Contents |
| --- | --- |
| `harness-base` | Node.js, git and the user with UID 10000. The base of the two below. |
| `harness-opencode` | [OpenCode](https://opencode.ai), started with `opencode acp`. It talks to the model server with the Chat Completions API. Told to ask, it asks before it runs a command or changes a file. |
| `harness-codex` | [Codex](https://github.com/openai/codex) behind its ACP adapter, `codex-acp`. It talks to the model server with the Responses API. Told to ask, it runs in Codex's read-only mode and asks before a command runs outside its sandbox. |

## Adding a harness

1. Write a Dockerfile that meets the seven points above. Building on
   `aviary-harness-base` gives you the second.
2. Build the image and test it:

   ```sh
   make test-harness HARNESS_IMAGES=registry.example.com/aviary-harness-myharness:1.0
   ```

   The tests run sessions in containers of the image, each with the runner
   in it and the tests sending it the turns, and need no cluster. They use a mock model server
   that speaks the Chat Completions API and the Responses API, and they have
   the agent run shell commands, so they need the harness to offer the model
   a tool with a `command` or a `cmd` parameter. A harness that fetches
   something for itself when it starts fails them, and so does one that runs
   a command of an agent that is to ask without asking.
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
