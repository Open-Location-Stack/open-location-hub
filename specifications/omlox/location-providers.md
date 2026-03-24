# OMLOX V2 Location Provider API

## Intent
Setup of location providers and advertisement of location updates to the hub.

Spec references:
- Chapter 6.4 (Location Provider API)
- Section 6.7.9 (`LocationProvider`)

## Resource Schema (`LocationProvider`)

Key fields from section 6.7.9:
- `id` (string, required; provider-specific, MAC-like where applicable)
- `type` (string, required)
- `name` (string)
- `sensors` (object)
- geofencing/collision parameters:
  - `fence_timeout`
  - `exit_tolerance`
  - `tolerance_timeout`
  - `exit_delay`
- `properties` (object)

## Operations

### `POST /v2/providers/locations`
Advertise/push location updates (`Location` objects) to the hub.

Current repository behavior for location ingestion:
- the hub accepts omitted `crs`, `local`, and named EPSG codes
- local-to-WGS84 publication uses zone `ground_control_points`
- named projected CRS conversion uses the runtime projection engine
- the hub publishes only the derived topic variants it can produce safely and suppresses unavailable variants instead of aliasing bad coordinates

### `POST /v2/providers/proximities`
Advertise proximity updates (`Proximity` objects), which the hub converts into `Location` processing flow.

Current repository behavior for proximity ingestion:
- the hub resolves the referenced source to a proximity-capable zone by `zone.id` or `zone.foreign_id`
- the hub may keep the currently resolved zone briefly to reduce flapping between nearby zones
- the derived `Location` uses the resolved zone position in local coordinates
- georeferenced zones also emit derived WGS84 publication for the resulting location
- per-zone tuning comes from `Zone.properties.proximity_resolution`

### Inferred resource lifecycle operations
The Location Provider API is defined as setup + update advertisement API; companion OpenAPI is expected to define provider management endpoints under `/v2/providers` and `/v2/providers/{providerId}`.
