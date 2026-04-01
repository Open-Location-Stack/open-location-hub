#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${1:-$SCRIPT_DIR/demo.env}"
USERNAME="${2:-admin@example.com}"
PASSWORD="${3:-testpass123}"

DEX_PORT="$(awk -F= '/^DEMO_DEX_PORT=/{print $2}' "$ENV_FILE" | tail -n1)"
DEX_PORT="${DEX_PORT:-5556}"

curl -sS -X POST "http://localhost:${DEX_PORT}/dex/token" \
  -u open-rtls-cli:cli-secret \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data "grant_type=password&scope=openid%20email%20profile&username=${USERNAME}&password=${PASSWORD}"
