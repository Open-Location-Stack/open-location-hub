# Implementation Plan

This plan reflects the current repository state as verified on 2026-03-24 with `just bootstrap`, `just generate`, `just test`, and `just check`.

## Current Status

### Completed and verified
- Project harness is in place: `justfile`, `Dockerfile`, `docker-compose.yml`, and runtime bootstrap in `cmd/hub/main.go`.
- The normative OpenAPI contract exists in `specifications/openapi/omlox-hub.v0.yaml`, and generated server/types are present under `internal/httpapi/gen`.
- The HTTP server is wired with Chi routing, request logging, auth middleware, generated route registration, and a health endpoint.
- Auth foundations are implemented for `none`, `oidc`, `static`, and `hybrid` modes, including JWT validation, OIDC discovery/JWKS refresh, permissions loading, and ownership-aware authorization.
- Core REST CRUD is implemented for zones, providers, trackables, and fences through a shared service layer backed by Postgres and `sqlc`.
- Provider ingestion endpoints are implemented for locations and proximities, with Valkey-backed deduplication and transient latest-state storage.
- MQTT is broker-backed and wired into startup, inbound ingest topics, and outbound location, fence-event, and trackable-motion publication.
- RPC is implemented as REST-to-MQTT bridging with retained method discovery tracking and support for `_all_within_timeout`, `_return_first_success`, and `_return_first_error`.
- Unit and integration coverage exist for config validation, auth, CRUD behavior, MQTT topic mapping, RPC bridge behavior, and Dex-backed end-to-end authorization.

### Implemented but still incomplete
- The persistence model stores canonical API payloads as JSON and only indexes a minimal set of fields; there is not yet richer filtering, search, or migration support for query-heavy workloads.
- Proximity ingestion currently resolves a synthetic location from the matching zone position; there is no richer proximity triangulation or provider-specific inference.
- Location ingest currently republishes the received coordinates as-is; there is no CRS transformation pipeline to produce a true derived WGS84/local dual output.
- Fence processing is currently a simple in-process point-in-region check over latest locations; provider- and trackable-specific timeout semantics from the OMLOX text are not yet modeled in depth.
- MQTT publication and subscription use a QoS 1 baseline and reconnect behavior, but there is no explicit backpressure policy, retry accounting, or dead-letter handling.
- RPC availability is discovered from MQTT retained announcements, but the hub does not yet publish its own built-in mandatory OMLOX methods or host local RPC handlers.
- Observability remains log-centric; dependency readiness, metrics, and deeper operational diagnostics are still limited.

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
- Valkey-backed TTL configuration exists for latest state, proximity-derived state, dedup windows, and RPC timeout.
- Ingestion uses a shared path so HTTP and MQTT inputs exercise the same core logic.

Residual work:
- Add explicit expiry-focused tests rather than only behavioral coverage through the current service layer.
- Replace the current proximity-to-zone-position fallback with richer OMLOX-aligned proximity resolution.
- Introduce explicit CRS transformation and validation behavior for local versus `EPSG:4326` flows.

### Phase 3: MQTT bridge baseline
Delivered:
- The MQTT client now connects to a real broker, subscribes, publishes, reconnects, and shuts down cleanly.
- Inbound MQTT location and proximity topics feed the shared ingestion service.
- Outbound MQTT publication exists for processed locations, fence events, and trackable motions.

Residual work:
- Add integration tests against the Mosquitto fixture that assert live publish/subscribe behavior, not just HTTP-side wiring.
- Document and enforce operational behavior for sustained publish failures, reconnect storms, and overloaded downstream consumers.
- Add retained method publication for hub-provided RPC methods if the hub is expected to advertise them directly.

### Phase 4: RPC baseline
Delivered:
- `GET /v2/rpc/available` exposes the retained MQTT-discovered method registry.
- `PUT /v2/rpc` bridges JSON-RPC requests to MQTT and collects responses according to the supported aggregation modes.
- Unit coverage exists for method discovery and first-success request handling.

Residual work:
- Add tests for `_all_within_timeout` and `_return_first_error` paths against multiple responses and timeout boundaries.
- Decide whether the hub should provide local handlers for `com.omlox.ping`, `com.omlox.identify`, and `com.omlox.core.xcmd`, and implement them if required.
- Harden malformed-response handling, response fan-in limits, and caller correlation cleanup under load.

## Remaining Work

### Phase 5: Production hardening
Scope:
- Expand observability with metrics, readiness checks, and richer failure diagnostics around auth, DB, Valkey, MQTT, and RPC.
- Tighten startup validation for dependency reachability and misconfiguration beyond the current env validation.
- Review auth hardening gaps such as key rotation telemetry, operator-facing guidance, and clearer runtime failure visibility.
- Establish baseline performance checks for CRUD, ingest, MQTT publication, and RPC fan-out/fan-in behavior.

Exit criteria:
- The hub can be operated with clear visibility into failures, dependency health, and expected throughput characteristics.

### Additional implementation depth
Scope:
- Add richer query/filter behavior for REST resources where OMLOX workflows benefit from more than list-by-created-time.
- Add stronger event modeling for fence timeouts, motion derivation, and future collision handling.
- Revisit the JSON-payload-first storage model if query volume or federation requirements demand more structured persistence.

Exit criteria:
- The implementation moves from an operational baseline to an OMLOX behavior model that is deeper, more scalable, and easier to evolve.

## Near-Term Priority

The immediate priority should be production hardening and behavior-depth follow-up rather than more scaffolding work. The repository now exposes a functioning CRUD, ingest, MQTT, and RPC baseline, so the biggest remaining gaps are correctness depth in OMLOX behavior and operational readiness under real runtime conditions.
