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
- Current repository behavior accepts omitted `crs`, `local`, and `EPSG:4326` on ingest and rejects other CRS values.
- In MQTT extension, the hub currently republishes the same canonical payload to both local and EPSG:4326 topic variants.
- Real local-to-WGS84 coordinate transformation is not implemented yet; the `epsg4326` topic is currently a routing alias, not a transformed output.
