#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${1:-$SCRIPT_DIR/demo.env}"
COMPOSE_FILE="$SCRIPT_DIR/demo-compose.yml"
STATE_DIR_DEFAULT="$SCRIPT_DIR/state"

if [[ ! -f "$ENV_FILE" ]]; then
  cp "$SCRIPT_DIR/demo.env.example" "$ENV_FILE"
  echo "created $ENV_FILE from demo.env.example"
fi

STATE_DIR="$(awk -F= '/^DEMO_STATE_DIR=/{print $2}' "$ENV_FILE" | tail -n1)"
STATE_DIR="${STATE_DIR:-$STATE_DIR_DEFAULT}"
if [[ "$STATE_DIR" != /* ]]; then
  STATE_DIR="$SCRIPT_DIR/${STATE_DIR#./}"
fi

mkdir -p "$STATE_DIR/postgres"

docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" up --build -d

cat <<EOF

Local hub demo stack is starting.

Persistent state:
  $STATE_DIR/postgres

Useful commands:
  docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" ps
  docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" logs -f hub
  "$SCRIPT_DIR/fetch_demo_token.sh" "$ENV_FILE"
  "$SCRIPT_DIR/stop_demo.sh" "$ENV_FILE"

Default Dex users:
  admin@example.com / testpass123
  reader@example.com / testpass123
  owner@example.com / testpass123
EOF
