# OMLOX V2 Locations Payload

## Intent
Canonical payload for RTLS position updates consumed by the hub.

Spec references:
- Section 6.7.8 (`Location`)
- Chapter 7.3 (Processing location updates from a zone)
- Chapter 7.8 (processing flow)

## Schema (`Location`)

Required:
- `position` (Point)
- `source` (zone id or foreign id)
- `provider_type`
- `provider_id`

Optional (selected):
- `trackables` (list)
- `timestamp_generated`, `timestamp_sent` (ISO-8601 UTC)
- `crs` (`local` or supported EPSG)
- `accuracy`, `floor`, `speed`, `course`
- `associated`, heading fields
- `elevation_ref` (`floor` | `wgs84`)
- `properties` (object)

## Ingestion endpoints

- `POST /v2/providers/locations` (REST)
- WebSocket topic `location_updates` via `/v2/ws/socket`
- MQTT optional: `/omlox/json/location_updates/pub/{provider_id}`

## Response/processing behavior

- Hub MUST process updates according to chapter 7 flow.
- Hub MUST publish resulting updates to WebSocket subscribers immediately.
- Current repository behavior accepts omitted `crs`, `local`, and named EPSG codes.
- In MQTT extension, the hub publishes derived local and WGS84 variants when the necessary transformation is available.
- Local georeferencing is derived from `Zone.ground_control_points`; named CRS conversion is delegated to the runtime projection engine.
- If a requested derived variant cannot be produced safely, the hub suppresses that variant rather than publishing incorrect coordinates.
- When optional Kalman normalization is enabled, the hub may derive OMLOX-compatible `course` and `speed` values from the normalized track and may publish additional hub-specific derived metrics such as vertical speed only in `properties`.
- Kalman-specific metadata must remain in `properties`; the hub does not add non-OMLOX top-level `Location` fields for this behavior.
