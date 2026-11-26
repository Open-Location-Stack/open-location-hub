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
- Metadata writers serialize snapshot preparation and take the reader lock only to publish the completed snapshot. A fence edit reprojects that fence and reuses other world geometries; zone changes rebuild world geometry. Spatial trees are rebuilt from the resulting snapshot. Readers continue on the previous immutable snapshot during preparation.
- Fence candidate lookup uses fixed cells within each coordinate scope. Fences
  that overlap several cells are indexed in each one and matched once per
  query. A scope-wide index handles large footprints, while a separate index
  keeps large fences visible to ordinary cell queries. Exact geometry, floor,
  height and active membership checks still use the existing rules.
- Provider location reads use a provider-to-source index maintained on writes, deletion, and expiry. Provider-to-trackable reads use the metadata assignment index.
- A background reconcile loop reloads durable metadata periodically and emits the same `metadata_changes` notifications when it detects out-of-band create, update, or delete drift.
- Decision-critical ingest state is kept in process memory:
  - dedup windows
  - latest provider-source locations
  - latest trackable locations and active motion state used for collision work
  - optional per-trackable Kalman filter state and retained samples
- proximity hysteresis state
  - fence membership state, including transient exit-tolerance and exit-delay bookkeeping
  - collision pair state

## Event Fan-Out

An optional Kafka shadow producer copies validated REST, MQTT and WebSocket
observations to a topic keyed by origin hub, provider and source. The
[Protobuf envelope](../specifications/events/observation.v1.proto) carries the
source timestamp when supplied, receive time, transport and OMLOX JSON payload.
Its content-derived ID groups exact retry payloads and has the same value for
indistinguishable repeated samples. Compare Kafka appends with the existing
processing path using the shadow outcome metrics. Kafka is currently a shadow
sink. The path below still owns state, decisions and acknowledgements.

The optional `asset-router` process consumes the mirrored log and writes one
input per associated trackable to a second Kafka topic. Each asset has a stable
partition key derived from origin hub and trackable ID. This co-locates inputs
from multiple providers. The Kafka transaction commits the consumed offsets and
the asset input records together. Association is captured at ingress during
this phase. Asset order reflects arrival in the output log, with event-time
reordering still needed in the estimator. Unassociated observations remain in
the original log for provider-scoped processing.

The Kafka `asset-estimator` worker consumes those asset inputs and emits motion
estimates on the same partition number. It selects strictly increasing event
times, falls back to receipt time when the source gives none, and uses a
constant-velocity filter with position and velocity covariance. WGS84 motion
is filtered in Earth-centered metric coordinates. Local motion is filtered in
the source frame. The filter resets on frame changes and gaps over 60 seconds.
Estimates include the asset sequence, source event identity, uncertainty and,
for a continuing frame, the preceding position and horizontal velocity.

The worker commits estimates, current asset checkpoints and the consumed
offsets in one Kafka transaction. Each partition restores its compacted current
state before it can process inputs. Its saved frontier must match the input
topic generation and the group's committed offset. The event topics keep the
normal time limit. The checkpoint store keeps only current state independently
of event expiry. The worker runs on the shadow path. Revision-aware mode
compiles the same locating rules as the compatibility path.

Each estimator replica captures Kafka offsets before reading a
repeatable-read PostgreSQL snapshot. It assembles the configuration fragments
in commit order and activates a complete revision by swapping an immutable
snapshot. An update copies only affected cache shards. The processing path
loads one snapshot for each batch without a configuration lock or a
PostgreSQL lookup. The Kafka consumer group gives one replica ownership of
each asset's partition. Decoded candidates stay with that partition and
recover from the checkpoint log after reassignment.

Estimates identify their configuration revision, trigger and selected sample.
A rule change can select an older sample from a different source and reset
the filter while the asset sequence advances. Configuration becomes visible
on the next asset input. Stationary-asset reevaluation, provider association
changes after ingress, bounded reordering and exact replay of the original
revision assignments still require additional stages.

Optional shadow fence routing indexes the committed geometry in fixed spatial
cells and caches native and projected copies. The estimator routes each
trackable estimate to nearby fences and to fences it may still occupy. Its
checkpoint retains those interests so crossing multiple cells or changing
estimator replicas still reaches the previously visited fence. Each fence and
trackable pair maps to a stable Kafka partition. The selected location, fence
geometry and effective timer policy travel with the configuration revision.
The estimate, fence inputs and checkpoint commit together.

