# OpenSky Germany Benchmark Capture

This directory contains a version-controlled `location_updates` capture that can
be replayed into the hub for repeatable throughput and latency benchmarking.

## Dataset

- File: `location_updates.ndjson`
- Source: `connectors/opensky` Germany region preset
- Capture date: `2026-04-08`
- Source timestamp span: about 14 minutes 56 seconds
- Raw location updates: `8,368`
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

- Scheduled replay emissions: `1,590,133`
- Synthetic interpolation points: `1,581,765`
- Expansion factor over raw updates: about `190x`

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

## Functional profile

Run on a disposable Hub with `COLLISIONS_ENABLED=true` and an administrator
token. Point both commands at the same Hub with `HUB_HTTP_URL`, `HUB_WS_URL`
and `HUB_TOKEN` in the environment.

```bash
OPENSKY_AIRPORT_PRESET=germany uv run --project connectors/opensky \
  python connectors/opensky/airport_fences.py
uv run --project connectors/replay python connectors/replay/benchmark.py \
  --require-airport-events --rates 0 --target-duration-seconds 45 \
  --timeout-seconds 100 --observer-drain-seconds 5
```

For the accelerated workload, use `--rates 1 --target-duration-seconds 60
--timeout-seconds 145` with the same gate. This workload schedules 159,740
observations, including interpolation, and can expose decision-path drops.

To exercise shadow ingest on the local Docker stack, create the usual `.env`
file, apply the PostgreSQL migrations and run `just compose-shadow-up`.
Inspect the topic's end offsets before and after replay to verify mirrored
record counts independently from live delivery. Run with a new topic or record
both starting and ending offsets for each partition.
With `just compose-shadow-router` running, also compare the consumer group's
lag and the count of read-committed asset inputs. The airport capture uses one
trackable per observation.
