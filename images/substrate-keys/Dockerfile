# kubectl-ate of the Substrate release, with which the chart makes the keys
# of Substrate. The build context is the source of the release.
ARG DOCKER_IO=docker.io
ARG GCR_IO=gcr.io

FROM ${DOCKER_IO}/library/golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9 AS build
WORKDIR /src
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -mod=vendor -trimpath -o /kubectl-ate ./cmd/kubectl-ate

# The debug variant has a shell, in which the Job runs kubectl-ate.
FROM ${GCR_IO}/distroless/static-debian13:debug-nonroot@sha256:3e7f48d714a3298b4df1a69fbc8809dd6fadd16c306838862fffc2895b168158
COPY --from=build /kubectl-ate /usr/local/bin/kubectl-ate