The `fence-worker` role evaluates exact geometry and the OMLOX exit timers
for its membership partitions. Each worker caches decoded fence geometry.
It keeps only active memberships in the compacted checkpoint and maintains
one scheduled deadline per active membership. A due deadline appends a hint
to the same partition as its observations. The hint carries the generation of
the membership that scheduled it, so retries and previous owners cannot
repeat an exit. A new owner restores live memberships and pending deadlines.
It commits entry and exit records, membership checkpoints and input offsets
in a single Kafka transaction.

The transition log is available for comparison. The compatibility path still
owns public events, provider-only memberships and stationary re-evaluation.
The shadow worker orders timer hints with evaluations that have reached the
fence log. A delayed upstream estimate can reach that log after a deadline
hint has committed. Measure upstream lag and transition parity before
selecting the public event path.

Optional collision routing projects eligible trackable estimates to WGS84 and
maps their current extent to Earth-centered metric cells. The estimator
checkpoints the prior cell coverage and sends a withdrawal, with the new
estimate, to every cell it leaves. The estimate and its cell inputs commit
together. Cell workers maintain an incremental index for each owned cell.
A worker searches both the previous and current boxes, so it can still find
a prior neighbor after the updated asset jumps out of shared coverage. The
worker snapshots only current asset samples and restores the index and
expiry schedule after partition reassignment.

Cell workers publish ordered pair keys with the two sample versions as shadow
candidates. Multiple cells may emit the same version pair under the same
candidate ID. Exact contact, collision timers and public delivery remain on
the compatibility path. A later pair worker must merge candidates by both
asset sequences, including when two cells observe the samples in different
order.

The configuration relay publishes changes to zones, fences, trackables and
location providers from a PostgreSQL outbox. It emits one revision per
committed transaction, including SQL inserts, updates and deletes. All
parts of a revision share a Kafka key and commit together in Kafka. If the
relay stops after that commit and before the database acknowledgement,
it may publish the same revision again. The Hub ID, revision number and
bundle digest identify those deliveries. The revision-aware estimator rejects
gaps and conflicting duplicates.

Kafka event logs support short-term delivery and replay. The Compose defaults
give each log a six-hour window and allow independent overrides. A future
consumer could persist selected events to S3-compatible storage with its own
retention policy.

1. REST, MQTT, or WebSocket ingest enters the shared hub service. MQTT callbacks enqueue messages for `MQTT_HANDLER_WORKERS` workers behind `MQTT_HANDLER_BUFFER`; full queues drop new messages with a runtime drop metric and a throttled warning. The broker receive loop stays available for publish acknowledgments.
2. The hub validates, normalizes, deduplicates, and updates in-memory transient state on the ingest path.
3. A buffered native-publication stage emits native provider location events. Trackable motion publication waits for location selection.
4. A second buffered decision stage publishes alternate provider projections independently, then selects each trackable's significant location using locating rules and generated timestamps. Selected locations pass through optional Kalman normalization before motion publication, geofence evaluation, and collision preparation.
5. Decision work is sharded by provider/source. Candidate evaluation and selected output are protected by bounded per-trackable lock stripes, so two providers for the same trackable cannot overwrite each other's decisions concurrently. Unrelated trackables can proceed independently.
6. The sharded decision workers drain queued locations in bounded batches before processing them so bursty ingest spends less time on per-item queue churn.
7. Kalman throttling applies to selected trackable motion publication. Raw provider streams remain independent; fencing and collision work still run for selected normalized observations.
8. Collision evaluation runs as its own downstream stage fed from the decision output so pairwise collision work does not block the rest of the derived path.
9. Collision work evaluates normalized WGS84 motions only; streams without a safe WGS84 representation do not enter collision evaluation.
10. MQTT and WebSocket consume the resulting internal event stream and publish transport-specific payloads in batches.
11. When any non-critical queue fills, the hub drops newer work on that path rather than backpressuring raw ingest.

Implications:
- ingest logic is shared across REST, MQTT, and WebSocket
- MQTT is no longer the only downstream publication path
- the internal event seam decouples downstream publication from MQTT-specific topics
- location ingest latency is protected from slower transport fan-out, geofence work, or collision work
- the decision stage retains candidates per trackable and provider/source, evaluates the highest matching rule priority, and uses newest generated time to resolve ties; without rules, only strictly newer generated timestamps move a trackable
- rule expressions are parsed and type-checked on writes, cached by trackable rule content, and evaluated without executable code; `timestamp_diff` is age in milliseconds
- `force_location_update=true` re-evaluates retained assigned-provider observations after trackable writes, without changing provider data or replaying an unchanged motion notification
- selected motion reads use their own cache and work when collisions are disabled; collision workers maintain a separate projected-motion cache
- switching a selected provider/source or selecting an older retained observation resets the optional Kalman filter to avoid blending unrelated coordinate frames
- when Kalman filtering is enabled, `trackable_motion` publication can be rate-limited independently from decision logic
- collision evaluation remains WGS84-only so downstream collision payloads stay in one coordinate space
- the collision worker maintains an incremental spatial index over active motions,
  with removal on expiry and administrative deletion. Candidate search uses the
  current metadata snapshot's radius bound. Active pairs are still checked when
  an asset jumps out of candidate range.
