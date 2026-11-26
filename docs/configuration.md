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
- `MQTT_HANDLER_WORKERS` (positive integer, default `4`)
- `MQTT_HANDLER_BUFFER` (positive integer, default `1024`)
- `WEBSOCKET_WRITE_TIMEOUT` (duration, default `5s`)
- `WEBSOCKET_READ_TIMEOUT` (duration, default `1m`)
- `WEBSOCKET_PING_INTERVAL` (duration, default `30s`)
- `WEBSOCKET_OUTBOUND_BUFFER` (default `256`)
- `EVENT_BUS_SUBSCRIBER_BUFFER` (default `1024`)
- `NATIVE_LOCATION_BUFFER` (default `2048`)
- `DERIVED_LOCATION_BUFFER` (default `1024`)
- `PPROF_ENABLED` (`true`/`false`, default `false`)
- `PPROF_MUTEX_PROFILE_FRACTION` (default `0`; `0` disables mutex contention sampling)
- `PPROF_BLOCK_PROFILE_RATE` (default `0`; `0` disables block profiling)

Hub metadata bootstrap behavior:
- the hub persists one durable metadata row in Postgres containing the stable `hub_id` and operator-facing label
- on first startup, `HUB_ID` seeds that row when provided; otherwise the hub generates a UUIDv7
- on first startup, `HUB_LABEL` seeds that row when provided; otherwise the hub defaults to the machine hostname and falls back to `open-location-hub` if hostname lookup is unavailable
- on later startups, the stored row is the source of truth when `HUB_ID` and `HUB_LABEL` are omitted
- if supplied env values disagree with the stored row, startup fails with a clear mismatch error unless `RESET_HUB_ID=true`
- when `RESET_HUB_ID=true`, only explicitly supplied values overwrite the stored row; omitted fields are preserved

HTTP request decoding behavior:
- JSON request bodies are capped by `HTTP_REQUEST_BODY_LIMIT_BYTES` before decode work proceeds
- the REST/RPC handler layer accepts exactly one JSON document per request body and rejects trailing JSON tokens
- unknown JSON object fields remain allowed so forwards-compatible clients are not rejected solely for extension data

### Kafka shadow ingest

Set `KAFKA_SHADOW_BROKERS` to a comma-separated list of Kafka `host:port`
addresses to mirror validated location and proximity observations. Provision
`KAFKA_SHADOW_TOPIC` before starting the Hub. Its default is
`location-hub.observations.v1`. The Hub verifies the topic at startup and the
producer waits for all in-sync replicas to acknowledge each mirrored record.
The mirror is asynchronous, with 16,384 buffered records, a 32 MiB buffer and
a 15-second delivery timeout. A full buffer or failed append increments the
shadow failure metrics. The existing ingest and decision path remains active.

The broker connection verifies TLS by default. Use `KAFKA_SHADOW_CA_FILE` for a
private CA. Set `KAFKA_SHADOW_CLIENT_CERT_FILE` and
`KAFKA_SHADOW_CLIENT_KEY_FILE` together for mTLS, or supply
`KAFKA_SHADOW_USERNAME` and `KAFKA_SHADOW_PASSWORD` for SCRAM-SHA-256. Use
`KAFKA_SHADOW_SCRAM_MECHANISM=SCRAM-SHA-512` for a Strimzi SCRAM listener. Use
`KAFKA_SHADOW_INSECURE_LOCAL=true` only with the isolated development broker.
The local Compose overlay defaults every event log to six hours (21,600,000
milliseconds). Set `KAFKA_LOG_RETENTION_MS` to change the shared default, or
`KAFKA_SHADOW_RETENTION_MS` to override the observation log alone. Retention
uses broker append time and expires whole Kafka segments. These settings
apply at topic creation. Use the broker's provisioning workflow to change
existing topics.

### Asset input router

Start `open-location-hub asset-router` as a separate process. It uses the same
`KAFKA_SHADOW_*` connection settings. Provision `KAFKA_ASSET_INPUT_TOPIC`
(default `location-hub.asset-inputs.v1`) and grant the worker read access to the
observation topic, write access to the output topic, and Kafka permissions for
its consumer group and transactional ID. `KAFKA_ASSET_ROUTER_GROUP` defaults to
`location-hub.asset-router.v1`. Set `KAFKA_ASSET_ROUTER_INSTANCE` to a unique
instance name when colocating workers on one host. Otherwise use the hostname.
Use the same group across replicas. The router stops when a committed offset
has expired, so the operator can establish a new processing boundary explicitly.

