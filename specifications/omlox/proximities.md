# OMLOX V2 Proximities Payload

## Intent
Payload for proximity-only systems (e.g., RFID, iBeacon) that do not provide full coordinates.

Spec references:
- Section 6.7.12 (`Proximity`)
- Chapter 7.13 (Processing proximity information)

## Schema (`Proximity`)

Required:
- `source`
- `provider_type`
- `provider_id`

Optional:
- `timestamp_generated`, `timestamp_sent`
- `accuracy`
- `properties`

## Ingestion endpoints

- `POST /v2/providers/proximities` (REST)
- WebSocket topic `proximity_updates` via `/v2/ws/socket`
- MQTT optional: `/omlox/json/proximity_updates/{source}/{provider_id}`

## Processing behavior

- Hub MUST map proximity data to a zone and derive `Location` for normal processing.
- Zone type validation MUST ensure proximity zone types (RFID/iBeacon).
