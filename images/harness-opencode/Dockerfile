FROM aviary-harness-base:dev
RUN apt-get update && \
    apt-get install --yes --no-install-recommends ripgrep && \
    rm -rf /var/lib/apt/lists/*
RUN --mount=type=cache,target=/root/.npm \
    npm install --global opencode-ai@1.19.5
COPY opencode.json /etc/xdg/opencode/opencode.json
# OpenCode reads instructions for every session from this file. The runner
# writes it, so the file belongs to the user of the harness. The directory
# does not: see below.
RUN install --owner 10000 --group 10000 --mode 644 /dev/null /etc/xdg/opencode/AGENTS.md
# OpenCode installs packages into a config directory it can write to, so its
# configuration lives in the image instead of the session home.
ENV XDG_CONFIG_HOME=/etc/xdg \
    OPENCODE_DISABLE_MODELS_FETCH=1 \
    AVIARY_HARNESS_COMMAND="opencode acp" \
    AVIARY_INSTRUCTIONS_FILE=/etc/xdg/opencode/AGENTS.md