Use `just compose-shadow-router` after the local shadow stack has started.
`KAFKA_ASSET_INPUT_RETENTION_MS` overrides the local six-hour default before topic
creation. The observation and asset input topics have independent retention.

### Asset motion estimator

Start `open-location-hub asset-estimator` or run `just compose-shadow-estimator`
after the shadow stack and asset router. It uses `KAFKA_SHADOW_*` connection
settings, `KAFKA_ASSET_INPUT_TOPIC` as input and `KAFKA_ESTIMATE_TOPIC`
(`location-hub.motion-estimates.v1`) as its output. The estimator writes current
filter state to `KAFKA_ESTIMATOR_STATE_TOPIC`
(`location-hub.estimator-state.v1`). Provision all three topics with equal,
fixed partition counts. The checkpoint topic requires `cleanup.policy=compact`.
It serves as a current-state store across idle periods. The estimate event log
defaults to six-hour retention in Compose, adjustable through
`KAFKA_ESTIMATE_RETENTION_MS`.

Use a dedicated checkpoint topic per estimator deployment. The consumer group
name is the checkpoint topic followed by `.estimator`. Set
`KAFKA_ESTIMATOR_INSTANCE` when two replicas share a hostname. Otherwise the
hostname uniquely identifies the producer within the group. Grant the worker
write access to the estimate topic, read and write access to the checkpoint
topic, read access to the input topic, and access to the group and its
transactional IDs. The worker verifies the checkpoint topic's configuration.
A new deployment starts at the earliest retained input. An existing deployment
stops on an expired offset or an incompatible checkpoint frontier. Restore an
aligned input and state generation or bootstrap a new checkpoint topic from an
explicitly chosen retained input range before resuming.

### Configuration relay

Apply the database migrations and start the Hub once to initialize its persisted
identity. Run `open-location-hub config-relay` or
`just compose-shadow-config-relay`. The worker requires access to `POSTGRES_URL`,
uses the `KAFKA_SHADOW_*` connection settings, and publishes to
`KAFKA_CONFIG_TOPIC` (`location-hub.configuration.v1`). Grant it Kafka write
permissions on that topic and on its transactional ID,
`location-hub.config-relay.<hub-id>`. Run one relay per Hub identity.

`KAFKA_CONFIG_RETENTION_MS` overrides the common event-log retention for that
topic. `HUB_CONFIG_HISTORY` controls how long PostgreSQL retains already
published outbox rows and defaults to `6h`. Unpublished revisions are held
through a broker outage and delivered in revision order after recovery.

## Stateful Processing
- `STATE_LOCATION_TTL` (duration, default `10m`)
- `STATE_PROXIMITY_TTL` (duration, default `5m`)
- `STATE_DEDUP_TTL` (duration, default `2m`)
- `METADATA_RECONCILE_INTERVAL` (duration, default `30s`)
- `RPC_TIMEOUT` (duration, default `5s`)
- `RPC_ANNOUNCEMENT_INTERVAL` (duration, default `1m`)
- `RPC_HANDLER_ID` (default `open-location-hub`)
- `COLLISIONS_ENABLED` (`true`/`false`, default `false`)
- `COLLISION_STATE_TTL` (duration, default `2m`)
- `KALMAN_FILTER_ENABLED` (`true`/`false`, default `false`)
- `KALMAN_LOCATION_MAX_POINTS` (default `8`)
- `KALMAN_LOCATION_MAX_AGE` (duration, default `10s`)
- `KALMAN_EMIT_MAX_FREQUENCY_HZ` (number, default `0`; `0` means unlimited)

