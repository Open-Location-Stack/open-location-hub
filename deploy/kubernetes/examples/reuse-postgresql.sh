#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export DB_MODE=external
: "${DB_RUNTIME_SECRET:?set the runtime connection Secret}"
: "${DB_MIGRATION_SECRET:?set the migration connection Secret}"
: "${DB_RELAY_SECRET:?set the configuration relay connection Secret}"
exec "$script_dir/../deploy.sh"
