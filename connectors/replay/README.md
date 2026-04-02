# Replay Connector

This connector is a diagnostic tool for replaying logged hub
`location_updates` NDJSON back into an Open RTLS Hub. It is intended for trace
files produced by the shared root logging scripts such as
[`scripts/log_locations.py`](../../scripts/log_locations.py).

Replay behavior:

- preserves the relative timing between captured location updates
- shifts the full stream so the first emitted timestamp starts at "now"
- supports real-time replay or faster playback via `--acceleration-factor`
- can emit synthetic straight-line interpolation points per object via
  `--interpolation-rate-hz`
- batches due replay emissions into fewer WebSocket publishes via
  `--batch-window-ms` and `--max-batch-size`
- best-effort bootstraps referenced providers and trackables when `HUB_HTTP_URL`
  is configured

## Files

- `connector.py`: loads NDJSON, corrects timestamps to the current replay time,
  and publishes the stream over `location_updates`
- `replay_support.py`: NDJSON parsing, replay scheduling, and interpolation helpers
- `hub_client.py`: hub REST and WebSocket helpers
- `.env.example`: environment template
- `pyproject.toml`: `uv`-managed Python project metadata

## Shared Local Hub

Start the reusable local hub runtime first:

```bash
connectors/local-hub/start_demo.sh
```

Fetch an admin token when auth is enabled:

```bash
connectors/local-hub/fetch_demo_token.sh
```

## Required Inputs

- `HUB_WS_URL`: WebSocket endpoint such as `ws://localhost:8080/v2/ws/socket`
- `REPLAY_INPUT`: path to a logged `location_updates` NDJSON file

Optional but recommended:

- `HUB_HTTP_URL`: enables best-effort provider and trackable bootstrap before replay
- `HUB_TOKEN`: bearer token when hub auth is enabled
- `REPLAY_ACCELERATION_FACTOR`: playback speed multiplier, where `1.0` is real time
- `REPLAY_INTERPOLATION_RATE_HZ`: per-object interpolation cadence in Hertz, where
  `1.0` means once per second and `0.1` means once every 10 seconds
- `REPLAY_BATCH_WINDOW_MS`: coalesce replay events due inside this window into one
  WebSocket publish
- `REPLAY_MAX_BATCH_SIZE`: cap the number of locations sent in one replay publish

## Setup

1. Start the shared local hub:

```bash
connectors/local-hub/start_demo.sh
```

2. Copy `.env.example` to `.env.local`:

```bash
cp connectors/replay/.env.example connectors/replay/.env.local
```

3. Set `REPLAY_INPUT` to a previously captured NDJSON trace. For example, first
   capture a hub location feed:

```bash
uv run --project scripts python scripts/log_locations.py --env-file connectors/gtfs/.env.local --output connectors/gtfs/logs/location_updates.ndjson
```

4. Sync the Python runtime:

```bash
uv sync --project connectors/replay
```

5. Start the replay connector:

```bash
uv run --project connectors/replay python connectors/replay/connector.py --env-file connectors/replay/.env.local
```

## Playback Examples

Replay in real time:

```bash
uv run --project connectors/replay python connectors/replay/connector.py \
  --env-file connectors/replay/.env.local \
  --input connectors/gtfs/logs/location_updates.ndjson
```

Replay four times faster:

```bash
uv run --project connectors/replay python connectors/replay/connector.py \
  --env-file connectors/replay/.env.local \
  --input connectors/gtfs/logs/location_updates.ndjson \
  --acceleration-factor 4.0
```

Replay with interpolated points every second:

```bash
uv run --project connectors/replay python connectors/replay/connector.py \
  --env-file connectors/replay/.env.local \
  --input connectors/opensky/logs/location_updates.ndjson \
  --interpolation-rate-hz 1.0
```

Replay with interpolated points every 10 seconds:

```bash
uv run --project connectors/replay python connectors/replay/connector.py \
  --env-file connectors/replay/.env.local \
  --input connectors/opensky/logs/location_updates.ndjson \
  --interpolation-rate-hz 0.1
```

Replay with interpolation and larger WebSocket batches:

```bash
uv run --project connectors/replay python connectors/replay/connector.py \
  --env-file connectors/replay/.env.local \
  --input connectors/opensky/logs/location_updates.ndjson \
  --interpolation-rate-hz 10.0 \
  --batch-window-ms 25 \
  --max-batch-size 256
```

## Input Format

The connector expects the NDJSON shape produced by the shared logging scripts:

```json
{
  "received_at": "2026-04-02T10:15:00+00:00",
  "topic": "location_updates",
  "message": {
    "event": "message",
    "topic": "location_updates",
    "payload": [
      {
        "position": { "type": "Point", "coordinates": [8.56, 50.03] },
        "provider_id": "opensky-demo",
        "provider_type": "adsb",
        "source": "opensky:3c6621",
        "timestamp_generated": "2026-04-02T10:14:58+00:00"
      }
    ]
  }
}
```

Replay uses `payload[*].timestamp_generated` when present and falls back to
`received_at` otherwise.

## Interpolation Behavior

Interpolation is keyed per object. The connector uses the first trackable ID
when present and otherwise falls back to the location `source`. For each object,
synthetic points are inserted on a straight line between two consecutive logged
positions when the elapsed time between them is greater than the requested
interval.

Synthetic locations:

- inherit the later sample as their metadata template
- receive an adjusted `timestamp_generated` aligned to the replay clock
- include `properties.replay_synthetic_interpolation=true`
- preserve the original source timestamp in
  `properties.replay_original_timestamp_generated`

Batching behavior:

- the connector waits for the first due replay timestamp in a batch
- it then publishes all subsequent events scheduled within `batch-window-ms`
- batching reduces per-frame overhead but does not change the replay timestamps
  carried in the payload

## Limitations

- interpolation is linear in WGS84 longitude and latitude; it is intended for
  diagnostic playback, not precise route reconstruction
- only GeoJSON `Point` locations are supported
- replay can recreate provider and trackable IDs, but it cannot restore
  metadata that was never present in the logged location payload