Stateful ingest behavior:
- inbound MQTT handlers share a bounded queue and worker pool; `MQTT_HANDLER_WORKERS` limits active callbacks and `MQTT_HANDLER_BUFFER` limits queued messages
- a full MQTT handler queue drops new work, records a runtime drop with `stage=mqtt_handler` and `reason=queue_full`, and logs the first and every hundredth drop; QoS 1 acknowledgment does not guarantee application processing after a queue drop
- MQTT client shutdown cancels active handler contexts and abandons queued messages
- duplicate location/proximity payloads inside `STATE_DEDUP_TTL` are suppressed in the in-memory processing state before fan-out work
- latest provider-source location state, trackable latest-location state, proximity hysteresis state, fence membership state, and collision pair state are all kept in process memory with the configured expiry semantics
- metadata is loaded from Postgres at startup, updated immediately after successful CRUD writes, and reconciled in the background every `METADATA_RECONCILE_INTERVAL`
- durable hub metadata is also loaded from Postgres at startup before the service begins accepting traffic
- WebSocket delivery uses a per-connection outbound queue capped by `WEBSOCKET_OUTBOUND_BUFFER`; when that queue fills, outbound payloads are dropped instead of backpressuring ingest or disconnecting the subscriber
- WebSocket liveness uses server ping frames every `WEBSOCKET_PING_INTERVAL` and considers the connection stale when no inbound message or pong arrives before `WEBSOCKET_READ_TIMEOUT`
- internal event-bus subscribers such as MQTT and WebSocket consume behind `EVENT_BUS_SUBSCRIBER_BUFFER`
- native location publication is queued behind `NATIVE_LOCATION_BUFFER` so ingest can decouple from transport fan-out
- post-native decision work such as Kalman normalization, alternate-CRS publication, geofence evaluation, and collision evaluation is queued behind `DERIVED_LOCATION_BUFFER`
- when the native, decision, event-bus, or outbound socket queues are full, the hub drops newer work on those non-critical paths instead of slowing raw location ingest
- the `metadata_changes` WebSocket topic emits lightweight `{id,type,operation,timestamp}` notifications for zone, fence, trackable, and location-provider CRUD or reconcile drift
- when `PPROF_ENABLED=true`, the auth-protected `/debug/pprof/*` handlers are registered on the main HTTP server for local profiling
- `PPROF_MUTEX_PROFILE_FRACTION` and `PPROF_BLOCK_PROFILE_RATE` directly control the Go runtime mutex and blocking profilers so replay or soak runs can capture contention evidence without patching the binary
- when `COLLISIONS_ENABLED=true`, the hub evaluates trackable-versus-trackable collisions from the latest active WGS84 motion state and retains observations for `COLLISION_STATE_TTL`; active pairs persist until their specification-defined separation or timeout
- when `KALMAN_FILTER_ENABLED=true`, the decision stage keeps short-lived per-trackable filter state plus a bounded retained sample history in memory
- `KALMAN_LOCATION_MAX_POINTS` caps the retained history per trackable; `KALMAN_LOCATION_MAX_AGE` also drops stale samples and resets the filter when the gap between accepted samples exceeds that age window
- Kalman normalization only affects the derived decision path for trackable-associated locations; native/raw publication remains unchanged
- normalized derived locations may populate OMLOX-compatible `course` and `speed` from track movement and may add hub extension properties such as `kalman_normalized` and `kalman_vertical_speed`
- `KALMAN_EMIT_MAX_FREQUENCY_HZ` throttles only derived location and trackable-motion publication; geofence and collision decisions still use every accepted normalized point even when publication is suppressed
- collision work uses only the normalized WGS84 motion state; local-only streams without a safe WGS84 transform do not participate in collision evaluation
- collision thresholds are expressed in meters
- `Trackable.radius` defines circular extent in meters and defaults to zero, as required by the specification
- WGS84 collision checks use a cheap short-range planar approximation that converts lon/lat deltas to approximate meters before threshold comparison; this favors hot-path throughput over geodesic precision
- fallback collision geometry for trackables without explicit polygon geometry uses the same meter-based approximation so emitted geometry remains consistent with the runtime threshold model
- every significant update that still intersects emits `colliding`; there is no collision notification debounce
- provider timeout/tolerance values override trackable values; the two sides contribute their maximum timeout and summed exit tolerance
- collision exit timers run autonomously; returning to intersection cancels pending separation

RPC behavior:
- `RPC_TIMEOUT` is the default wait time for request-response style RPC calls when the client does not supply `_timeout`
- `RPC_ANNOUNCEMENT_INTERVAL` controls how often the hub republishes retained MQTT availability announcements for hub-owned methods
- `RPC_HANDLER_ID` is the handler identifier announced for hub-owned RPC methods and the identifier clients may use with `_handler_id` to target the hub directly
- `com.omlox.identify` returns the persisted hub label as `name` plus the stable `hub_id`

