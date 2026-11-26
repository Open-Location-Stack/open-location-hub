#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
env_file="${1:-$script_dir/demo.env}"

docker compose -f "$script_dir/demo-compose.yml" --env-file "$env_file" down
