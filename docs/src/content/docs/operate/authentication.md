---
title: Authentication
description: How an installation names its identity provider, and who may use the API.
---

Aviary does not sign users in or store passwords. The installation names an
identity provider in `supervisor.auth.provider`, and a caller sends a token
of that provider with every request. A request without a token that the
provider issued for this installation returns 401. A caller becomes a user
of the installation with its first request. `GET /v1/me` returns the user's
ID, what the installation read from the token, and whether the caller is an
administrator: `{"id": "...", "subject": "...", "username": "...",
"display_name": "...", "groups": [...], "admin": false}`.

The groups that the provider reports for a caller decide what the caller
may do. Aviary has no built-in groups: the installation names groups of its
provider in its configuration. Members of the groups in
`supervisor.auth.admins` are administrators. Members of the groups in
`supervisor.auth.users` may use the API as well, and if no group is listed
there, every user that the provider authenticates may. Other callers get
403, but can still call `GET /v1/me`. Without an identity provider, every
caller is an administrator.

What a caller may do in a project is decided there: see
[Projects and members](../use/projects.md).

## Identity providers

A provider of the kind `oidc` is an OpenID Connect issuer:

```yaml
supervisor:
  auth:
    provider:
      kind: oidc
      settings:
        issuer: https://login.example.com/realms/main
        audience: aviary
        claims: {subject: sub, username: email, displayName: name, groups: groups}
```

- **`issuer`** is the URL under which the provider publishes its
  configuration and its signing keys. A Supervisor reads the configuration
  when it starts, and does not start if it cannot.
- **`audience`** is the audience that a token must be issued for. Tokens
  are JWTs, ID tokens or access tokens, signed by the issuer for that
  audience and not expired.
- **`claims`** names the claims that identify the caller. `subject` is the
  caller's permanent ID at the provider, `sub` by default. `username` is a
  human-readable name that no other caller has, and a caller has one only if
  the settings name its claim. `displayName` defaults to `name`, and
  `groups` to `groups`. A claim inside another is named by its path, such
  as `realm_access.roles`. An email address is accepted as a username only
  if the token has `"email_verified": true`.

The web UI signs callers in at the same issuer: see
[Signing in](./web-ui.md#signing-in).

## Try it locally

To try it locally, `make keycloak` installs Keycloak in dev mode in
the local cluster, with the realm in `hack/kind/keycloak.yaml`. The realm
has four users, whose passwords are their names: `alice` is an
administrator, `bob` and `carol` may use the API, `carol` is also in the
group `team-a`, and `dave` is in no group. Aviary uses this provider when it
is installed with `make deploy VALUES=hack/kind/aviary-keycloak-values.yaml`.
The provider is at http://localhost:8081. A caller gets a token from it and
sends it:

```sh
token=$(curl -s localhost:8081/realms/aviary/protocol/openid-connect/token \
  -d grant_type=password -d client_id=aviary -d username=alice -d password=alice | jq -r .access_token)
curl -s -H "Authorization: Bearer $token" localhost:8080/v1/me
```
