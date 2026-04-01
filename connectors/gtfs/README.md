# GTFS Connector Demonstrator

This demonstrator forwards GTFS-RT vehicle positions to a locally running Open
RTLS Hub over the OMLOX WebSocket `location_updates` topic and bootstraps
station zones and polygon fences for arrival and departure tracking.

The checked-in defaults target the live Grand Dole network:

- static GTFS input defaults to the public `gtfs-dole.zip` feed
- realtime input defaults to the public Grand Dole GTFS-RT vehicle positions feed
- station polygons are generated from GTFS stop geometry by default
- optional external reference datasets can be layered on top when a deployment
  has richer stop-area geometry available

## Files

- `connector.py`: polls GTFS-RT vehicle positions and publishes OMLOX
  `location_updates` over WebSocket
- `station_polygons.py`: creates one station `Zone` and one station `Fence` per
  station in the hub
- `scripts/log_locations.py`: subscribes to `location_updates` and writes NDJSON
- `scripts/log_fence_events.py`: subscribes to `fence_events` and writes NDJSON
- `scripts/log_collision_events.py`: subscribes to `collision_events` and writes NDJSON
- `scripts/check_geofence_alignment.py`: compares logged locations against the
  current station fences
- `hub_client.py`: shared REST and WebSocket helper code
- `gtfs_support.py`: GTFS parsing, GTFS-RT decoding, and station polygon
  generation helpers
- `.env.example`: environment template
- `pyproject.toml`: `uv`-managed Python project metadata
- `uv.lock`: locked Python dependency set for the demo

## Shared Local Hub

This demo uses the shared local runtime in
[`connectors/local-hub`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/local-hub).

Start it with:

```bash
connectors/local-hub/start_demo.sh
```

Fetch an admin token with:

```bash
connectors/local-hub/fetch_demo_token.sh
```

## Required Inputs

- `HUB_HTTP_URL`: base URL for REST bootstrap calls such as `http://localhost:8080`
- `HUB_WS_URL`: WebSocket endpoint such as `ws://localhost:8080/v2/ws/socket`
- `HUB_TOKEN`: optional JWT access token; required when hub auth is enabled
- `GTFS_STATIC_URL`: GTFS zip URL or local path
- `GTFS_RT_URL`: GTFS-RT protobuf URL
- `GTFS_REFERENCE_DATASET_FAMILY`: optional external geometry helper set; leave
  unset for GTFS-only geometry, or set to `idfm` for Ile-de-France reference data

Optional filters and tuning:

- `GTFS_POLL_INTERVAL_SECONDS`: GTFS-RT poll interval
- `GTFS_ROUTE_FILTER`: route ID filter for the runtime connector
- `GTFS_STATION_FILTER`: station-name substring filter for polygon bootstrap
- `GTFS_MAX_STATIONS`: cap station bootstrap volume during local testing
- `GTFS_FALLBACK_RADIUS_METERS`: legacy/default station circle radius
- `GTFS_STATION_POLYGON_MODE`: `circle`, `auto`, or `hull`
- `GTFS_STATION_RADIUS_METERS`: radius used for circle-based station fences
- `GTFS_STATION_HULL_BUFFER_METERS`: outward expansion applied to hull polygons

## Setup

1. Start the self-contained local runtime:

```bash
connectors/local-hub/start_demo.sh
```

2. Copy `.env.example` to `connectors/gtfs/.env.local` and fill in the required
   variables:

```bash
cp connectors/gtfs/.env.example connectors/gtfs/.env.local
```

3. If the hub runs with auth enabled, fetch a token with `connectors/local-hub/fetch_demo_token.sh`
   and set `HUB_TOKEN`.
4. Sync the Python runtime with `uv`:

```bash
uv sync --project connectors/gtfs
```

5. Bootstrap station zones and fences:

```bash
uv run --project connectors/gtfs python connectors/gtfs/station_polygons.py --env-file connectors/gtfs/.env.local
```

