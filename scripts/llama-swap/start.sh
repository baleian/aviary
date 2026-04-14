#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
config="$script_dir/config.yaml"

if [ -f "$script_dir/.env" ]; then
  set -a
  source "$script_dir/.env"
  set +a
fi

export HOME=$(cd ~ && pwd)
exec llama-swap -config "$config" -listen ":9292"
