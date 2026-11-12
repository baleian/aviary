FROM aviary-harness-base:dev
RUN apt-get update && \
    apt-get install --yes --no-install-recommends jq && \
    rm -rf /var/lib/apt/lists/*
RUN --mount=type=cache,target=/root/.npm \
    npm install --global @agentclientprotocol/codex-acp@2.6.0
COPY config.toml /etc/codex/config.toml
COPY --chmod=755 codex-acp-aviary /usr/local/bin/codex-acp-aviary
# The pod is the sandbox. Codex's own sandbox needs to create namespaces,
# which a pod does not allow.
ENV INITIAL_AGENT_MODE=agent-full-access \
    AVIARY_HARNESS_COMMAND=codex-acp-aviary
