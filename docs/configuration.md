# Configuration

All runtime configuration is environment-driven.

Runtime lifecycle behavior:
- the hub process runs from a single signal-aware root context created from `SIGINT` and `SIGTERM`
- startup failures return structured process errors instead of panicking during early config or logger initialization
- graceful shutdown uses a bounded timeout so HTTP shutdown and internal event-publisher fan-out can complete deterministically after a stop signal

## Core
- `HTTP_LISTEN_ADDR` (default `:8080`)
- `HTTP_REQUEST_BODY_LIMIT_BYTES` (default `4194304`)
- `LOG_LEVEL` (default `info`)
- `HUB_ID` (optional UUID bootstrap or reset value for the persisted hub identity)
- `HUB_LABEL` (optional bootstrap or reset value for the persisted human-readable hub label)
- `RESET_HUB_ID` (`true`/`false`, default `false`; when `true`, overwrite stored hub metadata with explicitly supplied env values)
- `POSTGRES_URL` (default `postgres://postgres:postgres@localhost:5432/openrtls?sslmode=disable`)
- `MQTT_BROKER_URL` (default `tcp://localhost:1883`)
- `WEBSOCKET_WRITE_TIMEOUT` (duration, default `5s`)
- `WEBSOCKET_READ_TIMEOUT` (duration, default `1m`)
- `WEBSOCKET_PING_INTERVAL` (duration, default `30s`)
- `WEBSOCKET_OUTBOUND_BUFFER` (default `32`)

Hub metadata bootstrap behavior:
- the hub persists one durable metadata row in Postgres containing the stable `hub_id` and operator-facing label
- on first startup, `HUB_ID` seeds that row when provided; otherwise the hub generates a UUIDv7
- on first startup, `HUB_LABEL` seeds that row when provided; otherwise the hub defaults to the machine hostname and falls back to `open-rtls-hub` if hostname lookup is unavailable
- on later startups, the stored row is the source of truth when `HUB_ID` and `HUB_LABEL` are omitted
- if supplied env values disagree with the stored row, startup fails with a clear mismatch error unless `RESET_HUB_ID=true`
- when `RESET_HUB_ID=true`, only explicitly supplied values overwrite the stored row; omitted fields are preserved

HTTP request decoding behavior:
- JSON request bodies are capped by `HTTP_REQUEST_BODY_LIMIT_BYTES` before decode work proceeds
- the REST/RPC handler layer accepts exactly one JSON document per request body and rejects trailing JSON tokens
- unknown JSON object fields remain allowed so forwards-compatible clients are not rejected solely for extension data

## Stateful Processing
- `STATE_LOCATION_TTL` (duration, default `10m`)
- `STATE_PROXIMITY_TTL` (duration, default `5m`)
- `STATE_DEDUP_TTL` (duration, default `2m`)
- `METADATA_RECONCILE_INTERVAL` (duration, default `30s`)
- `RPC_TIMEOUT` (duration, default `5s`)
- `RPC_ANNOUNCEMENT_INTERVAL` (duration, default `1m`)
- `RPC_HANDLER_ID` (default `open-rtls-hub`)
- `COLLISIONS_ENABLED` (`true`/`false`, default `false`)
- `COLLISION_STATE_TTL` (duration, default `2m`)
- `COLLISION_COLLIDING_DEBOUNCE` (duration, default `5s`)

