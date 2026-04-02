#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${1:-$SCRIPT_DIR/demo.env}"
COMPOSE_FILE="$SCRIPT_DIR/demo-compose.yml"
STATE_DIR_DEFAULT="$SCRIPT_DIR/state"
SIGNOZ_PORT_DEFAULT="8090"
SIGNOZ_ADMIN_PASSWORD_DEFAULT="SignozAdmin123!"

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

SIGNOZ_RENDERED_COMPOSE="$STATE_DIR/signoz.docker-compose.rendered.yaml"

render_signoz_compose() {
  perl -0pe 's/- "8080:8080" # signoz port/- "'"${SIGNOZ_PORT:-8090}"':8080" # signoz port/' \
    "$SIGNOZ_DIR/deploy/docker/docker-compose.yaml" > "$SIGNOZ_RENDERED_COMPOSE"
}

render_signoz_compose

docker compose -f "$SIGNOZ_RENDERED_COMPOSE" up -d
docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" up --build -d

SIGNOZ_ADMIN_NAME="$(awk -F= '/^DEMO_SIGNOZ_ADMIN_NAME=/{print substr($0, index($0,$2))}' "$ENV_FILE" | tail -n1)"
SIGNOZ_ADMIN_EMAIL="$(awk -F= '/^DEMO_SIGNOZ_ADMIN_EMAIL=/{print $2}' "$ENV_FILE" | tail -n1)"
SIGNOZ_ADMIN_PASSWORD="$(awk -F= '/^DEMO_SIGNOZ_ADMIN_PASSWORD=/{print $2}' "$ENV_FILE" | tail -n1)"
SIGNOZ_PORT="$(awk -F= '/^DEMO_SIGNOZ_PORT=/{print $2}' "$ENV_FILE" | tail -n1)"
SIGNOZ_ADMIN_NAME="${SIGNOZ_ADMIN_NAME:-Local Admin}"
SIGNOZ_ADMIN_EMAIL="${SIGNOZ_ADMIN_EMAIL:-admin@local.test}"
SIGNOZ_ADMIN_PASSWORD="${SIGNOZ_ADMIN_PASSWORD:-$SIGNOZ_ADMIN_PASSWORD_DEFAULT}"
SIGNOZ_PORT="${SIGNOZ_PORT:-$SIGNOZ_PORT_DEFAULT}"

wait_for_signoz() {
  local attempts="${1:-60}"
  local i
  for ((i=1; i<=attempts; i++)); do
    if curl -fsS "http://localhost:${SIGNOZ_PORT}/api/v2/healthz" >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done
  echo "timed out waiting for SigNoz UI on http://localhost:${SIGNOZ_PORT}" >&2
  return 1
}

signoz_access_token() {
  local context_json org_id login_json
  context_json="$(curl -fsS -G "http://localhost:${SIGNOZ_PORT}/api/v2/sessions/context" \
    --data-urlencode "email=$SIGNOZ_ADMIN_EMAIL" \
    --data-urlencode "ref=http://localhost:${SIGNOZ_PORT}")" || return 1
  org_id="$(printf '%s' "$context_json" | python3 -c 'import json,sys; data=json.load(sys.stdin); orgs=((data.get("data") or {}).get("orgs") or []); print(orgs[0]["id"] if orgs else "", end="")')" || return 1
  [[ -n "$org_id" ]] || return 1
  login_json="$(curl -fsS "http://localhost:${SIGNOZ_PORT}/api/v2/sessions/email_password" \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"$SIGNOZ_ADMIN_EMAIL\",\"password\":\"$SIGNOZ_ADMIN_PASSWORD\",\"orgId\":\"$org_id\"}")" || return 1
  printf '%s' "$login_json" | python3 -c 'import json,sys; data=json.load(sys.stdin); print(((data.get("data") or {}).get("accessToken")) or "", end="")'
}

bootstrap_signoz_admin() {
  local register_status register_body access_token
  if access_token="$(signoz_access_token)" && [[ -n "$access_token" ]]; then
    return 0
  fi

  register_body="$(mktemp)"
  register_status="$(curl -sS -o "$register_body" -w "%{http_code}" "http://localhost:${SIGNOZ_PORT}/api/v1/register" \
    -H "Content-Type: application/json" \
    -d "{\"name\":\"$SIGNOZ_ADMIN_NAME\",\"orgName\":\"\",\"email\":\"$SIGNOZ_ADMIN_EMAIL\",\"password\":\"$SIGNOZ_ADMIN_PASSWORD\"}")"

  if [[ "$register_status" == "200" ]]; then
    rm -f "$register_body"
    return 0
  fi

  rm -f "$register_body"

  if access_token="$(signoz_access_token)" && [[ -n "$access_token" ]]; then
    return 0
  fi

  echo "failed to bootstrap or authenticate the configured SigNoz admin user $SIGNOZ_ADMIN_EMAIL" >&2
  echo "if SigNoz was previously initialized with different credentials, either update demo.env or remove its persisted sqlite volume" >&2
  return 1
}

wait_for_signoz
bootstrap_signoz_admin

cat <<EOF

Local hub demo stack is starting.

Persistent state:
  $STATE_DIR/postgres

Observability:
  SigNoz UI: http://localhost:$SIGNOZ_PORT
  SigNoz login: $SIGNOZ_ADMIN_EMAIL / $SIGNOZ_ADMIN_PASSWORD
  OTLP gRPC: localhost:4317
  OTLP HTTP: localhost:4318

Useful commands:
  docker compose -f "$SIGNOZ_RENDERED_COMPOSE" ps
  docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" ps
  docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" logs -f hub
  "$SCRIPT_DIR/fetch_demo_token.sh" "$ENV_FILE"
  "$SCRIPT_DIR/stop_demo.sh" "$ENV_FILE"

Default Dex users:
  admin@example.com / testpass123
  reader@example.com / testpass123
  owner@example.com / testpass123
EOF
