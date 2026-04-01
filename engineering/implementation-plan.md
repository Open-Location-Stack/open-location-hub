# Implementation Plan

This plan reflects the current repository state as verified on 2026-03-27 with targeted Go package tests, repository inspection, and the required repository workflow of `just bootstrap`, `just generate`, and `just check`, plus `just test-race` when race-detector coverage is needed. The standard Go test run is available both through the standalone `just test` command and as part of `just check`, so local workflows and GitHub Actions can share one package-selection path. On macOS, PROJ installation currently requires a repo-local shim, so coordinate transformations are not treated as a verified host-native path there. The GitHub Actions CI workflow now installs native Ubuntu packages for `just`, `pkg-config`, `libproj-dev`, and `proj-data` before lint, unit-test, race-test, and build steps so CRS-linked packages compile on hosted runners without relying on deprecated Node 20-backed setup actions. The Go module graph was refreshed to the latest stable compatible releases in this repository's dependency set, and the workflow action majors were updated to the latest stable tags currently available for checkout, Go setup, and cache. The contributor lint stack now also installs and pins `staticcheck` v0.7.0 and `govulncheck` v1.1.4.

The repository documentation is now split by audience: software/runtime documentation lives under `docs/`, while project-development and contributor-process documentation lives under `engineering/`.

## Current Status

