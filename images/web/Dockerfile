FROM node:26.13.1-trixie-slim@sha256:e6bedef217677b276c591795e169840edd9096dbf13f29df8ef8d1856b8820bc AS build
WORKDIR /src
COPY web/package.json web/package-lock.json web/.npmrc ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web .
RUN npm run build

# Serves the files of the web UI. The entrypoint of the image writes the
# configuration of the server and the settings of the web UI from the
# templates and the environment, when the container starts.
FROM nginxinc/nginx-unprivileged:1.30.6-alpine-slim@sha256:5d8bb9437d74a600ec5dbf90c035451c724dc33eac8d3a71078f6f8612c345f6
# The API that the server passes the requests of /v1 on to, such as
# http://aviary-supervisor.aviary-system. If empty, the server serves only
# the web UI: something in front of it routes /v1 to the API, or the browser
# calls the API at AVIARY_PUBLIC_API.
ENV AVIARY_API=""
# The address at which the browser calls the API, if it is not the address
# of the web UI, such as https://api.example.com. The API has to allow the
# origin of the web UI.
ENV AVIARY_PUBLIC_API=""
# The OpenID Connect issuer and the client that the web UI signs callers in
# with, and the scopes that it asks for, separated by spaces: if empty,
# "openid profile email". Without an issuer nobody signs in.
ENV AVIARY_OIDC_ISSUER="" AVIARY_OIDC_CLIENT_ID="" AVIARY_OIDC_SCOPES=""
COPY --chmod=755 images/web/19-api.envsh /docker-entrypoint.d/
COPY images/web/default.conf.template images/web/config.json.template /etc/nginx/templates/
COPY --from=build /src/dist /usr/share/nginx/html
