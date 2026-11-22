ARG DOCKER_IO=docker.io
ARG GCR_IO=gcr.io

FROM ${DOCKER_IO}/library/golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9 AS build
ARG GOPROXY
WORKDIR /src
COPY supervisor .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -o /aviary-supervisor .

# Provides what a static binary lacks: a non-root user, and the CA
# certificates for TLS connections to the database and the event log.
FROM ${GCR_IO}/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931
COPY --from=build /aviary-supervisor /aviary-supervisor
ENTRYPOINT ["/aviary-supervisor"]
