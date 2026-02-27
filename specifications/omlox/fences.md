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
