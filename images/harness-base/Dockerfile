ARG RUNNER_IMAGE=aviary-runner:dev
FROM ${RUNNER_IMAGE} AS runner

FROM node:26.13.1-trixie-slim@sha256:e6bedef217677b276c591795e169840edd9096dbf13f29df8ef8d1856b8820bc
RUN apt-get update && \
    apt-get install --yes --no-install-recommends ca-certificates git python3 python3-pip python3-venv && \
    rm -rf /var/lib/apt/lists/*
# Node uses the proxy in HTTP_PROXY and HTTPS_PROXY only if this is set.
ENV NODE_USE_ENV_PROXY=1
# The agent's user, whose home is the session volume at /home. The node user
# is removed, since its home would be inside the session home.
RUN userdel --remove node && \
    groupadd --gid 10000 agent && \
    useradd --uid 10000 --gid 10000 --home-dir /home --no-create-home agent && \
    install --owner agent --group agent --directory /home /home/workspace
# The sandbox runs aviary-session, which starts the runner.
COPY --from=runner /aviary-runner /usr/local/bin/aviary-runner
COPY --chmod=755 aviary-session /usr/local/bin/aviary-session
