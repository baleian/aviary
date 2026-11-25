---
title: Quick start
description: Run Aviary in a local cluster, and give an agent its first message.
---

This runs Aviary on your machine, in a Kubernetes cluster that
[kind](https://kind.sigs.k8s.io) creates in Docker.

## Before you begin

You need Docker, [kind](https://kind.sigs.k8s.io), kubectl, Helm, jq, make,
curl, openssl and bash. Tested with kind 0.33, Helm 4.3 and Docker 29 on
Linux.

You also need a model endpoint: a provider's API, an AI gateway or a model
server of your own.

## Start Aviary

```sh
make cluster-up        # create a kind cluster
make postgres valkey   # install a database and an event log in it
make images            # build the images and load them into the cluster
make deploy            # install Aviary with its Helm chart
make web               # build the web UI and install it
```

The web UI is then at http://localhost:8082, and the API at
http://localhost:8080. In a network that does not reach the public
registries, see [Mirrors](../operate/mirrors.md).

## Connect a model

A model provider is the base URL of a model endpoint and its API key.
OpenCode needs an endpoint with the OpenAI Chat Completions API, Codex one
with the Responses API, and Claude Code one with the Anthropic Messages API,
which it calls at `v1/messages` under the base URL, or at `messages` if the
URL ends in `/v1`.

The endpoint must be reachable from the cluster. A model server on your own
machine is: register it at `localhost`, as in `http://localhost:11434/v1`.
`make deploy` names the machine in `egressProxy.localhost` and lets the
Egress Proxy connect to it with `egressProxy.extraEgress` (see
[Configuration](../reference/configuration.md)), so the sandboxes reach the machine through
the proxy as well. A values file of your own sets the two otherwise. With
Docker Engine on Linux the model server has to listen on more than
`127.0.0.1`, which the containers of Docker do not reach there. Another
endpoint at a private address needs an entry in `egressProxy.extraEgress`.

## Send a message

In the web UI at http://localhost:8082:

1. Create a project.
2. Under **Model providers**, add your endpoint with its URL and its key.
   **Test connection** shows whether Aviary reaches it.
3. Under **Agents**, create an agent: choose a harness and a model of the
   provider.
4. Open the agent, start a session and send it a message, such as "Create a
   file named hello.txt that says hello."

The conversation shows what the agent does as it works. Send the session
another message: the agent still knows the conversation and finds the file.
[The web UI](../use/web-ui.md) describes the screens.

## Or use the API

```sh
project=$(curl -s -X POST localhost:8080/v1/projects -d '{"slug": "demo"}' | jq -r .id)
provider=$(curl -s -X POST localhost:8080/v1/projects/$project/providers \
  -d '{"name": "a model server", "url": "https://model-server.example.com/v1", "api_key": {"value": "..."}}' | jq -r .id)
agent=$(curl -s -X POST localhost:8080/v1/projects/$project/agents \
  -d '{"slug": "assistant", "harness": "opencode", "model": {"provider": "'$provider'", "name": "..."}}' | jq -r .id)
session=$(curl -s -X POST localhost:8080/v1/agents/$agent/sessions | jq -r .id)

turn=$(curl -s -X POST localhost:8080/v1/sessions/$session/messages \
  -d '{"text": "Create a file named hello.txt that says hello."}' | jq -r .id)
curl -N localhost:8080/v1/turns/$turn/events
```

The last command prints the events of the turn as they happen, and returns
when the turn has ended. Send the session another message: the agent still
knows the conversation and finds the file.

[API overview](../reference/api/overview.md) lists the requests.

## Stop

`make cluster-down` deletes the cluster, including the session volumes,
which are directories on its nodes.

## Next

- [Concepts](./concepts.md): the words used here.
- [Development environment](../contribute/development.md): more of the
  local cluster, such as another database, an identity provider and
  dashboards.
- [Installation](../operate/installation.md): what a cluster of your own
  needs.
