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

### `POST /v2/providers/proximities`
Advertise proximity updates (`Proximity` objects), which the hub converts into `Location` processing flow.

### Inferred resource lifecycle operations
The Location Provider API is defined as setup + update advertisement API; companion OpenAPI is expected to define provider management endpoints under `/v2/providers` and `/v2/providers/{providerId}`.