### Completed and verified
- Project harness is in place: `justfile`, `Dockerfile`, `docker-compose.yml`, and runtime bootstrap in `cmd/hub/main.go`.
- The repository dependency baseline has been refreshed to current stable compatible Go module releases, and CI now targets the latest stable GitHub Action major tags for checkout, Go setup, and cache while installing `just` from Ubuntu packages.
- GitHub Actions now splits the regular verification path and the race-detector path into parallel jobs so the slowest repository checks do not serialize wall-clock time on every push or pull request.
- The normative OpenAPI contract exists in `specifications/openapi/omlox-hub.v0.yaml`, and generated server/types are present under `internal/httpapi/gen`.
- Public Go packages now include package-level and exported-symbol doc comments so `go doc` renders the operational API surface more usefully for maintainers.
- Repository-local Codex skills now explicitly require documentation updates during implementation work and provide a dedicated documentation skill for Go doc, OpenAPI, and implementation-facing docs.
- The HTTP server is wired with Chi routing, request logging, auth middleware, generated route registration, and a health endpoint.
- Auth foundations are implemented for `none`, `oidc`, `static`, and `hybrid` modes, including JWT validation, OIDC discovery/JWKS refresh, permissions loading, and ownership-aware authorization.
- Core REST CRUD is implemented for zones, providers, trackables, and fences through a shared service layer backed by Postgres and `sqlc`.
- Hub-generated UUIDs for REST-managed resources, derived fence and collision events, and JSON-RPC caller IDs now use UUIDv7 so newly issued identifiers are time-sortable.
- UUID generation is intentionally centralized behind `internal/ids` so the repository can switch from `github.com/google/uuid` to a future Go standard-library UUIDv7 implementation with a narrowly scoped change once that support is available and stable.
- Provider ingestion endpoints are implemented for locations and proximities, with in-memory deduplication, latest-state tracking, proximity hysteresis, fence membership, and collision state.
- Ingest accepts omitted `crs`, `local`, and named EPSG codes, derives true local and WGS84 variants when transformation is possible, and suppresses only the unavailable output topic when it is not.
- A shared internal event bus now fans normalized hub events out to MQTT and WebSocket consumers instead of keeping MQTT as the only outbound path.
- MQTT is broker-backed and wired into startup, inbound ingest topics, and outbound location, fence-event, trackable-motion, and optional collision publication.
- The mandatory OMLOX WebSocket surface is implemented at `GET /v2/ws/socket`, including wrapper events, runtime subscription IDs, topic fan-out, `params.token` authentication, dedicated WebSocket topic permissions, GeoJSON topic variants, and disabled-feature errors for collision topic access when collisions are turned off.
- WebSocket connection shutdown no longer closes the outbound send channel, so concurrent disconnect and fan-out paths now converge on the `done` signal instead of racing a send against channel close under `go test -race`.
- Collision support now exists as an explicitly optional feature controlled by `COLLISIONS_ENABLED`; when enabled, the hub emits bounded single-hub trackable-versus-trackable collision events in WGS84.
- RPC now operates as a control-plane surface: `GET /v2/rpc/available` and `PUT /v2/rpc` support hub-owned methods, MQTT-bridged methods, retained method discovery, and the `_all_within_timeout`, `_return_first_success`, and `_return_first_error` aggregation modes.
- Unit and integration coverage exist for config validation, auth, CRUD behavior, transient ingest state, CRS transformation/georeferencing behavior, MQTT topic mapping/publication, RPC bridge behavior, Dex-backed end-to-end authorization, and shared-hub traffic scenarios including multi-geofence movement validation, with `t.Parallel()` now enabled across the test suites after removing the remaining shared runtime-seam and process-env test coupling.
- The integration test harness now keeps HTTP response bodies open for decode assertions, retries Postgres migration startup briefly so CI tolerates transient container readiness races on hosted runners, auto-aligns Testcontainers with the active Docker context when local runtimes use non-default Unix sockets, and reuses one shared hub app image per integration test process so concurrent runs do not contend on redundant identical builds.
- The OpenAPI contract now includes clearer tag, operation, parameter, response, and schema descriptions for the current REST and RPC surface.
- The repository quality gates now include a dedicated `just test-race` target plus a deeper `just lint` stack that runs `go vet`, `staticcheck`, `govulncheck`, `go mod tidy`, and generated-file cleanliness checks for the OpenAPI and `sqlc` outputs.
- Test execution now favors parallelism across packages, tests, and selected subtests because the remaining environment and runtime-entry tests were refactored to use injected test-local dependencies instead of shared process-global state.
- The handwritten REST/RPC handler layer now hardens JSON request decoding with a shared body-size ceiling, single-document enforcement, and helper-level tests while still allowing unknown object fields for extension compatibility.
- The runtime entrypoint now uses a `run(ctx)` lifecycle with `signal.NotifyContext`, structured early-startup error returns instead of config/logger panics, and deterministic shutdown of the HTTP server plus the MQTT event-publisher goroutine from one root context.
- Adapter and runtime coverage is now broader: route-level `httptest` coverage exercises the REST handler surface across CRUD, ingest, fence, and RPC paths; the MQTT client has direct tests for reconnect hooks, publish/subscribe timeouts, broker errors, and disconnect behavior; and `cmd/hub` now has startup/shutdown smoke coverage plus startup failure-path assertions via injectable runtime seams.

### Implemented but still incomplete
- The persistence model stores canonical API payloads as JSON and only indexes a minimal set of fields; there is not yet richer filtering, search, or migration support for query-heavy workloads.
- Proximity ingestion now uses a stateful resolver that maps proximity updates to proximity-capable zones, applies anti-flap stickiness, and emits derived locations from the resolved zone position.
- Proximity resolution is still intentionally simple:
  - it uses the resolved zone's declared position rather than triangulation or sensor fusion
  - it does not support moving zones tied to a provider or trackable
  - it does not combine multiple simultaneous proximity observations into a richer confidence model
  - it does not yet share logic with trackable locating rules or fence tolerance behavior
