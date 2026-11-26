#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export DB_MODE=managed
export PG_VALUES="${PG_VALUES:-$script_dir/postgres.yaml}"
export KAFKA_VALUES="${KAFKA_VALUES:-$script_dir/kafka.yaml}"
exec "$script_dir/../deploy.sh"
