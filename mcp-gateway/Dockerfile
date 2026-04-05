FROM python:3.12-slim

COPY --from=ghcr.io/astral-sh/uv:0.7 /uv /usr/local/bin/uv

WORKDIR /workspace

RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Copy workspace layout for dependency resolution (cached layer)
COPY pyproject.toml uv.lock ./
COPY shared/pyproject.toml ./shared/pyproject.toml
COPY api/pyproject.toml ./api/pyproject.toml
COPY admin/pyproject.toml ./admin/pyproject.toml
COPY agent-supervisor/pyproject.toml ./agent-supervisor/pyproject.toml
COPY mcp-gateway/pyproject.toml ./mcp-gateway/pyproject.toml

# Shared source needed for workspace install
COPY shared/ ./shared/

# Install deps from lock file
ARG UV_INDEX_URL
ENV UV_LINK_MODE=copy
RUN uv sync --frozen --package aviary-mcp-gateway --no-dev --no-install-project

# Copy app source
COPY mcp-gateway/ ./mcp-gateway/
RUN uv sync --frozen --package aviary-mcp-gateway --no-dev

# Activate virtual environment via PATH
ENV PATH="/workspace/.venv/bin:$PATH"

WORKDIR /workspace/mcp-gateway

COPY mcp-gateway/entrypoint.sh /workspace/mcp-gateway/entrypoint.sh
RUN chmod +x /workspace/mcp-gateway/entrypoint.sh

EXPOSE 8100

CMD ["/workspace/mcp-gateway/entrypoint.sh"]
