# Mock UWB Simulator

This connector generates a repeatable WGS84 UWB demo stream for Open RTLS Hub.
It simulates 10 objects moving through a 3-floor Pac-Man-style building,
publishes raw `location_updates` at 25Hz, and is intended to run against a hub
configured to emit Kalman-normalized derived output at 2Hz.

The connector also bootstraps building metadata into the hub:

- one georeferenced `uwb` zone per floor
- one WGS84 polygon fence per floor
- one generated floorplan image per floor under `connectors/uwb_sim/assets/`
- stable floorplan IDs, ground control points, and image corner coordinates in `Zone.properties`

## Runtime Behavior

- simulation rate: `25Hz`
- object count: `10`
- floor count: `3`
- floor elevations: `0m`, `5m`, `10m`
- collisions: trackables are bootstrapped with `radius=0.5m`, so the hub emits
  collisions when objects come within `1m`

Movement rules:

- objects stay on corridor edges only
- dead ends reverse direction
- normal branch points choose a non-backtracking continuation randomly
- floor connector nodes either continue on the same floor or switch floors
- horizontal position includes a bounded sinusoidal lateral sway
- altitude includes a bounded sinusoidal vertical wobble
- speed varies smoothly per object over time

## Floorplan Metadata

The connector generates one SVG floorplan image per floor and stores the
alignment metadata in `Zone.properties`:

- `floorplan_id`
- `floorplan_image_path`
- `floorplan_image_size`
- `floorplan_corner_order`
- `floorplan_corners_local`
- `floorplan_corners_wgs84`
- `floor_outline_local`
- `floor_outline_wgs84`

Corner order is always:

1. `top_left`
2. `top_right`
3. `bottom_right`
4. `bottom_left`

The zone bootstrap also includes three `ground_control_points` per floor. The
simulator still uses a local corridor graph internally, but it publishes WGS84
positions and exposes both local and WGS84 image-corner metadata so a future
visualizer can place the generated floorplan image behind live movement points.

## Shared Local Hub

Start the reusable local hub runtime first:

```bash
local-hub/start_demo.sh
```

Fetch an admin token when auth is enabled:

```bash
local-hub/fetch_demo_token.sh
```

The local demo compose for this repository should enable:

- `COLLISIONS_ENABLED=true`
- `KALMAN_FILTER_ENABLED=true`
- `KALMAN_EMIT_MAX_FREQUENCY_HZ=2`

## Setup

1. Copy `.env.example` to `.env.local`:

```bash
cp connectors/uwb_sim/.env.example connectors/uwb_sim/.env.local
```

Set `HUB_TOKEN` in that local file when auth is enabled. The repository does not
ship a prebuilt token.

2. Sync the Python runtime:

```bash
uv sync --project connectors/uwb_sim
```

3. Generate floorplan assets and bootstrap the provider, trackables, floors, and
   fences:

```bash
uv run --project connectors/uwb_sim python connectors/uwb_sim/connector.py \
  --env-file connectors/uwb_sim/.env.local \
  --bootstrap-only
```

4. Start the simulator:

```bash
uv run --project connectors/uwb_sim python connectors/uwb_sim/connector.py \
  --env-file connectors/uwb_sim/.env.local
```

## Useful Subscriptions

Raw and derived locations:

```bash
uv run --project scripts python scripts/log_locations.py \
  --env-file connectors/uwb_sim/.env.local \
  --output connectors/uwb_sim/logs/location_updates.ndjson
```

Collision events:

```bash
uv run --project scripts python scripts/log_collision_events.py \
  --env-file connectors/uwb_sim/.env.local \
  --output connectors/uwb_sim/logs/collision_events.ndjson
```

## Notes

- The floorplans are slightly offset in local XY between floors before
  georeferencing so the current WGS84 collision implementation, which is
  effectively 2D plus per-trackable radius, does not emit false collisions for
  vertically stacked positions.
- The same corridor topology is reused on all floors.
- The generated SVG assets live in `connectors/uwb_sim/assets/`.
- `UWB_SIM_ANCHOR_LATITUDE` and `UWB_SIM_ANCHOR_LONGITUDE` control where the
  mock building is placed in WGS84.