- CRS transformation now exists for WGS84, projected EPSG inputs, and OMLOX local coordinates backed by zone ground control points, but it currently relies on a fitted 2D similarity model and does not yet attempt richer benchmark/anchor calibration.
- PROJ installation on macOS currently relies on a shimmed host setup. In practice that means coordinate-transformation behavior is not a verified macOS build path in the current repository state.
- Linux and Docker builds use native PROJ packages and are expected to work normally.
- GitHub Actions Ubuntu runners now explicitly install `just`, `pkg-config`, `libproj-dev`, and `proj-data` before the regular verification and race-test jobs, matching the documented Linux dependency model.
- CRS behavior is currently verified only through Linux/Docker-backed builds and tests.
- Fence processing is currently a simple in-process point-in-region check over latest locations; provider- and trackable-specific timeout semantics from the OMLOX text are not yet modeled in depth.
- MQTT publication and subscription use a QoS 1 baseline and reconnect behavior, but there is no explicit retry accounting or dead-letter handling.
- MQTT remains useful for local integration, but the intended architecture is that cross-hub federation uses REST and WebSocket rather than MQTT.
- RPC now publishes retained announcements for hub-owned methods and hosts local implementations of `com.omlox.ping`, `com.omlox.identify`, and `com.omlox.core.xcmd`, but `com.omlox.core.xcmd` still depends on a deployment-specific adapter before it can execute real device commands.
- MQTT method announcement support currently relies on retained publication without MQTT v5 message-expiry enforcement because the current client layer does not yet expose that broker feature cleanly.
- Observability remains log-centric; dependency readiness, metrics, and deeper operational diagnostics are still limited.
- The repository now has a dedicated `just test-race` target and CI step, and the RPC bridge test double has been synchronized so the package passes the Go race detector under the standard package-selection rules.
- Coverage is still lighter in observability and a few storage/runtime edge packages than in the core service and RPC packages, but the previous blind spots around the REST handler layer, MQTT client edges, process wiring, metadata cache diffs, and in-memory processing state now have direct failure-path coverage.
- WebSocket authorization and fan-out now exist, but the current topic-filter implementation is still intentionally simple and not yet tuned for high-cardinality subscriber counts or peer federation.
- Collision support is intentionally bounded and configuration-gated; it does not yet model cross-hub correlation, richer polygon semantics, or broader OMLOX collision behaviors beyond trackable-versus-trackable detection.
- Federation between OMLOX hubs is not yet modeled in configuration, auth, data provenance, or runtime behavior, so current deployments are effectively single-hub topologies.
- The runtime does not yet define a stable configured hub UUID for provenance, scoped identity, replay handling, and cross-hub routing.
- The current auth model covers user and caller authorization for one hub, but hub-to-hub service identities, multi-issuer trust, per-peer scopes, and propagated ownership/provenance rules are not yet designed or enforced.

## Completed Phases

### Phase 1: Core CRUD
Delivered:
- `queries/core.sql` and generated `sqlc` outputs now support create, get, list, update, and delete for zones, providers, trackables, and fences.
- `internal/hub/service.go` provides shared CRUD validation, payload normalization, and persistence mapping.
- `internal/httpapi/handlers/handlers.go` now serves real OpenAPI-shaped CRUD responses instead of `501 Not Implemented`.
- Integration coverage now proves authenticated CRUD behavior on the REST surface.

### Phase 2: Ingestion and transient state baseline
Delivered:
- `POST /v2/providers/locations` and `POST /v2/providers/proximities` are implemented.
- In-memory TTL configuration now governs latest state, proximity-derived state, dedup windows, metadata reconcile cadence, and RPC timeout.
- Ingestion uses a shared path so HTTP and MQTT inputs exercise the same core logic.
- Location ingest validation now accepts omitted `crs`, `local`, and named EPSG codes, with runtime transformation determining which derived outputs are publishable.
- CRS transformation uses PROJ-backed named CRS conversion plus zone `ground_control_points` fitting for local georeferencing.
- Proximity-derived locations still originate in local zone coordinates, but georeferenced zones now also emit derived WGS84 publication.
- Service-level and end-to-end tests cover round-trip transformation behavior, randomized and edge-case coordinate conversion, topic suppression when a derived variant is unavailable, and Mosquitto-backed live publish/subscribe assertions.
- Shared-hub scenario tests now cover concurrent REST location publishers with simultaneous MQTT and WebSocket subscribers, mixed REST and MQTT ingest against one running hub instance, and a ten-object path traversal across multiple arranged geofences so the highest-volume traffic shapes are exercised beyond isolated single-publisher flows while also checking fence entry/exit accuracy and latest motion updates.

