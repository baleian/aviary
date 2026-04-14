# --- Build stage: compile TypeScript ---
FROM node:22-slim AS build

ARG NPM_CONFIG_REGISTRY

WORKDIR /app
COPY package.json package-lock.json /app/
RUN npm ci

COPY tsconfig.json /app/
COPY src/ /app/src/
RUN npx tsc

# --- Runtime stage ---
FROM node:22-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    git curl ca-certificates bubblewrap \
    python3 python3-venv python3-pip python-is-python3 \
    && rm -rf /var/lib/apt/lists/* \
    && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
       -o /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
       > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y --no-install-recommends gh \
    && rm -rf /var/lib/apt/lists/*

# Install Claude Code CLI globally
ARG NPM_CONFIG_REGISTRY
RUN npm install -g @anthropic-ai/claude-code \
    && chmod +x /usr/local/lib/node_modules/@anthropic-ai/claude-code/vendor/ripgrep/*/rg

# Replace `claude` binary with bubblewrap sandbox wrapper
RUN CLAUDE_BIN=$(which claude) && \
    mv "$CLAUDE_BIN" "$(dirname "$CLAUDE_BIN")/claude-real" && \
    cp /dev/null "$CLAUDE_BIN"

WORKDIR /app
COPY package.json package-lock.json /app/
RUN npm ci --omit=dev

COPY --from=build /app/dist/ /app/dist/
COPY scripts/ /app/scripts/
COPY config/managed-settings.json /etc/claude-code/managed-settings.json

RUN chmod +x /app/scripts/sandbox.sh /app/scripts/claude-sandbox.sh /app/scripts/git-credential-github.sh

# Install the claude wrapper as the `claude` command (forwards to sandbox.sh + claude-real)
RUN CLAUDE_BIN=$(which claude-real) && \
    cp /app/scripts/claude-sandbox.sh "$(dirname "$CLAUDE_BIN")/claude" && \
    chmod +x "$(dirname "$CLAUDE_BIN")/claude"

# Pre-create the mount points owned by the runtime user. Docker copies the
# image contents (including ownership) into an empty named volume on first
# mount; setting this to node:node prevents root-owned subpaths later.
RUN mkdir -p /workspace /workspace-shared \
    && chown node:node /workspace /workspace-shared

RUN git config --system user.name "aviary" \
    && git config --system user.email "aviary-agent@aviary.com"

# Use built-in non-root `node` user (UID 1000)
RUN chown -R node:node /app
USER node

EXPOSE 3000

CMD ["node", "dist/server.js"]
