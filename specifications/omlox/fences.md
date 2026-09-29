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

- Provider observations and selected trackable observations maintain independent fence membership and emit their respective events.
- Trackable entry includes the circular extent defined by `radius`; even a single touching point counts as inside. Providers are evaluated as points. Polygon holes are excluded.
- Radius and exit tolerance are meters, including fences in EPSG:4326. Geographic comparisons use a local planar approximation; the spatial index expands its query by the trackable radius.
- Table 13 controls fence, tolerance, and outside deadlines. The earliest deadline emits `region_exit` even when no further position arrives. Returning inside cancels the exit timers and refreshes the fence timeout.
- Zero is immediate and `-1` is infinite. An absent fence `timeout` is infinite. With a nonzero exit tolerance, an unset or zero `tolerance_timeout` inherits the fence timeout, as required by section 8.2.1.4.
- Each parameter uses the current provider's value first, then the trackable's, then the fence's. The fence's own `timeout` initializes the fence deadline, following section 10.1 despite the less precise object-field descriptions of `fence_timeout`.
- Fence membership is not silently removed by the location cache TTL. One indexed deadline per membership avoids accumulating obsolete timer callbacks.
- Different explicit floor values suppress fence events and clear previous membership. Missing floor matches all floors, following section 8.4.
- Events copy fence properties and foreign ID. Exit events retain both the entry time and scheduled exit time; the event envelope timestamp uses the exit time.
- Local and projected fence definitions are transformed into a shared geographic index when metadata changes. Each observation is evaluated once; switching source zones can release membership in the previous zone.
- `subdivide=true` on create/update splits polygon edges to at most ten meters before transformation, preserving metadata and interpolating height. Ten meters and the 100000-vertex limit are implementation choices; the upstream parameter does not prescribe a distance. Point regions are unchanged.
- `spatial_query=false` returns logical membership maintained by timers. `true` recomputes geometric containment from retained observations, including CRS, radius, floor, and height where available.
- Deleting a fence or trackable cancels its pending timers. Administrative deletion does not emit a fabricated location or exit event.