Stateful ingest behavior:
- duplicate location/proximity payloads inside `STATE_DEDUP_TTL` are suppressed in the in-memory processing state before fan-out work
- latest provider-source location state, trackable latest-location state, proximity hysteresis state, fence membership state, and collision pair state are all kept in process memory with the configured expiry semantics
- metadata is loaded from Postgres at startup, updated immediately after successful CRUD writes, and reconciled in the background every `METADATA_RECONCILE_INTERVAL`
- durable hub metadata is also loaded from Postgres at startup before the service begins accepting traffic
- WebSocket delivery uses a per-connection outbound queue capped by `WEBSOCKET_OUTBOUND_BUFFER`; slow subscribers are disconnected instead of backpressuring the ingest path
- WebSocket liveness uses server ping frames every `WEBSOCKET_PING_INTERVAL` and considers the connection stale when no inbound message or pong arrives before `WEBSOCKET_READ_TIMEOUT`
- the `metadata_changes` WebSocket topic emits lightweight `{id,type,operation,timestamp}` notifications for zone, fence, trackable, and location-provider CRUD or reconcile drift
- when `COLLISIONS_ENABLED=true`, the hub evaluates trackable-versus-trackable collisions from the latest WGS84 motion state and keeps short-lived collision pair state in memory for `COLLISION_STATE_TTL`
- `COLLISION_COLLIDING_DEBOUNCE` limits repeated `colliding` emissions for already-active pairs

RPC behavior:
- `RPC_TIMEOUT` is the default wait time for request-response style RPC calls when the client does not supply `_timeout`
- `RPC_ANNOUNCEMENT_INTERVAL` controls how often the hub republishes retained MQTT availability announcements for hub-owned methods
- `RPC_HANDLER_ID` is the handler identifier announced for hub-owned RPC methods and the identifier clients may use with `_handler_id` to target the hub directly
- `com.omlox.identify` returns the persisted hub label as `name` plus the stable `hub_id`

## Proximity Resolution
- `PROXIMITY_RESOLUTION_ENTRY_CONFIDENCE_MIN` (number, default `0`)
- `PROXIMITY_RESOLUTION_EXIT_GRACE_DURATION` (duration, default `15s`)
- `PROXIMITY_RESOLUTION_BOUNDARY_GRACE_DISTANCE` (number, default `2`)
- `PROXIMITY_RESOLUTION_MIN_DWELL_DURATION` (duration, default `5s`)
- `PROXIMITY_RESOLUTION_POSITION_MODE` (default `zone_position`; supported value: `zone_position`)
- `PROXIMITY_RESOLUTION_FALLBACK_RADIUS` (number, default `0`)
- `PROXIMITY_RESOLUTION_STALE_STATE_TTL` (duration, default `10m`)

Proximity resolution behavior:
- proximity updates are resolved to a zone before the hub emits a derived `Location`
- the first valid proximity observation enters immediately
- the hub keeps the current zone for a short grace period to reduce flapping between nearby zones
- zone-specific overrides may be supplied through `Zone.properties.proximity_resolution`
- `Proximity.properties` is preserved into derived location metadata but does not override configured policy

## Auth
- `AUTH_ENABLED` (`true`/`false`, default `true`)
- `AUTH_MODE` (`none|oidc|static|hybrid`, default `none`)
- `AUTH_ISSUER` (OIDC issuer URL)
- `AUTH_AUDIENCE` (comma-separated)
- `AUTH_ALLOWED_ALGS` (comma-separated, default `RS256`)
- `AUTH_STATIC_PUBLIC_KEYS` (comma-separated PEM blocks or JWKS URLs)
- `AUTH_CLOCK_SKEW` (duration, default `30s`)
- `AUTH_OIDC_REFRESH_TTL` (duration, default `10m`)
- `AUTH_HTTP_TIMEOUT` (duration, default `5s`)
- `AUTH_PERMISSIONS_FILE` (YAML path, default `config/auth/permissions.yaml`)
- `AUTH_ROLES_CLAIM` (JWT claim used for role extraction, default `groups`)
- `AUTH_OWNED_RESOURCES_CLAIM` (JWT object claim for owned resource IDs; see `docs/auth.md` for format and usage)

See [docs/auth.md](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/auth.md) for the full auth model, Dex setup, and permission file format.

## RPC Security Defaults

For production deployments:
- keep `AUTH_ENABLED=true`
- treat `GET /v2/rpc/available` as sensitive because it reveals reachable control functions
- use per-method RPC permissions in the auth registry to control who may invoke which methods
- grant `com.omlox.core.xcmd` only to tightly controlled operator or automation roles
- keep direct MQTT broker access limited to the hub and trusted device/adaptor components