6. Start the connector:

```bash
uv run --project connectors/gtfs python connectors/gtfs/connector.py --env-file connectors/gtfs/.env.local
```

7. Optional: record live WebSocket topics to NDJSON:

```bash
uv run --project connectors/gtfs python connectors/gtfs/scripts/log_locations.py --env-file connectors/gtfs/.env.local
uv run --project connectors/gtfs python connectors/gtfs/scripts/log_fence_events.py --env-file connectors/gtfs/.env.local
uv run --project connectors/gtfs python connectors/gtfs/scripts/log_collision_events.py --env-file connectors/gtfs/.env.local
```

For a single GTFS-RT fetch during local testing:

```bash
uv run --project connectors/gtfs python connectors/gtfs/connector.py --env-file connectors/gtfs/.env.local --once
```

## Hub Mapping

### Vehicle updates

The connector publishes OMLOX WebSocket wrapper messages shaped like:

```json
{
  "event": "message",
  "topic": "location_updates",
  "payload": [
    {
      "position": { "type": "Point", "coordinates": [2.35, 48.85] },
      "crs": "EPSG:4326",
      "provider_id": "grand-dole-gtfs-demo",
      "provider_type": "gtfs-rt",
      "source": "gtfs-stop:stop-area-12345"
    }
  ],
  "params": {
    "token": "..."
  }
}
```

Runtime mapping details:

- `provider_id` stays stable for the connector instance
- `provider_type` defaults to `gtfs-rt`
- `source` is derived from the GTFS stop when present and falls back to the
  vehicle or entity ID otherwise
- vehicle IDs are mapped to deterministic trackable UUIDs and upserted through
  `/v2/trackables`
- trip, route, stop, and vehicle metadata are copied into `Location.properties`

### Station zones and fences

The bootstrap script creates:

- one `Zone` per station with deterministic UUID, `foreign_id` equal to the GTFS
  station ID, and centroid `position`
- one `Fence` per station with deterministic UUID, `foreign_id` equal to the
  GTFS station ID, and a WGS84 polygon ring

The current hub API does not let `Zone` carry polygon geometry, so polygon-based
arrival and departure tracking is intentionally modeled through `Fence` objects.

## Polygon Generation

Station polygons are generated from the available station geometry in this order:

1. GTFS station and child-stop coordinates
2. optional external reference points when `GTFS_REFERENCE_DATASET_FAMILY` is configured
3. centroid-plus-buffer fallback when there are too few points for a hull

Generation behavior:

- the default `circle` mode creates larger station catchment polygons around the centroid
- `auto` uses a hull when enough points exist and otherwise falls back to a circle
- `hull` uses a hull when possible and otherwise still falls back to a circle
- `GTFS_STATION_HULL_BUFFER_METERS` can expand tight stop-derived hulls outward
- `properties.generation_mode`, `properties.source_point_count`, and
  `properties.point_sources` capture the provenance

## Tracking Arrivals And Departures

After the bootstrap script creates station fences, normal hub fence processing
can emit `fence_events` when vehicle trackables enter or leave those station
polygons. Subscribe to `fence_events` or `fence_events:geojson` to observe
arrival and departure behavior.

The `scripts/` directory also includes a simple alignment checker:

```bash
uv run --project connectors/gtfs python connectors/gtfs/scripts/check_geofence_alignment.py --env-file connectors/gtfs/.env.local
```

## Limitations

- vehicle-to-trackable persistence is best-effort and keyed by deterministic
  UUIDs derived from feed identifiers
- station polygons are heuristic when the available stop geometry does not
  expose enough points for a hull
- the connector currently polls GTFS-RT over HTTP; it does not yet support
  feed-specific authentication or incremental realtime cursors
- the demo stack persists Postgres data but keeps Dex state in memory because
  the repository fixture uses static local users and clients
