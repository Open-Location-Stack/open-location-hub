# OpenSky Germany Benchmark Capture

This directory contains a version-controlled `location_updates` capture that can
be replayed into the hub for repeatable throughput and latency benchmarking.

## Dataset

- File: `location_updates.ndjson`
- Source: `connectors/opensky` Germany region preset
- Capture date: `2026-04-08`
- Capture duration: about 5 minutes
- Raw location updates: `8,367`
- Unique assets: `687`

The file is the raw NDJSON output from `scripts/log_locations.py`. It includes
the initial subscription event followed by the captured `location_updates`
messages exactly as they were received from the hub.

## Replay Benchmark Profile

The current stress profile replays this dataset with:

- `--interpolation-rate-hz 10.0`
- `--batch-window-ms 25`
- `--max-batch-size 256`
- `--trackable-radius-meters 50`

Those settings expand the capture to:

- Scheduled replay emissions: `1,581,177`
- Synthetic interpolation points: `1,572,810`
- Expansion factor over raw updates: about `189x`

## Example

```bash
TOKEN=$(./local-hub/fetch_demo_token.sh | jq -r .access_token)

env \
  HUB_HTTP_URL=http://localhost:8080 \
  HUB_WS_URL=ws://localhost:8080/v2/ws/socket \
  HUB_TOKEN="$TOKEN" \
  uv run --project connectors/replay python connectors/replay/connector.py \
    --input connectors/replay/benchmarks/opensky-germany-2026-04-08/location_updates.ndjson \
    --interpolation-rate-hz 10.0 \
    --trackable-radius-meters 50 \
    --batch-window-ms 25 \
    --max-batch-size 256
```
