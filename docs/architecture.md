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
2. The hub validates, normalizes, deduplicates, and updates in-memory transient state on the ingest path.
3. A buffered native-publication stage emits native location and motion events without blocking ingest on downstream fan-out.
4. A second buffered decision stage is the insertion point for future filtered or smoothed track processing and currently drives alternate-CRS publication, geofence evaluation, and optional collision evaluation.
5. The decision worker drains queued locations in bounded batches before processing them so bursty ingest spends less time on per-item queue churn.
6. MQTT and WebSocket consume the resulting internal event stream and publish transport-specific payloads in batches.
7. When any non-critical queue fills, the hub drops newer work on that path rather than backpressuring raw ingest.

Implications:
- ingest logic is shared across REST, MQTT, and WebSocket
- MQTT is no longer the only downstream publication path
- the internal event seam decouples downstream publication from MQTT-specific topics
- location ingest latency is protected from slower transport fan-out, geofence work, or collision work
- the decision-stage queue is the intended insertion point for future filtered or smoothed track processing before fence/collision decisions
- WebSocket fan-out coalesces multiple internal events into fewer wrapper messages and drops outbound payloads for slow subscribers instead of tearing the connection down immediately
- hub-issued UUIDs for REST-managed resources, derived fence/collision events, and RPC caller IDs now use UUIDv7 so emitted identifiers are time-sortable
- internal hub events carry the persisted `origin_hub_id` so downstream transports preserve source provenance

## Observability Boundaries
- `internal/observability` owns OpenTelemetry resource setup, OTLP exporters, lifecycle management, and the small internal instrumentation API used by the rest of the runtime.
- OTLP export is collector-first: metrics, traces, and logs go to an OpenTelemetry collector such as SigNoz rather than to a hub-owned scrape endpoint.
- Transport handlers attach ingest transport context at entry so REST, MQTT, and WebSocket traffic create one shared root ingest span shape before entering `internal/hub`.
- The hot path records low-cardinality metrics for accepted, deduplicated, rejected, and failed ingest, queue depth and wait time, stage latency, event-bus fan-out, MQTT publish, WebSocket dispatch, RPC execution, and end-to-end processing time.
- Child spans isolate proximity resolution, native publication, decision processing, fence evaluation, collision evaluation, metadata reconcile, auth, and runtime dependency work so slow stages can be inspected directly.
- Asset-, provider-, zone-, and fence-centric identifiers belong on traces and structured logs only. They are intentionally excluded from normal metric labels so dashboards remain queryable under sustained ingest volume.
- Zap remains the application logging API. When OTLP logs are enabled, the logger tees into the OpenTelemetry bridge so the same structured events still appear locally while also being exported to the collector.
- Runtime drop counters and gauges for queue occupancy, event-bus subscribers, and WebSocket connections are exposed from `internal/hub` through observable instruments so the e2e stack can dashboard overload and degraded states without adding lock-heavy bookkeeping to the ingest path.
- Per-drop OTLP attribution is also exported via `hub.runtime.drop_events_total{stage,reason}` so dashboards can break overload down by bounded cause labels without relying on the local debug endpoint.
- For local troubleshooting, the hub also exposes an auth-protected `/debug/runtime/drops` endpoint that returns the current drop counters plus a bounded set of recent sampled drops with stage and object context.
- Collision thresholds are meter-based even on the WGS84 collision path; the runtime uses a short-range planar approximation instead of geodesic math so collision checks stay cheap in the decision stage.

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

Resolver scope:
- the resolver emits the configured zone position as the derived point
- proximity resolution supports static proximity zones
- resolution policy is driven by hub defaults and zone-specific overrides

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
