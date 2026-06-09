# OMLOX V2 Trackable API

## Intent
Management and behavior of trackable entities that aggregate one or more location providers.

Spec references:
- Chapter 6.3 (Trackable API)
- Chapter 9 (Trackables)
- Section 6.7.13 (`Trackable`)

## Resource Schema (`Trackable`)

Key fields from section 6.7.13:
- `id` (UUID, required)
- `type` (required): `omlox` | `virtual`
- `name` (string)
- `geometry` (Polygon)
- `extrusion` (number)
- `radius` (number, meters; per-trackable override for collision and fallback geometry extent)
- `location_providers` (list of provider IDs)
- geofencing/collision parameters:
  - `fence_timeout`
  - `exit_tolerance`
  - `tolerance_timeout`
  - `exit_delay`
- `locating_rules` (list)
- `properties` (object)

## Operations

### Inferred resource lifecycle operations
Trackable API is defined as a management API; companion OpenAPI is expected to define CRUD endpoints under `/v2/trackables` and `/v2/trackables/{trackableId}`.

## Behavioral requirements

- A trackable location is based on updates from assigned location providers.
- `omlox` type supports self-assignment style, `virtual` type supports API assignment.
- Trackable/fence interaction and collision behavior is normative in chapters 9, 10, and 11.

Current repository behavior:
- If an incoming `Location` already names `trackables`, the hub uses that explicit association.
- If `trackables` is omitted and exactly one stored `Trackable.location_providers` entry matches the incoming `provider_id`, the hub auto-associates the location to that trackable and marks it as associated.
- If no match or more than one match exists for the provider, the hub currently leaves the location unassociated rather than guessing.
- `locating_rules` are modeled in the contract but are not yet applied at runtime to resolve ambiguous multi-trackable provider matches.
