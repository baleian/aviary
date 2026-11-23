# Harness images

A harness is the agent program, such as OpenCode or Codex, that runs the
turns of a session. A harness image packages one harness. This page describes
how Aviary uses a harness image and what the image must provide, so that you
can build one for a harness that this repository does not ship.

## How a session uses the image

Each session runs in a sandbox made from the harness image. The sandbox runs
`aviary-session`, which starts the runner, `aviary-runner`. Both come from
`aviary-harness-base`. The runner takes the session's turns, starts the
harness for the first one, and reports what the harness does.

The harness runs in this environment:

| | |
| --- | --- |
| User | `agent`, with UID 10000 and GID 10000, which cannot become root: the pod has no capabilities and meets the restricted Pod Security Standard. npm and pip install into the home, under `/home/.local`, whose programs are on the `PATH`. |
| Home | `/home`, also in `HOME`. |
| Working directory | `/home/workspace` |
| Model endpoint | `AVIARY_LLM_BASE_URL`, `AVIARY_LLM_API_KEY` and `AVIARY_LLM_MODEL`. The URL points to the Egress Proxy, and the key is the session token. |
| Approval | `AVIARY_APPROVAL=ask` if the agent must ask a person before it acts. Otherwise the variable is not set. |
| Persistence | The home is the session volume. It is kept while the session sleeps, and the next turn finds it in a new pod. The rest of the file system comes from the image, fresh in every pod. The harness keeps running between turns for as long as its pod does, or until the agent's model, approval or instructions are changed: the next turn then gets a new harness, which resumes the session. |
| Network | The hosts that the installation allows, through the Egress Proxy, which `HTTP_PROXY`, `HTTPS_PROXY` and their lowercase forms point to, with the session token as the password. The pod reaches nothing else and has no DNS. |

