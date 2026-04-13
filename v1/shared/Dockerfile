FROM python:3.12-slim

COPY --from=ghcr.io/astral-sh/uv:0.7 /uv /usr/local/bin/uv

WORKDIR /workspace/shared

COPY pyproject.toml ./
COPY aviary_shared/ ./aviary_shared/
COPY alembic.ini ./

ARG UV_INDEX_URL
ENV UV_LINK_MODE=copy
RUN uv pip install --system .

CMD ["alembic", "upgrade", "head"]