### Phase 3: MQTT bridge baseline
Delivered:
- The MQTT client now connects to a real broker, subscribes, publishes, reconnects, and shuts down cleanly.
- Inbound MQTT location and proximity topics feed the shared ingestion service.
- Outbound MQTT publication now consumes the shared hub-event bus and exists for processed locations, fence events, trackable motions, and optional collision events.

Residual work:
- Document and enforce operational behavior for sustained publish failures, reconnect storms, and overloaded downstream consumers.
- Revisit MQTT v5 message-expiry support for retained RPC availability announcements if strict OMLOX expiry behavior becomes a deployment requirement.

### Phase 4: RPC baseline
Delivered:
- `GET /v2/rpc/available` exposes a unified registry of hub-owned and MQTT-discovered methods.
- `PUT /v2/rpc` validates OMLOX JSON-RPC extensions, applies per-method authorization, dispatches to local handlers and/or MQTT, and collects responses according to the supported aggregation modes.
- The hub now publishes retained MQTT availability announcements for `com.omlox.ping`, `com.omlox.identify`, and `com.omlox.core.xcmd`.
- Local handlers now exist for `com.omlox.ping`, `com.omlox.identify`, and `com.omlox.core.xcmd`, with `com.omlox.core.xcmd` routed through an adapter seam that currently returns a deterministic unsupported error when no adapter is configured.
- Unit coverage now includes method discovery, local handler dispatch, `_all_within_timeout`, `_return_first_error`, invalid-parameter handling, and per-method authorization checks.

Residual work:
- Add Mosquitto-backed end-to-end coverage for retained announcements, reconnect re-announcement behavior, and mixed local/external handler scenarios.
- Implement one or more real `com.omlox.core.xcmd` adapters for supported provider/core integrations.
- Extend RPC auditability and operational diagnostics beyond the current structured logs.

### Phase 5: WebSocket baseline
Delivered:
- `GET /v2/ws/socket` is implemented as the OMLOX WebSocket wrapper surface outside the REST OpenAPI contract.
- WebSocket subscriptions now consume the same normalized event stream as MQTT publication.
- WebSocket supports `location_updates`, `location_updates:geojson`, `proximity_updates`, `fence_events`, `fence_events:geojson`, `trackable_motions`, and configuration-gated `collision_events`.
- WebSocket now also supports subscribe-only `metadata_changes` notifications for zones, fences, trackables, and location providers.
- WebSocket message handling reuses the same location and proximity ingest paths as REST and MQTT.
- Topic-level WebSocket authorization now exists alongside route-level REST auth and RPC method auth.

Residual work:
- Deepen filter indexing and fan-out efficiency for high-cardinality subscriber counts.
- Add more end-to-end coverage for auth-enabled WebSocket flows once Docker-backed integration is consistently available in CI/local environments.
- Revisit topic/backpressure metrics and operational visibility beyond the current disconnect-on-overflow behavior.

## Remaining Work

### Phase 6: Production hardening
Scope:
- Expand observability with metrics, readiness checks, and richer failure diagnostics around auth, DB, metadata reconcile, MQTT, and RPC.
- Tighten startup validation for dependency reachability and misconfiguration beyond the current env validation.
- Evaluate whether repository scale and rule count now justify consolidating the current explicit lint commands under `golangci-lint` or a comparable aggregator without obscuring which checks actually gate CI.
- Review auth hardening gaps such as key rotation telemetry, operator-facing guidance, and clearer runtime failure visibility.
- Establish baseline performance checks for CRUD, ingest, MQTT publication, and RPC fan-out/fan-in behavior.
- Raise direct test coverage around runtime adapters and entrypoints, especially the REST handler layer, MQTT client edges, and process wiring, so regressions at package boundaries are caught earlier.
- Extend the same style of boundary-focused tests to observability logging middleware and the remaining low-coverage runtime packages so they have comparable regression resistance.

