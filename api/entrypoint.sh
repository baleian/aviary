#!/usr/bin/env bash
set -e

# Ensure app package is importable (bind mount overrides the editable install)
export PYTHONPATH=/app

echo "Running database migrations..."
alembic upgrade head

echo "Starting API server..."
exec uvicorn app.main:app --host 0.0.0.0 --port 8000 --reload
