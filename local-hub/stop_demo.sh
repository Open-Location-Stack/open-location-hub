#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${1:-$SCRIPT_DIR/demo.env}"
SIGNOZ_DIR="$(awk -F= '/^DEMO_SIGNOZ_DIR=/{print $2}' "$ENV_FILE" | tail -n1)"
SIGNOZ_DIR="${SIGNOZ_DIR:-${DEMO_SIGNOZ_DIR:-$SCRIPT_DIR/state/signoz}}"
STATE_DIR="$(awk -F= '/^DEMO_STATE_DIR=/{print $2}' "$ENV_FILE" | tail -n1)"
STATE_DIR="${STATE_DIR:-$SCRIPT_DIR/state}"
if [[ "$SIGNOZ_DIR" != /* ]]; then
  SIGNOZ_DIR="$SCRIPT_DIR/${SIGNOZ_DIR#./}"
fi
if [[ "$STATE_DIR" != /* ]]; then
  STATE_DIR="$SCRIPT_DIR/${STATE_DIR#./}"
fi

SIGNOZ_RENDERED_COMPOSE="$STATE_DIR/signoz.docker-compose.rendered.yaml"

docker compose -f "$SCRIPT_DIR/demo-compose.yml" --env-file "$ENV_FILE" down
if [[ -f "$SIGNOZ_RENDERED_COMPOSE" ]]; then
  docker compose -p signoz -f "$SIGNOZ_RENDERED_COMPOSE" down
elif [[ -f "$SIGNOZ_DIR/deploy/docker/docker-compose.yaml" ]]; then
  docker compose -p signoz -f "$SIGNOZ_DIR/deploy/docker/docker-compose.yaml" down
fi