Exit criteria:
- The hub can be operated with clear visibility into failures, dependency health, expected throughput characteristics, and repository quality checks that exercise concurrency and adapter-boundary failure modes.

### Additional implementation depth
Scope:
- Add richer query/filter behavior for REST resources where OMLOX workflows benefit from more than list-by-created-time.
- Add stronger event modeling for fence timeouts, motion derivation, and future collision handling.
- Revisit the current 2D similarity-fit georeferencing model if OMLOX deployments require affine, anchor-assisted, or higher-order calibration.
- Revisit optional proximity depth such as mobile zones, richer confidence-based switching, and shared tolerance semantics with fences/trackable locating.
- Revisit the JSON-payload-first storage model if query volume or federation requirements demand more structured persistence.
- Add a documented federation model so hubs can push to or pull from other hubs over standard OMLOX APIs with explicit provenance, replication state, and loop suppression.
- Add change-feed and synchronization support for hub-managed resources so federated hubs can reconcile zones, providers, trackables, and fences without relying on blind polling alone.

Exit criteria:
- The implementation moves from an operational baseline to an OMLOX behavior model that is deeper, more scalable, and easier to evolve.

### Phase 7: Standards-facing feature completion
Scope:
- Close remaining MQTT extension gaps for enabled deployments, including clearer expiry behavior, topic-family completeness, and operational handling under reconnect/replay conditions.
- Deepen the currently bounded collision implementation and decide how much more OMLOX collision behavior belongs in the product scope.

Exit criteria:
- Another OMLOX-compliant client or hub can use the documented REST, WebSocket, and optional MQTT surfaces without relying on repository-specific shortcuts or undocumented gaps.

### Phase 8: Federation foundations
Scope:
- Define hub identity, peer identity, and peer configuration for upstream and downstream relationships.
- Add a required stable configured hub UUID and treat it as the origin namespace for federated resources, events, deduplication, and routed RPC.
- Support push and pull federation patterns over REST and WebSocket, with one clear source-of-truth rule per replicated data class.
- Add provenance metadata, replay cursors, deduplication keys, and loop suppression for federated events and resources.
- Extend auth design and implementation for machine-to-machine service identities, multi-issuer trust, per-peer scopes, and propagated ownership or tenancy boundaries.
- Define cloud-authoritative metadata distribution for facility metadata, zones, fences, and other replicated configuration so on-prem hubs can operate from synchronized local copies.
- Add a slave-mode deployment option where an instance subscribes to remote metadata updates, applies them into its local PostgreSQL store, and rejects direct local writes for cloud-owned metadata classes while still serving from the local in-memory snapshot.
- Add operational visibility for peer health, replication lag, retries, reconciliation state, and dead-letter conditions.

Exit criteria:
- An on-prem hub can federate selected OMLOX resources and event streams with a regional cloud hub securely and repeatably, and a regional hub can in turn forward or expose that data to an aggregate hub without losing provenance or policy control.

