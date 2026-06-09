# OMLOX V2 Fence API

## Intent
Fence creation and lifecycle for geofencing and event emission.

Spec references:
- Chapter 6.5 (Fence API)
- Section 6.7.4 (`Fence`)
- Section 6.7.5 (`FenceEvent`)
- Chapter 8 (Fences)

## Resource Schema (`Fence`)

Key fields:
- `id` (UUID)
- `region` (Polygon | Point)
- `radius` (for point-based circular fence)
- `extrusion`, `floor`
- `name`, `foreign_id`, `properties`
- geofencing behavior controls:
  - `timeout`
  - `exit_tolerance`
  - `tolerance_timeout`
  - `exit_delay`
- coordinate metadata:
  - `crs`
  - `zone_id` (required when `crs=local`)
  - `elevation_ref`

## Operations

### Inferred resource lifecycle operations
Chapter 6.5 explicitly states fence API handles creation/update/deletion, implying companion OpenAPI CRUD endpoints under `/v2/fences` and `/v2/fences/{fenceId}`.

## Events (`FenceEvent`)

Event object includes:
- `id`, `fence_id`, `event_type` (`region_entry` | `region_exit`)
- optional `provider_id`, `trackable_id`, `trackables`, `foreign_id`
- optional `entry_time`, `exit_time`
- copied custom `properties`

Published via WebSocket topic `fence_events`.

## Current Repository Behavior

- Fence entry is emitted as soon as an accepted location is inside the fence geometry.
- Fence exit is evaluated on each accepted location update for the trackable.
- `exit_tolerance` creates an outer grace band beyond the fence boundary before an exit is considered final.
- `tolerance_timeout` limits how long the hub keeps a trackable inside while it remains only within that tolerance band.
- `exit_delay` adds an additional debounce after the hub has decided the trackable is truly outside.
- Positive values are interpreted as milliseconds for `tolerance_timeout` and `exit_delay`.
- Positive values are applied in override order from fence to current location provider to trackable.
- Disabling negative values on `tolerance_timeout` and `exit_delay` clear that behavior back to the conservative default instead of inheriting a broader delay or hold.
- Omitted or `null` values do not add grace behavior on their own; if no positive value survives the override chain, the effective default remains immediate exit.
- Exit timing is event-driven rather than timer-driven: the hub finalizes delayed exits when a subsequent location update confirms the elapsed timeout or delay.
