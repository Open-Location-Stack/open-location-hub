# OMLOX V2 Zone API

## Intent
Zone setup and spatial transformation context for location processing.

Spec references:
- Chapter 6.2 (Zone API)
- Chapter 7 (Zone behavior)
- Chapter 7.12 (zone announcement)

## Resource Schema (`Zone`)

Key fields from section 6.7.19:
- `id` (UUID, required)
- `type` (string, required)
- `foreign_id` (string)
- `position` (Point, optional)
- `radius` (number, proximity zone radius)
- `ground_control_points` (list; required for non-proximity complete configs)
- `incomplete_configuration` (boolean)
- `measurement_timestamp` (date)
- `site`, `building`, `floor`, `name`, `description`, `address`
- `wgs84_height` (number)
- `properties` (object)

## Operations

### `GET /v2/zones/{zoneId}`
Explicitly shown in chapter 7.12 as example lookup for zone existence.

### `POST /v2/zones`
Used for both:
- uninitialized zone announcement (`incomplete_configuration=true`)
- initialized zone announcement (complete required fields)

### Inferred resource lifecycle operations
The Zone API is defined as setup/management API. In the companion OpenAPI this typically includes list/read/update/delete forms for `/v2/zones` and `/v2/zones/{zoneId}`.

## Behavioral requirements

- Zone id MUST be stable for a localization system.
- Hub MUST support zone types including `uwb`, `wifi`, `rfid`, `ibeacon`.
- For non-proximity zones, GCP mapping is required for complete config.
- Hub MUST support CRS handling for EPSG:4326, UTM/UPS projections listed in chapter 7.
- Uninitialized zones are normative and MUST be supported.

## Hub Extension: `properties.proximity_resolution`

This repository reserves `Zone.properties.proximity_resolution` for hub-specific proximity resolver tuning.

Supported keys:
- `entry_confidence_min` (number, optional)
- `exit_grace_duration` (duration string like `15s` or numeric seconds)
- `boundary_grace_distance` (number, local coordinate units)
- `min_dwell_duration` (duration string or numeric seconds)
- `position_mode` (currently only `zone_position`)
- `fallback_radius` (number, used when `zone.radius` is unset)
- `stale_state_ttl` (duration string or numeric seconds)

Behavior:
- Hub-wide defaults come from environment configuration.
- Zone-level values override hub defaults for that zone only.
- Unknown keys are preserved for forward compatibility but ignored by the current resolver.