### Phase 9: Resource and event federation completion
Scope:
- Add snapshot and reconciliation flows for zones, providers, trackables, and fences.
- Add practical change feeds for CRUD-managed resources so pull-based federation does not depend only on periodic full scans.
- Add a concrete cloud-to-edge metadata sync model with authoritative ownership, versioning, tombstones, offline cache behavior, and scope-aware rollout by site, tenant, or region.
- Make the receiving side able to persist remotely subscribed metadata changes into local PostgreSQL so slave-mode hubs can restart from local state, reconcile gaps, and keep ingest/geofence/collision processing off the remote control plane.
- Federate normalized `Location`, `Proximity`, `FenceEvent`, `TrackableMotion`, and eventual collision-event traffic with idempotent apply semantics on the receiving hub.
- Define deletion, redaction, tenancy partitioning, and jurisdiction-aware forwarding behavior for regional and aggregate cloud deployments.
- Constrain RPC federation to explicitly allowed methods and audited forwarding paths once data-plane federation is stable.

Exit criteria:
- The hub supports realistic on-prem, regional, and aggregate federation topologies with standard OMLOX APIs as the main contract and documented extensions only where OMLOX leaves practical gaps.

## Issue Tracking

The remaining work is now tracked in GitHub issues with explicit priority labels plus the `0.1` milestone for work judged urgent for the first public alpha release targeted for April 30, 2026.

### Parent trackers

- `#23` parent issue for `0.1` alpha readiness
- `#24` parent issue for federation foundations and replication backlog
- `#25` parent issue for post-alpha product depth and scalability backlog

### Priority now: `0.1` alpha

- `#23` parent tracker for the full alpha slice
- `#1` production observability and readiness baseline
- `#2` baseline performance checks for CRUD, ingest, MQTT publication, and RPC
- `#3` boundary-focused runtime coverage and auth-enabled WebSocket flows
- `#4` MQTT failure/reconnect/overload behavior and documentation
- `#5` runtime and operations guide
- `#6` ingest and CRS guide
- `#7` MQTT and RPC documentation
- `#8` data and behavior model guide
- `#9` decision on alpha fence timeout and tolerance scope
- `#10` decision on alpha collision-event scope

### Priority next: post-alpha backlog

- `#24` parent tracker for federation design and implementation
- `#17` define stable hub identity and peer configuration for federation
- `#18` design federation auth and trust for hub-to-hub service identities
- `#21` federated event replication with provenance, replay handling, deduplication, and loop suppression
- `#22` federation operational visibility for peer health, lag, retries, and dead-letter conditions
- `#25` parent tracker for non-federation product depth
- `#12` real `com.omlox.core.xcmd` adapters
- `#13` richer REST query and filter behavior
- `#14` higher-cardinality WebSocket fan-out efficiency
- `#15` deeper proximity resolution and shared locating tolerances
- `#19` cloud-authoritative metadata sync and slave-mode behavior
- `#20` resource snapshot sync and change feeds for federated CRUD resources
- `#11` contributor-quality guide
- `#16` evaluate advanced georeferencing calibration beyond the current 2D similarity fit
- `#26` downstream consumer delivery and reconnect semantics for WebSocket and MQTT event streams

### Needs input before implementation can move cleanly

- `#9` decide the exact alpha target for fence timeout and tolerance semantics
- `#10` decide whether alpha collision behavior stays intentionally narrow or expands
- `#12` choose the first real `com.omlox.core.xcmd` adapter target and command scope
- `#17` decide the first federation topology slice and peer bootstrap model
- `#18` decide the federation trust model, scopes, and ownership propagation rules
- `#19` decide which metadata classes become cloud-authoritative first and what slave-mode write restrictions apply

### Documentation follow-up now tracked explicitly

- `#5` runtime and operations guide
- `#6` ingest and CRS guide
- `#7` MQTT and RPC guide
- `#8` data and behavior model guide
- `#11` contributor-quality guide
- Add the federation and trust guide after implementation starts, using `engineering/federation-plan.md` as the design baseline for software-facing documentation.

## Adjacent Project Note

Asset-history, movement, fence-event, and collision analytics for Grafana should be built as a separate downstream project on top of the hub's WebSocket or MQTT event APIs rather than as high-cardinality operational metrics inside the hub itself. A first draft plan for that adjacent project now lives in `engineering/analytics-project-plan.md`.
