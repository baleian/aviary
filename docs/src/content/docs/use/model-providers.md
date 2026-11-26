---
title: Model providers
description: How a project connects its agents to model endpoints, and what happens to the keys.
---

A model provider is the base URL of a model endpoint and its API key: a
provider's API, an AI gateway or your own model server. A project registers
its providers, and each agent names its model at one of them.

The turns of an agent send their model requests to the endpoint of the
agent's provider, for the agent's model.


## Adding a provider

In the web UI, **Add provider** under **Model providers** asks for a name,
the URL and the key. The key is stored in Aviary, read from a
[secret store](./secret-stores.md) of the project, or left out for
an endpoint that takes none. The headers that an endpoint needs besides its
key are under **Advanced**. **Test connection** lists the models of the
endpoint before the provider is saved. With the API, see
[Model providers](../reference/api/connections.md#model-providers).

![The form of a new model provider](../../../assets/screens/add-provider.png)

Which API an endpoint must serve depends on the harness of the agents that
use it: see [the harness images](../operate/harness-images.md#the-images-in-this-repository).

## How providers behave

- **Secrets are write-only.** A key, or the secret of a header, is stored
  encrypted with a key of the installation, which the database does not
  hold. A provider is returned with `{"stored": true}` in its place, as
  `"api_key": {"stored": true}`.
- **A stored secret stays with its URL.** A provider that is replaced keeps
  a secret that is given as `{"stored": true}`, but only if its URL stays
  the same: a secret goes to no endpoint that it was not given for.
- **A secret can stay in a secret store of the project.** Aviary then
  stores where the secret is, not the secret, and reads it from the store
  when a model request needs it. See [Secret stores](./secret-stores.md).
- **A key goes only to its host.** The harness of a turn that uses the
  provider sends its model requests to the provider's URL with a
  placeholder, and the egress gateway puts the key in its place on their
  way out, in every request to the host of the URL. A request to another
  host carries no key. See [Network and egress](../operate/egress.md#secrets).
- **An endpoint can need headers besides its key**, as a gateway does that
  routes its requests by a header. A provider has up to 20 of them, as
  `"headers": [{"name": "x-gateway-route", "value": "team-a"}]`, and the
  harness sends them with its model requests. A header that says where a
  request goes or how it is carried, such as `host`, cannot be one of them,
  nor can `authorization`, which carries the key. A value that is a secret is
  given as a key is, as `{"name": "x-team-token", "secret": {"value": "..."}}`
  or by its location in a secret store, and the egress gateway puts it in.
  Other values are not secrets: a provider is returned with them.
- **A provider can be replaced while turns use it.** Their next model
  request gets the new key and secrets, and the next turn the new URL and
  headers.
- **The models of a provider can be listed**, before it is registered and
  after. The Supervisor asks its endpoint itself, with its key and headers,
  so an endpoint that it does not reach, or a key that the endpoint refuses,
  shows at once and not as a failed turn. An endpoint that has no list of
  models answers 404, and was reached all the same. The Supervisor does not
  connect to the addresses of its own host or of its link.
- **The host of a provider is a name**, which the sandboxes of its turns may
  reach whatever `sandboxes.allowedHosts` says. A provider at an IP address
  is not reached, and a Service of the cluster needs its full name, such as
  `models.llm.svc.cluster.local`.
- **A provider at `localhost` is not reached** unless `sandboxes.localhost`
  names the host that stands in for it. The sandboxes and the Supervisor
  then reach that host, on the port of the provider's URL.
