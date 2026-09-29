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
- If `trackables` is omitted, all trackables assigning the incoming provider are associated with the observation. Each trackable independently applies its locating rules. No match leaves the location unassociated.
- `locating_rules` select among a trackable's provider/source candidates. The highest
  matching priority wins; unmatched candidates have priority zero. Equal priorities
  use the newest generated timestamp and preserve the existing selection on an
  exact tie. With no rules, only a strictly newer generated timestamp is significant.
- All retained candidates are re-evaluated on each associated update. An older
  observation arriving after a newer observation from the same provider/source
  does not replace the newer observation.
- Expressions support comparisons (`=`, `!=`, `<`, `<=`, `>`, `>=`), `AND`,
  parentheses, boolean literals, numeric values, and quoted strings. Properties
  are `accuracy`, `name` (provider name), `type` (provider technology), `source`,
  `provider_id`, `floor`, `speed`, and `timestamp_diff` (generated-time age in ms).
  Missing comparison operands do not match. Omitted floor uses zero.
- Rule parsing and operand type validation happen before persisting trackables.
  Expressions are limited to 4096 bytes and 64 nested groups.
- Provider location streams remain independent of the selected trackable location.
  Motion read endpoints work with collision processing disabled.
- The optional Kalman stage processes only selected observations. A provider/source
  switch or backwards selected event time resets its previous filter state.

`force_location_update=true` on trackable creation/update selects from retained
observations of the assigned providers without modifying those provider records.
Reprocessing an unchanged motion does not replay a motion notification; containment
is re-evaluated so an actual fence transition can still emit an event.

## Circular extent and collisions

`radius` defaults to zero and is used for both fence and collision decisions. When
set, motion geometry is a 64-segment approximation of the circle around the selected
position. Arbitrary supplied polygon bounding boxes do not determine collisions.

When collision processing is enabled, each significant intersecting update emits
`collision_start` or `colliding`. The trackable updated last is first in the pair.
Active pairs are considered even after a move outside the spatial search window.
Table 14 controls separation: provider values override each trackable's values,
exit tolerances add, and timeout values use the maximum of the two sides (infinity
wins). The minimum active separation deadline emits `collision_end` without another
position update. Observation cache expiry does not silently remove a collision.

Explicit floors must match. With known z-coordinates and explicit extrusions,
vertical intervals must intersect; zero extrusion is a plane. Missing extrusion is
unbounded within the applicable floor, following the companion JSON description.
Missing location height leaves the horizontal/floor comparison. Mixed elevation
references are excluded from comparisons because section 7.14 leaves them undefined.

Collision candidates use an Earth-centered spatial index to avoid geographic date
line and polar projection discontinuities. Final horizontal distance and geometry
use a short-range planar approximation in meters. The emitted collision area is a
point for tangency and a polygonal approximation of the intersection boundary
otherwise. That closed boundary is encoded as a `LineString`, following the
`collision_area` schema in section 6.7.2 and the companion JSON, even though the
behavioral chapter describes the overlap as an area.
