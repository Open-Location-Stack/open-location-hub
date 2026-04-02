# OpenSky Connector Demonstrator

This demonstrator polls the public OpenSky state-vector API and forwards live
aircraft positions to a local Open RTLS Hub over the OMLOX WebSocket
`location_updates` topic.

The checked-in defaults target Frankfurt Airport and its surrounding airspace:

- bounding box preset `frankfurt` limits polling volume
- airport fence preset `frankfurt` creates airport and terminal-sector catchments
- no OpenSky account or API key is required for the basic public demo path

Alternate built-in presets:

- `newyork`: wider New York area with JFK, LaGuardia, and Newark airport sectors
- `munich`: Munich Airport and terminal sectors

## Files

- `connector.py`: polls OpenSky state vectors and publishes OMLOX `location_updates`
- `airport_fences.py`: creates airport and apron-sector zones/fences from presets
- `opensky_support.py`: OpenSky polling and preset helpers
- `hub_client.py`: hub REST and WebSocket helpers
- `.env.example`: environment template
- `pyproject.toml`: `uv`-managed Python project metadata
- `uv.lock`: locked Python dependency set

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

- `HUB_HTTP_URL`
- `HUB_WS_URL`
- `HUB_TOKEN` optional but needed when auth is enabled
- `OPENSKY_URL`
- either `OPENSKY_REGION_PRESET` or all of `OPENSKY_LAMIN`, `OPENSKY_LOMIN`, `OPENSKY_LAMAX`, `OPENSKY_LOMAX`

Optional tuning:

- `OPENSKY_POLL_INTERVAL_SECONDS`
- `OPENSKY_ON_GROUND_ONLY`
- `OPENSKY_AIRPORT_PRESET`
- `OPENSKY_FENCE_RADIUS_SCALE`

## Setup

1. Start the shared local hub:

```bash
connectors/local-hub/start_demo.sh
```

2. Copy `.env.example` to `.env.local`:

```bash
cp connectors/opensky/.env.example connectors/opensky/.env.local
```

3. Fetch a Dex token and set `HUB_TOKEN` in `.env.local` when needed.

4. Sync the Python runtime:

```bash
uv sync --project connectors/opensky
```

5. Bootstrap airport fences:

```bash
uv run --project connectors/opensky python connectors/opensky/airport_fences.py --env-file connectors/opensky/.env.local
```

6. Start the connector:

```bash
uv run --project connectors/opensky python connectors/opensky/connector.py --env-file connectors/opensky/.env.local
```

7. Optional: log live WebSocket topics with the shared root scripts:

```bash
uv run --project scripts python scripts/log_locations.py --env-file connectors/opensky/.env.local --output connectors/opensky/logs/location_updates.ndjson
uv run --project scripts python scripts/log_fence_events.py --env-file connectors/opensky/.env.local --output connectors/opensky/logs/fence_events.ndjson
uv run --project scripts python scripts/log_collision_events.py --env-file connectors/opensky/.env.local --output connectors/opensky/logs/collision_events.ndjson
```

8. Check how close captured aircraft positions came to the airport fences:

```bash
uv run --project scripts python scripts/check_fence_alignment.py --env-file connectors/opensky/.env.local --locations-log connectors/opensky/logs/location_updates.ndjson
```

## Hub Mapping

- one OpenSky state vector becomes one OMLOX `Location`
- aircraft `icao24` identifiers become deterministic hub `Trackable` IDs
- `provider_id` defaults to `opensky-demo`
- `provider_type` defaults to `adsb`
- `source` is `opensky:<icao24>`
- callsign, country, on-ground state, altitude, and squawk go into `Location.properties`

## Airport Fences

The bootstrap script creates circular zones and fences for airport sectors. The
default `frankfurt` preset includes:

- airport-wide catchment
- Terminal 1 sector
- Terminal 2 sector
- cargo/south apron sector

These are sector-level catchments rather than gate-level geometry because the
public feed is suitable for airport/apron eventing but not reliable enough for
precise stand or gate assignment.

## Limitations

- anonymous OpenSky access is subject to public rate limits
- the public feed is good for airport and terminal-sector movement, not exact gate tracking
- on-ground traffic quality varies by airport and receiver coverage
