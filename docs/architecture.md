# Architecture

## Layers
- `cmd/hub`: process bootstrap and wiring
- `internal/config`: environment-driven configuration
- `internal/httpapi`: API surface and handlers
- `internal/ws`: OMLOX WebSocket wrapper protocol, subscriptions, and fan-out
- `internal/storage/postgres`: durable store
- `internal/mqtt`: MQTT topic mapping and broker integration
- `internal/auth`: token verification middleware
- `internal/rpc`: local-method dispatch, MQTT RPC bridging, announcements, and aggregation
- `internal/hub`: shared CRUD, ingest, derived event generation, collision evaluation, and internal event bus emission

## Metadata And Hot State
- Postgres is the durable source of truth for hub metadata, zones, fences, trackables, and location providers.
- The runtime resolves the singleton hub metadata row before the service starts so one stable `hub_id` and label are available for startup validation, internal event provenance, and identify responses.
- The hub loads those resources into an immutable in-memory metadata snapshot before it accepts traffic.
- Successful CRUD writes update Postgres first, then update the in-memory snapshot, invalidate any affected derived metadata such as zone transforms, and emit a `metadata_changes` bus event.
- A background reconcile loop reloads durable metadata periodically and emits the same `metadata_changes` notifications when it detects out-of-band create, update, or delete drift.
- Decision-critical ingest state is kept in process memory:
  - dedup windows
  - latest provider-source locations
  - latest trackable locations and WGS84 motions
  - proximity hysteresis state
  - fence membership state
  - collision pair state

## Event Fan-Out
1. REST, MQTT, or WebSocket ingest enters the shared hub service.
2. The hub validates, normalizes, deduplicates, updates in-memory transient state, and derives follow-on events.
3. The hub emits normalized internal events for locations, proximities, trackable motions, fence events, optional collision events, and metadata changes.
4. MQTT and WebSocket consume that same event stream and publish transport-specific payloads.

Implications:
- ingest logic is shared across REST, MQTT, and WebSocket
- MQTT is no longer the only downstream publication path
- the internal event seam is intended to keep future federation work from depending on MQTT-specific topics
- hub-issued UUIDs for REST-managed resources, derived fence/collision events, and RPC caller IDs now use UUIDv7 so emitted identifiers are time-sortable
- internal hub events now also carry the persisted `origin_hub_id` so downstream transports and future federation work can preserve source provenance without inventing it later

## RPC Control Plane
1. A client calls `GET /v2/rpc/available` or `PUT /v2/rpc` over HTTP.
2. REST auth verifies the bearer token and route-level access.
3. The RPC bridge applies method-level authorization for discovery or invocation.
4. The bridge looks up the method in a unified registry containing:
   - hub-owned local methods
   - MQTT-discovered external handlers
5. The bridge either:
   - handles the method locally
   - forwards it to MQTT
   - or does both and aggregates responses
6. The bridge returns a JSON-RPC result or JSON-RPC error payload to the HTTP caller.

Built-in identify behavior:
- `com.omlox.identify` returns the persisted hub label as its `name`
- `com.omlox.identify` also returns the stable persisted `hub_id`

Trust boundaries:
- HTTP clients should talk to the hub, not directly to MQTT devices
- MQTT should be restricted to the hub and trusted device/adaptor components
- the hub is the policy, audit, and handler-selection boundary for control-plane actions

## Proximity Resolution Path
1. A REST, WebSocket, or MQTT `Proximity` update enters the shared hub service.
2. The hub resolves the referenced zone by `zone.id` or `zone.foreign_id`.
3. Only proximity-oriented zones are accepted for this path (`rfid` and `ibeacon`).
4. The hub loads transient per-provider proximity state from the in-memory processing state.
5. The resolver applies hub defaults plus any `Zone.properties.proximity_resolution` overrides.
6. Hysteresis rules decide whether to stay in the current zone or switch to the new candidate zone.
7. The hub emits a derived local `Location` using the resolved zone position and then continues through the normal location pipeline.

Resolver notes:
- durable configuration lives in Postgres as part of the zone resource
- transient proximity membership state lives in the in-memory processing state
- derived location metadata includes hub extension fields such as `resolution_method`, `resolved_zone_id`, and `sticky`

Current limits and likely next steps:
- the resolver currently treats zone position as the emitted point; it does not estimate a better coordinate inside the zone
- only static proximity zones are supported today
- a practical next extension is a mobile-zone mode where a proximity zone follows a referenced provider or trackable
- a second practical extension is to reuse future confidence/tolerance concepts across proximity resolution, locating rules, and fence handling

## Contract-first flow
1. Update OpenAPI spec.
2. Regenerate generated server/types.
3. Implement handler behavior.
4. Validate with tests and check pipeline.

## WebSocket Notes
- `GET /v2/ws/socket` is implemented outside the REST OpenAPI contract because it is a protocol companion surface rather than a generated REST endpoint.
- When auth is enabled, WebSocket messages authenticate with `params.token` and apply dedicated topic publish/subscribe authorization.
- `collision_events` is a known topic but remains configuration-gated by `COLLISIONS_ENABLED`.
- `metadata_changes` is a subscribe-only topic that carries lightweight metadata replication notifications shaped as `{id,type,operation,timestamp}`.
