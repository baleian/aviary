---
title: Web UI
description: How the web UI is installed next to the API, and how it signs callers in.
---

The web UI is a client of the API that runs in the browser.
[The web UI](../use/web-ui.md) describes its screens.

The chart does not install it. Its image, built from `images/web`, is a web
server with the files of the web UI, configured through its environment:

| Variable | Description |
| --- | --- |
| `AVIARY_API` | The API that the server passes the requests of `/v1` on to, such as `http://aviary-supervisor.aviary-system`. Empty by default: the server then serves only the web UI. |
| `AVIARY_PUBLIC_API` | The address at which the browser calls the API, if it is not the address of the web UI, such as `https://api.example.com`. Empty by default. |
| `AVIARY_OIDC_ISSUER`, `AVIARY_OIDC_CLIENT_ID` | The OpenID Connect issuer and the client that the web UI signs callers in with. Empty by default: nobody signs in, as with an API that has no identity provider. |
| `AVIARY_OIDC_SCOPES` | The scopes that the web UI asks the issuer for, separated by spaces. Default `openid profile email`. |

There are three ways for the browser to find the API:

- **The web server in front of the API.** Set `AVIARY_API`, and expose the
  web server: the API is under `/v1` at the address of the web UI.
  `make web` installs the web UI like this in the local cluster, from
  `hack/kind/web.yaml`.
- **A proxy in front of both.** Leave `AVIARY_API` empty, and have a proxy or
  an ingress route `/v1/` to the Supervisor and every other path to the web
  server, at one address. The proxy must not buffer the responses of `/v1/`,
  which include event streams, and must take the files that messages carry:
  request bodies of up to `runtime.maxAttachmentBytes`.
- **Two addresses.** Set `AVIARY_PUBLIC_API` to the address of the API, and
  list the origin of the web UI in `supervisor.allowedOrigins` (see
  [Configuration](../reference/configuration.md)), so that browsers let its pages call the
  API there.

The server listens on port 8080 as a user that is not root. When it starts
it writes its configuration to `/etc/nginx/conf.d` and uses `/tmp`, so with
a read-only root file system both need a volume, as in `hack/kind/web.yaml`.

The image has the licenses of the packages that the web UI bundles in
`licenses.md`, which the server serves at `/licenses.md`.

## Signing in

With an identity provider of the kind `oidc`, the web UI signs callers in at
the issuer, with the authorization code flow and PKCE, and sends the ID
token with its requests. Register a public client for it at the issuer:

- Its client ID is the `audience` of the provider's settings.
- Its redirect URI is the address of the web UI followed by `/signin`.
- The issuer accepts requests from the origin of the web UI (CORS).
- Its ID tokens carry the claims that the settings name, such as the groups.

Browsers sign in only at an address that is served over HTTPS, or at
`localhost`. A token that has expired is renewed with the refresh token, if
the issuer gives one: some do only for the scope `offline_access`. Without a
refresh token the caller signs in again. Signing out forgets the tokens in
the browser, and does not end the session at the issuer.

In the local cluster, the web UI of `make web` signs in with the Keycloak of
`make keycloak`, at http://keycloak.localhost:8081, once Aviary is
installed with it (see [Identity providers](./authentication.md#identity-providers)).
