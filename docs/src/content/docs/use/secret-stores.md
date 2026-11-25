---
title: Secret stores
description: How a project keeps its keys in a store of its own, and gives Aviary their locations.
---

A project that keeps its secrets in a store of its own can register the
store, and then give the location of a secret in the store instead of the
secret itself. The key of a model provider, and the secret of a header of a
provider or an MCP server, are such secrets.

```sh
store=$(curl -s -X POST localhost:8080/v1/projects/$project/secret-stores \
  -d '{"name": "team store", "kind": "kv-v2", "credential": {"value": "..."},
       "settings": {"address": "https://secrets.example.com:8200", "mount": "secret", "role_id": "..."}}' | jq -r .id)
provider=$(curl -s -X POST localhost:8080/v1/projects/$project/providers \
  -d '{"name": "a model server", "url": "https://api.example.com/v1",
       "api_key": {"store": "'$store'", "path": "team/model", "key": "api_key"}}' | jq -r .id)
```

In the web UI, **Register store** under **Secret stores** asks for the same,
and the forms of a model provider and of an MCP server then take the
location of a secret in the store.

## How stores behave

- **The kind says how the store is accessed.** `kv-v2` is a store that
  serves the KV version 2 HTTP API and accepts AppRole logins, such as
  OpenBao. Its settings are its address, the mount of its secrets and the
  role ID, and its credential is the role's secret ID. A secret is at a path
  below the mount and holds values under keys. Another kind of store needs a
  [plugin](../operate/plugins.md).
- **The installation decides where stores may be.** The Supervisor connects
  to the stores itself, so a project can register a store only at a host
  that `supervisor.secretStores.allowedHosts` lists. By default it lists
  none.
- **Errors are reported at once.** The Supervisor logs in to a store before
  it accepts the store, and reads a secret from its location before it
  accepts the provider.
- **Values are read when they are used.** A Supervisor caches a value for
  `supervisor.secretStores.cacheSeconds`, 60 by default, and never uses it
  for longer: a secret that changes in the store reaches the model requests
  within that time, and a secret that is removed from the store stops being
  used within that time too. The cache is bounded in size, and a value
  larger than 8 KiB is not accepted as a secret.
- **A secret that cannot be read fails the request.** The model request
  gets a 503 with the reason, and the turns of other providers continue.
- **The credential is stored encrypted**, like the key of a provider. A
  store is returned with `"credential": {"stored": true}`.
- **Give the role no more access than Aviary needs.** Anyone who can create
  a provider in the project can have any value that the role can read sent
  to an endpoint of their choice.

## Try it locally

To try it locally, `make openbao` installs OpenBao in dev mode in the
local cluster, which keeps its data in memory. It is at
`http://openbao.aviary-data:8200`, a host that
`supervisor.secretStores.allowedHosts` must then list, and its root token is
in the secret `openbao-root` in the namespace `aviary-data`. `make vault`
installs Vault in the same way, at `http://vault.aviary-data:8200` with its
root token in `vault-root`.
