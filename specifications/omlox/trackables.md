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
Trackable API is defined as a management API. The companion OpenAPI surface used in this repository now includes:
- `/v2/trackables`
- `/v2/trackables/summary`
- `/v2/trackables/motions`
- `/v2/trackables/{trackableId}`
- `/v2/trackables/{trackableId}/fences`
- `/v2/trackables/{trackableId}/location`
- `/v2/trackables/{trackableId}/locations`
- `/v2/trackables/{trackableId}/motion`
- `/v2/trackables/{trackableId}/providers`
- `/v2/trackables/{trackableId}/sensors`

Current repository contract status:
- CRUD, summary, motion, nested read endpoints, and collection delete are implemented.

## Behavioral requirements

- A trackable location is based on updates from assigned location providers.
- `omlox` type supports self-assignment style, `virtual` type supports API assignment.
- Trackable/fence interaction and collision behavior is normative in chapters 9, 10, and 11.

Current repository behavior:
- If an incoming `Location` already names `trackables`, the hub uses that explicit association.
- If `trackables` is omitted and exactly one stored `Trackable.location_providers` entry matches the incoming `provider_id`, the hub auto-associates the location to that trackable and marks it as associated.
- If no match or more than one match exists for the provider, the hub currently leaves the location unassociated rather than guessing.
- `locating_rules` are modeled in the contract but are not yet applied at runtime to resolve ambiguous multi-trackable provider matches.
