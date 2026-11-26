FROM aviary-harness-base:dev
USER root
RUN apt-get update && \
    apt-get install --yes --no-install-recommends jq && \
    rm -rf /var/lib/apt/lists/*
ARG NPM_CONFIG_REGISTRY
RUN --mount=type=cache,target=/root/.npm \
    npm install --global @agentclientprotocol/codex-acp@2.6.0
COPY config.toml /etc/codex/config.toml
COPY --chmod=755 codex-acp-aviary /usr/local/bin/codex-acp-aviary
# Full access, since the session's sandbox is the sandbox: Codex's own
# creates namespaces, which the session's sandbox does not allow. Codex reads
# instructions for every session from AGENTS.md in its directory in the
# session home. Its adapter titles a call of a tool of an MCP server
# "mcp.server.tool".
ENV INITIAL_AGENT_MODE=agent-full-access \
    AVIARY_HARNESS_COMMAND=codex-acp-aviary \
    AVIARY_INSTRUCTIONS_FILE=/home/.codex/AGENTS.md \
    AVIARY_MCP_TOOL_TITLE="mcp.{server}.{tool}"
USER 10000:10000
