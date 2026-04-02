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

CONFIGURED_SIGNOZ_DIR="$(awk -F= '/^DEMO_SIGNOZ_DIR=/{print $2}' "$ENV_FILE" | tail -n1)"
CONFIGURED_SIGNOZ_REF="$(awk -F= '/^DEMO_SIGNOZ_REF=/{print $2}' "$ENV_FILE" | tail -n1)"
SIGNOZ_DIR="${SIGNOZ_DIR:-${DEMO_SIGNOZ_DIR:-$CONFIGURED_SIGNOZ_DIR}}"
SIGNOZ_REF="${SIGNOZ_REF:-${DEMO_SIGNOZ_REF:-$CONFIGURED_SIGNOZ_REF}}"
SIGNOZ_DIR="${SIGNOZ_DIR:-$STATE_DIR/signoz}"
SIGNOZ_REF="${SIGNOZ_REF:-v0.117.1}"
if [[ "$SIGNOZ_DIR" != /* ]]; then
  SIGNOZ_DIR="$SCRIPT_DIR/${SIGNOZ_DIR#./}"
fi

ensure_signoz_checkout() {
  local target_dir="$1"
  local ref="$2"
  local ref_file="$target_dir/.codex-signoz-ref"
  if [[ -d "$target_dir/.git" && -f "$ref_file" ]] && [[ "$(cat "$ref_file")" == "$ref" ]]; then
    return
  fi
  rm -rf "$target_dir.tmp"
  git clone --depth 1 --branch "$ref" https://github.com/SigNoz/signoz "$target_dir.tmp"
  printf '%s\n' "$ref" > "$target_dir.tmp/.codex-signoz-ref"
  rm -rf "$target_dir"
  mv "$target_dir.tmp" "$target_dir"
}

ensure_signoz_checkout "$SIGNOZ_DIR" "$SIGNOZ_REF"

docker compose -f "$SIGNOZ_DIR/deploy/docker/docker-compose.yaml" up -d
docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" up --build -d

cat <<EOF

Local hub demo stack is starting.

Persistent state:
  $STATE_DIR/postgres

Observability:
  SigNoz UI: http://localhost:8080
  OTLP gRPC: localhost:4317
  OTLP HTTP: localhost:4318

Useful commands:
  docker compose -f "$SIGNOZ_DIR/deploy/docker/docker-compose.yaml" ps
  docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" ps
  docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" logs -f hub
  "$SCRIPT_DIR/fetch_demo_token.sh" "$ENV_FILE"
  "$SCRIPT_DIR/stop_demo.sh" "$ENV_FILE"

Default Dex users:
  admin@example.com / testpass123
  reader@example.com / testpass123
  owner@example.com / testpass123
EOF
