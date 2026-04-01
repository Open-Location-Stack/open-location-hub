#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${1:-$SCRIPT_DIR/demo.env}"

docker compose -f "$SCRIPT_DIR/demo-compose.yml" --env-file "$ENV_FILE" down
