FROM python:3.12-slim

WORKDIR /app

# System deps
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Install Python deps (cached layer — only re-runs when pyproject.toml changes)
COPY pyproject.toml /app/
RUN pip install --no-cache-dir -e ".[dev]"

# Copy application code (overridden by volume mount in dev)
COPY . /app/

COPY entrypoint.sh /app/
RUN chmod +x /app/entrypoint.sh

EXPOSE 8000

CMD ["/app/entrypoint.sh"]
