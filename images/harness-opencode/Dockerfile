FROM aviary-harness-base:dev
RUN apt-get update && \
    apt-get install --yes --no-install-recommends ripgrep && \
    rm -rf /var/lib/apt/lists/*
ARG NPM_CONFIG_REGISTRY
RUN --mount=type=cache,target=/root/.npm \
    npm install --global opencode-ai@1.19.5
COPY opencode.json /etc/xdg/opencode/opencode.json
COPY --chmod=755 opencode-acp-aviary /usr/local/bin/opencode-acp-aviary
# OpenCode reads instructions for every session from this file. The runner
# writes it, so the agent's user owns the file, though not the directory.
RUN install --owner 10000 --group 10000 --mode 644 /dev/null /etc/xdg/opencode/AGENTS.md
# OpenCode installs packages into its configuration directory if it can write
# to it, so the configuration is kept in the image, not in the session home.
# It titles a call of a tool of an MCP server "server_tool".
ENV XDG_CONFIG_HOME=/etc/xdg \
    OPENCODE_DISABLE_MODELS_FETCH=1 \
    AVIARY_HARNESS_COMMAND=opencode-acp-aviary \
    AVIARY_INSTRUCTIONS_FILE=/etc/xdg/opencode/AGENTS.md \
    AVIARY_MCP_TOOL_TITLE="{server}_{tool}"
