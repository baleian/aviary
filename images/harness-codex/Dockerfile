FROM aviary-harness-base:dev
RUN apt-get update && \
    apt-get install --yes --no-install-recommends jq && \
    rm -rf /var/lib/apt/lists/*
RUN --mount=type=cache,target=/root/.npm \
    npm install --global @agentclientprotocol/codex-acp@2.6.0
COPY config.toml /etc/codex/config.toml
COPY --chmod=755 codex-acp-aviary /usr/local/bin/codex-acp-aviary
# The sandbox of the session is what keeps the agent in, so Codex has full
# access in it. Codex reads instructions for every session from AGENTS.md in
# its own directory, which is in the session home.
ENV INITIAL_AGENT_MODE=agent-full-access \
    AVIARY_HARNESS_COMMAND=codex-acp-aviary \
    AVIARY_INSTRUCTIONS_FILE=/home/.codex/AGENTS.md
