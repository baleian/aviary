FROM node:26.13.1-trixie-slim@sha256:e6bedef217677b276c591795e169840edd9096dbf13f29df8ef8d1856b8820bc
RUN apt-get update && \
    apt-get install --yes --no-install-recommends ca-certificates git && \
    rm -rf /var/lib/apt/lists/*
# Turn pods reach the outside through a proxy that they are given in the
# usual variables. Node only reads those if it is told to.
ENV NODE_USE_ENV_PROXY=1
# Turn pods run as this user, with the session home mounted at /home.
RUN groupadd --gid 10000 agent && \
    useradd --uid 10000 --gid 10000 --home-dir /home --no-create-home agent