## Proximity Resolution
- `PROXIMITY_RESOLUTION_ENTRY_CONFIDENCE_MIN` (number, default `0`)
- `PROXIMITY_RESOLUTION_EXIT_GRACE_DURATION` (duration, default `0s`)
- `PROXIMITY_RESOLUTION_BOUNDARY_GRACE_DISTANCE` (number, default `0`)
- `PROXIMITY_RESOLUTION_MIN_DWELL_DURATION` (duration, default `0s`)
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

See [docs/auth.md](auth.md) for the full auth model, Dex setup, and permission file format.

## Telemetry
- `OTEL_ENABLED` (`true`/`false`, default `false`)
- `OTEL_METRICS_ENABLED` (`true`/`false`, default `true` when telemetry is enabled)
- `OTEL_TRACES_ENABLED` (`true`/`false`, default `true` when telemetry is enabled)
- `OTEL_LOGS_ENABLED` (`true`/`false`, default `true` when telemetry is enabled)
- `OTEL_EXPORTER_OTLP_ENDPOINT` (base OTLP HTTP endpoint such as `http://localhost:4318`)
- `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` (optional full traces endpoint override)
- `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` (optional full metrics endpoint override)
- `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` (optional full logs endpoint override)
- `OTEL_EXPORTER_OTLP_HEADERS` (comma-separated `key=value` headers for collector auth or routing)
- `OTEL_EXPORTER_OTLP_INSECURE` (`true`/`false`, default `false`)
- `OTEL_EXPORTER_OTLP_TIMEOUT` (duration, default `10s`)
- `OTEL_METRIC_EXPORT_INTERVAL` (duration, default `30s`)
- `OTEL_METRIC_EXPORT_TIMEOUT` (duration, default `10s`)
- `OTEL_TRACE_SAMPLE_RATIO` (number between `0` and `1`, default `1`)
- `OTEL_SERVICE_NAME` (default `open-location-hub`)
- `OTEL_SERVICE_VERSION` (optional override for release tagging)
- `OTEL_DEPLOYMENT_ENVIRONMENT` (optional deployment environment label such as `local-demo` or `production`)
- `OTEL_DEBUG_IDENTIFIERS` (`true`/`false`, default `false`)

Telemetry behavior:
- the hub exports OTLP metrics, traces, and logs directly to a collector and does not expose a separate Prometheus scrape endpoint in this slice
- `service.name`, `service.version`, `deployment.environment.name`, and the persisted `hub.id` are attached to the OTel resource when available. Each process has a unique `service.instance.id` unless explicitly supplied as an OpenTelemetry resource attribute.
- invalid telemetry configuration fails startup only when telemetry is enabled
- metrics are intentionally low-cardinality and are labeled only with bounded dimensions such as transport, signal type, stage, feature, and outcome
- entity identifiers such as `trackable_id`, `provider_id`, `zone_id`, `fence_id`, and collision pair identifiers are emitted on spans and structured logs for drill-down, not on normal metric series
- runtime metrics cover ingest acceptance and deduplication, end-to-end processing latency, queue occupancy and wait time, queue and outbound drop counters, fence evaluation, collision evaluation, MQTT/WebSocket publication, metadata reconcile, auth, and RPC outcomes
- the [local demo stack](../local-hub/README.md) sends OTLP metrics to Prometheus via a private Collector and provisions Perses dashboards from Git. The demo writes application logs to stdout. Other deployments can configure trace and log exporters as well.

## RPC Security Defaults

For production deployments:
- keep `AUTH_ENABLED=true`
- treat `GET /v2/rpc/available` as sensitive because it reveals reachable control functions
- use per-method RPC permissions in the auth registry to control who may invoke which methods
- grant `com.omlox.core.xcmd` only to tightly controlled operator or automation roles
- keep direct MQTT broker access limited to the hub and trusted device/adaptor components


The MQTT client requires a broker supporting MQTT 5. RPC availability is retained
with a 120-second expiry. Proximity grace and dwell behavior is opt-in; the default
uses the zone in each observation immediately. Before applying migration 00003,
resolve any duplicate nonempty zone `foreign_id` values in an existing database.
