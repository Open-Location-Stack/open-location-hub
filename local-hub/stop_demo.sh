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

signoz_compose_down() {
  local project="$1"
  shift
  local files=("$@")
  local args=()
  local file
  for file in "${files[@]}"; do
    [[ -n "$file" && -f "$file" ]] || continue
    args+=(-f "$file")
  done
  [[ "${#args[@]}" -gt 0 ]] || return 1
  docker compose -p "$project" "${args[@]}" down
}

signoz_discovered_compose_down() {
  local container compose_project config_files args=()
  for container in signoz-otel-collector signoz-clickhouse signoz-zookeeper-1; do
    if ! docker inspect --type container "$container" >/dev/null 2>&1; then
      continue
    fi
    compose_project="$(docker inspect --type container -f '{{ index .Config.Labels "com.docker.compose.project" }}' "$container" 2>/dev/null || true)"
    config_files="$(docker inspect --type container -f '{{ index .Config.Labels "com.docker.compose.project.config_files" }}' "$container" 2>/dev/null || true)"
    [[ -n "$compose_project" && -n "$config_files" ]] || continue
    IFS=',' read -r -a args <<<"$config_files"
    signoz_compose_down "$compose_project" "${args[@]}" && return 0
  done
  return 1
}

remove_stale_signoz_containers() {
  local containers=()
  while IFS= read -r name; do
    [[ -n "$name" ]] && containers+=("$name")
  done < <(docker ps -aq --filter name='^signoz$' --filter name='^signoz-' --filter name='^signoz-zookeeper-')

  [[ "${#containers[@]}" -gt 0 ]] || return 0
  docker rm -f "${containers[@]}" >/dev/null 2>&1 || true
}

docker compose -f "$SCRIPT_DIR/demo-compose.yml" --env-file "$ENV_FILE" down
if [[ -f "$SIGNOZ_RENDERED_COMPOSE" ]]; then
  signoz_compose_down signoz "$SIGNOZ_RENDERED_COMPOSE" || true
elif [[ -f "$SIGNOZ_DIR/deploy/docker/docker-compose.yaml" ]]; then
  signoz_compose_down signoz "$SIGNOZ_DIR/deploy/docker/docker-compose.yaml" || true
fi
signoz_discovered_compose_down || true
remove_stale_signoz_containers
