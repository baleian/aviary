#!/usr/bin/env bash
set -e

echo "Starting agent-supervisor..."
exec uvicorn app.main:app --host 0.0.0.0 --port 9000 --reload --no-access-log
