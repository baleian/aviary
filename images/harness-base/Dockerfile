ARG RUNNER_IMAGE=aviary-runner:dev
FROM ${RUNNER_IMAGE} AS runner

FROM node:26.13.1-trixie-slim@sha256:e6bedef217677b276c591795e169840edd9096dbf13f29df8ef8d1856b8820bc
# Besides Node.js, which the harnesses run on, the agent has git and Python,
# with pip and venv.
RUN apt-get update && \
    apt-get install --yes --no-install-recommends ca-certificates git python3 python3-pip python3-venv && \
    rm -rf /var/lib/apt/lists/*
# What a harness fetches goes out through a proxy if it is given one in the
# usual variables. Node only reads those if it is told to.
ENV NODE_USE_ENV_PROXY=1
# The agent runs as this user, and not as root, with the home of its session
# at /home. The user that the Node.js image has is nobody here, and its home
# is not to be in that of the session.
RUN userdel --remove node && \
    groupadd --gid 10000 agent && \
    useradd --uid 10000 --gid 10000 --home-dir /home --no-create-home agent && \
    install --owner agent --group agent --directory /home /home/workspace
# The sandbox of a session runs aviary-session, which starts the runner.
COPY --from=runner /aviary-runner /usr/local/bin/aviary-runner
COPY --chmod=755 aviary-session /usr/local/bin/aviary-session
