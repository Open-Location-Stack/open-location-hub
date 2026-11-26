#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
env_file="${1:-$script_dir/demo.env}"
if [[ ! -f "$env_file" ]]; then
  cp "$script_dir/demo.env.example" "$env_file"
  echo "Created $env_file from demo.env.example"
fi

compose=(docker compose -f "$script_dir/demo-compose.yml" --env-file "$env_file")
"${compose[@]}" up --build -d --wait

printf 'Open Location Hub API at http://%s\n' "$("${compose[@]}" port hub 8080)"
printf 'Open Location Hub dashboards at http://%s/projects/location-hub\n' "$("${compose[@]}" port perses 8080)"
