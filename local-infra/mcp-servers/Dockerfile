FROM python:3.12-slim

RUN pip install --no-cache-dir \
    "mcp[cli]>=1.0.0" \
    "httpx>=0.28.0" \
    "markdown-it-py>=3.0.0"

WORKDIR /app

ARG MCP_SERVER_NAME
COPY ${MCP_SERVER_NAME}/ /app/

CMD ["python", "server.py"]
