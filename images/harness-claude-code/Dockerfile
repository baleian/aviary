FROM aviary-harness-base:dev
ARG NPM_CONFIG_REGISTRY
RUN --mount=type=cache,target=/root/.npm \
    npm install --global @agentclientprotocol/claude-agent-acp@0.109.1
# Every session starts in the default permission mode, in which Claude Code
# asks before it acts. The runner allows the actions of an agent that does
# not ask. WebFetch does not ask api.anthropic.com about a host first: the
# installation decides which hosts a session reaches.
COPY managed-settings.json /etc/claude-code/managed-settings.json
COPY --chmod=755 claude-agent-acp-aviary /usr/local/bin/claude-agent-acp-aviary
# Without telemetry, error reports, update checks and feature flags. Claude
# Code reads instructions for every session from CLAUDE.md in its directory in
# the session home.
ENV CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 \
    AVIARY_HARNESS_COMMAND=claude-agent-acp-aviary \
    AVIARY_INSTRUCTIONS_FILE=/home/.claude/CLAUDE.md
