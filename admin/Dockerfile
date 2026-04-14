FROM python:3.12-slim

COPY --from=ghcr.io/astral-sh/uv:0.7 /uv /usr/local/bin/uv

WORKDIR /workspace

RUN apt-get update && apt-get install -y --no-install-recommends curl \
    && rm -rf /var/lib/apt/lists/*

COPY shared/pyproject.toml shared/pyproject.toml
COPY shared/aviary_shared/ shared/aviary_shared/

COPY admin/pyproject.toml admin/pyproject.toml
COPY admin/app/ admin/app/

ARG UV_INDEX_URL
ENV UV_LINK_MODE=copy

RUN cd shared && uv pip install --system . \
    && cd /workspace/admin && uv pip install --system .

WORKDIR /workspace/admin

EXPOSE 8001

CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8001", "--reload", "--no-access-log"]
