FROM python:3.12-slim

COPY --from=ghcr.io/astral-sh/uv:0.7 /uv /usr/local/bin/uv

WORKDIR /workspace

COPY shared/pyproject.toml shared/pyproject.toml
COPY shared/aviary_shared/ shared/aviary_shared/

COPY agent-supervisor/pyproject.toml agent-supervisor/pyproject.toml
COPY agent-supervisor/app/ agent-supervisor/app/

ARG UV_INDEX_URL
ENV UV_LINK_MODE=copy

RUN cd shared && uv pip install --system . \
    && cd /workspace/agent-supervisor && uv pip install --system .

WORKDIR /workspace/agent-supervisor

EXPOSE 9000

CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "9000"]
