ARG DOCKER_IO=docker.io
ARG RUNNER_IMAGE=aviary-runner:dev

FROM ${RUNNER_IMAGE} AS runner

FROM ${DOCKER_IO}/library/node:26.13.1-trixie-slim@sha256:e6bedef217677b276c591795e169840edd9096dbf13f29df8ef8d1856b8820bc
# DEB_DEBIAN_ORG replaces the Debian archive with a mirror, also for the
# images built from this one. The image has no CA certificates until this
# step installs them, so apt cannot verify the certificate of a mirror here;
# it still verifies the signatures of the packages.
ARG DEB_DEBIAN_ORG=http://deb.debian.org
RUN sed --in-place "s|http://deb.debian.org|$DEB_DEBIAN_ORG|" /etc/apt/sources.list.d/debian.sources && \
    apt-get --option Acquire::https::Verify-Peer=false update && \
    apt-get --option Acquire::https::Verify-Peer=false install --yes --no-install-recommends \
        ca-certificates git python3 python3-pip python3-venv && \
    rm -rf /var/lib/apt/lists/*
# The agent's user, whose home is the session volume at /home. The node user
# is removed, since its home would be inside the session home.
RUN userdel --remove node && \
    groupadd --gid 10000 agent && \
    useradd --uid 10000 --gid 10000 --home-dir /home --no-create-home agent && \
    install --owner agent --group agent --directory /home /home/workspace
# The sandbox runs aviary-session, which starts the runner.
COPY --from=runner /aviary-runner /usr/local/bin/aviary-runner
COPY --chmod=755 aviary-session /usr/local/bin/aviary-session
# The sandbox runs as the user of its image. An image built from this one
# installs as root and ends with this line again.
USER 10000:10000