The runner talks to the harness over its standard input and output with the
[Agent Client Protocol](https://agentclientprotocol.com) (ACP):

- **Start.** The runner sends `initialize`, then `session/new`, or
  `session/resume` if the session already has a harness session, from this
  pod or an earlier one. The runner keeps the harness session ID in the home.
- **Turns.** For each turn, the runner sends `session/prompt` with the
  message as text. The turn ends when the prompt returns, and the harness
  stays for the next turn.
- **Events.** From `session/update` notifications, the runner reports the
  agent's message and thought text, tool calls and their updates, plans, and
  a title that the harness gives the session. It advertises
  `session.notices`, so that a harness can send warnings and other live
  information as notices rather than in the agent's message. The runner
  writes notices to its log and does not report them.
- **Cancellation.** To cancel a turn, the runner sends `session/cancel`. A
  harness that then ends the turn answers the prompt with the stop reason
  `cancelled`. A turn cancelled before its prompt is sent never reaches the
  harness.
- **Approval.** For an agent that asks, the runner passes each
  `session/request_permission` on with the options that the harness gave, and
  answers with the option that a person picks. The harness waits for as long
  as the turn runs. A request that is still open when the turn is cancelled
  is answered as cancelled. For any other agent, the runner allows the action
  once by itself.

## What the image must provide

1. **The command that starts the harness as an ACP agent**, in
   `AVIARY_HARNESS_COMMAND`. The runner runs it and speaks ACP on its
   standard input and output.
2. **What the sandbox runs:** `aviary-session`, `aviary-runner`, and the user
   `agent` with UID 10000 and GID 10000. An image built on
   `images/harness-base` has them. Everything the harness reads or executes
   must be usable by that user, and the harness must not need to write
   anywhere but the home.
3. **The harness's record of the conversation, in the home.** A harness that
   is started again, in the same pod or in a new pod of the session,
   continues its session from there.
4. **The model endpoint from the environment.** The image configures the
   harness to read `AVIARY_LLM_BASE_URL`, `AVIARY_LLM_API_KEY` and
   `AVIARY_LLM_MODEL`, and to send the key as a bearer token
   (`Authorization: Bearer ...`), which the Egress Proxy replaces with the
   provider's key.
5. **Support for `session/resume`.** The harness is started for the first
   turn of a session and stays for the turns after it. A harness that has
   exited, or that starts in a new pod of the session, resumes the harness
   session.
6. **The instructions file**, in `AVIARY_INSTRUCTIONS_FILE`: the file from
   which the harness reads instructions for every session, often named
   `AGENTS.md`. Before it starts the harness and before every turn, the
   runner writes the agent's instructions there, or empties the file if the
   agent has none, so the harness's user must be able to write it. An image
   that names no file runs only agents without instructions.
7. **Asking before acting if `AVIARY_APPROVAL` is `ask`.** The image then
   starts the harness so that it sends `session/request_permission` before
   actions that a person should approve first. Which actions is up to the
   image and the settings of its harness. An image that ignores the variable
   runs such an agent like any other: nobody is asked.

Environment variables that start with `AVIARY_` are reserved for Aviary. The
harness also sees the ones that the runner uses, such as
`AVIARY_MAX_TOOL_OUTPUT_BYTES`.

A harness may report the token usage of a turn as `usage` in its response to
`session/prompt`. Aviary records it with the turn as given. ACP marks this
field as unstable.

## What to watch for

A harness is usually written for one person at one machine, who starts it
and watches what it does. Aviary starts it for the first turn of a session,
in a home that was empty when the session began, with nobody watching. The
images in this repository handle these cases:

- **Work repeated for every new home.** With plugins on, Codex downloads its
  plugin catalog into each new session home, so the image turns plugins off.
  OpenCode fetches a list of models at startup, which the image also turns
  off.
- **Background work.** After the first turn of a session, the Codex ACP
  adapter has a model of its own choosing write a title for the session. It
  uses the default provider of the configuration, which is OpenAI if none is
  set. The image sets a default provider that reaches nothing, and gives each
  session the provider of the agent's model.
- **Configuration written next to state.** OpenCode installs packages into
  its configuration directory, so the image keeps the configuration in the
  image, at `/etc/xdg`, not in the session home.
- **The harness's own sandbox.** Codex's sandbox creates namespaces, which a
  pod does not allow. The pod is the sandbox, so the image runs Codex with
  full access.
- **Settings not read from the environment.** Codex takes the model and the
  server URL from its configuration, so the image starts it through a small
  script that puts the variables there.
- **Approval tied to the harness's sandbox.** Codex asks a person only when a
  command needs to leave its sandbox, and the sandbox cannot start in a pod.
  For an agent that asks, the image still runs Codex in read-only mode: a
  command that the model tries in the sandbox fails, and runs once the model
  asks to run it outside and a person allows that. The failed attempt costs
  one more model request.
- **Refusals.** Both harnesses here end the turn when a person refuses an
  action: OpenCode with the stop reason `end_turn`, Codex with `cancelled`.
  Aviary records the turn as the harness ends it.
- **How long "always" lasts.** An `allow_always` answer lasts as long as the
  harness remembers it. OpenCode forgets it when its process exits. Codex
  writes a rule into its directory in the session home, and reads it again
  when it is started again.
- **Startup time.** The harness starts with the first turn of a session, and
  again with the first turn after the session sleeps or after the agent's
  model, approval or instructions are changed, so its startup time adds to
  those turns.

## The images in this repository

| Image | Contents |
| --- | --- |
| `harness-base` | Node.js, git, Python with pip and venv, the `agent` user, and what the sandbox runs. The base of the other two. |
| `harness-opencode` | [OpenCode](https://opencode.ai), run as `opencode acp`. It uses the Chat Completions API. For an agent that asks, it asks before it runs a command or changes a file. |
| `harness-codex` | [Codex](https://github.com/openai/codex) through its ACP adapter, `codex-acp`. It uses the Responses API. For an agent that asks, it runs in read-only mode and asks before a command runs outside its sandbox. |

## Adding a harness

1. Write a Dockerfile that meets the seven requirements above. Building on
   `aviary-harness-base` covers the second.
2. Build the image and test it:

   ```sh
   make test-harness HARNESS_IMAGES=registry.example.com/aviary-harness-myharness:1.0
   ```

   The tests need no cluster. They run sessions in containers of the image,
   set up like the sandbox pods: as the agent's user, without capabilities,
   with a volume as the home. They send turns to the runner in the container
   and use a mock model server that speaks the Chat Completions and Responses
   APIs. The tests have the agent run shell commands, so the harness must
   offer the model a tool with a `command` or `cmd` parameter. They fail a
   harness that fetches anything for itself at startup, or that runs a
   command of an agent that asks without asking. The containers reach nothing
   but the mock server, which is also their proxy in the standard variables,
   so the tests see what a harness fetches. git, npm, pip and curl use those
   variables, and Node's `fetch` does with `NODE_USE_ENV_PROXY=1`, which
   `images/harness-base` sets.
3. Make the image available to the cluster. For the local cluster, run
   `kind load docker-image --name aviary <image>`.
4. Add the harness to the chart values under a name of your choice:

   ```yaml
   runtime:
     harnesses:
       myharness:
         image: registry.example.com/aviary-harness-myharness:1.0
         # Requests and limits of the harness container.
         resources:
           requests: {cpu: 250m, memory: 512Mi}
           limits: {memory: 4Gi}
   ```

5. Create an agent with that name: `{"harness": "myharness"}`.