- pending internal events resume delivery when subscribers make room, even if no further events arrive; unsubscribe stops the flusher and synchronizes channel closure with concurrent emitters
- WebSocket subscriptions match topic and observation filters before projection; a broadcast batch shares projections for each event, CRS, and target zone across connections
- lagging internal subscribers coalesce hot `location` and `trackable_motion` events to the latest value per object instead of dropping them immediately, while discrete fence/collision/metadata edges remain non-coalesced
- WebSocket fan-out coalesces multiple internal events into fewer wrapper messages and drops outbound payloads for slow subscribers instead of tearing the connection down immediately
- hub-issued UUIDs for REST-managed resources, derived fence/collision events, and RPC caller IDs now use UUIDv7 so emitted identifiers are time-sortable
- internal hub events carry the persisted `origin_hub_id` so downstream transports preserve source provenance
- fence membership follows the published three-timer transition table; an indexed deadline scheduler emits exits without requiring another location update, using provider settings before trackable settings before fence settings

## Observability Boundaries
- `internal/observability` owns OpenTelemetry resource setup, OTLP exporters, lifecycle management, and the small internal instrumentation API used by the rest of the runtime.
- The Hub exports OTLP to an OpenTelemetry Collector. In the demo it sends metrics through the Collector to Prometheus. Perses queries Prometheus, and Alertmanager receives alerts evaluated by Prometheus.
- Transport handlers attach ingest transport context at entry so REST, MQTT, and WebSocket traffic create one shared root ingest span shape before entering `internal/hub`.
- The hot path records low-cardinality metrics for accepted, deduplicated, rejected, and failed ingest, queue depth and wait time, stage latency, event-bus fan-out, MQTT publish, WebSocket dispatch, RPC execution, and end-to-end processing time.
- HTTP request timings use route templates. Fence membership, collision pairs and proximity hysteresis each record evaluations by trigger and outcome. Worker metrics separate item handling time, queue wait and Kafka append or commit time. Heartbeats distinguish idle processes from missing telemetry.
- Child spans isolate proximity resolution, native publication, decision processing, fence evaluation, collision evaluation, metadata reconcile, auth, and runtime dependency work so slow stages can be inspected directly.
- Asset-, provider-, zone-, and fence-centric identifiers belong on traces and structured logs only. They are intentionally excluded from normal metric labels so dashboards remain queryable under sustained ingest volume.
- Zap remains the application logging API. When OTLP logs are enabled, the logger tees into the OpenTelemetry bridge so the same structured events still appear locally while also being exported to the collector.
- Runtime drop counters and gauges for queue occupancy, event-bus subscribers, and WebSocket connections are exposed from `internal/hub` through observable instruments so the e2e stack can dashboard overload and degraded states without adding lock-heavy bookkeeping to the ingest path. Collision queue drops are counted by work batch and appear in runtime diagnostics and the stage-specific drop metric.
- Per-drop OTLP attribution is also exported via `hub.runtime.drop_events_total{stage,reason}` so dashboards can break overload down by bounded cause labels without relying on the local debug endpoint.
- For local troubleshooting, the hub also exposes an auth-protected `/debug/runtime/drops` endpoint that returns the current drop counters plus a bounded set of recent sampled drops with stage and object context.
- When `PPROF_ENABLED=true`, the same auth-protected HTTP surface also exposes `/debug/pprof/*`; optional runtime mutex and block profiler rates can be enabled from env so replay benchmarks can produce direct contention evidence instead of only throughput symptoms.
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
- HTTP clients talk to the hub, not directly to MQTT devices
- MQTT is restricted to the hub and trusted device/adaptor components
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


The 0.2 transport layer uses MQTT 5 with expiring RPC discovery. WebSocket
subscriptions select one location representation and then project into the
requested coordinate system. Native observations remain available through
`crs=local` without a target zone. Stored source metadata is preserved across
projection, and motion/collision geometry follows the same transform as positions.
