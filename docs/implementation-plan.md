# Implementation Plan

This plan reflects the current repository state as verified on 2026-03-25 with targeted Go package tests plus repository inspection. A full `just test` and `just check` pass still remains blocked on local PROJ headers/libs in the current macOS environment.

## Current Status

### Completed and verified
- Project harness is in place: `justfile`, `Dockerfile`, `docker-compose.yml`, and runtime bootstrap in `cmd/hub/main.go`.
- The normative OpenAPI contract exists in `specifications/openapi/omlox-hub.v0.yaml`, and generated server/types are present under `internal/httpapi/gen`.
- Public Go packages now include package-level and exported-symbol doc comments so `go doc` renders the operational API surface more usefully for maintainers.
- Repository-local Codex skills now explicitly require documentation updates during implementation work and provide a dedicated documentation skill for Go doc, OpenAPI, and implementation-facing docs.
- The HTTP server is wired with Chi routing, request logging, auth middleware, generated route registration, and a health endpoint.
- Auth foundations are implemented for `none`, `oidc`, `static`, and `hybrid` modes, including JWT validation, OIDC discovery/JWKS refresh, permissions loading, and ownership-aware authorization.
- Core REST CRUD is implemented for zones, providers, trackables, and fences through a shared service layer backed by Postgres and `sqlc`.
- Provider ingestion endpoints are implemented for locations and proximities, with Valkey-backed deduplication and transient latest-state storage.
- Ingest accepts omitted `crs`, `local`, and named EPSG codes, derives true local and WGS84 variants when transformation is possible, and suppresses only the unavailable output topic when it is not.
- MQTT is broker-backed and wired into startup, inbound ingest topics, and outbound location, fence-event, and trackable-motion publication.
- RPC now operates as a control-plane surface: `GET /v2/rpc/available` and `PUT /v2/rpc` support hub-owned methods, MQTT-bridged methods, retained method discovery, and the `_all_within_timeout`, `_return_first_success`, and `_return_first_error` aggregation modes.
- Unit and integration coverage exist for config validation, auth, CRUD behavior, transient ingest state, CRS transformation/georeferencing behavior, MQTT topic mapping/publication, RPC bridge behavior, and Dex-backed end-to-end authorization.
- The OpenAPI contract now includes clearer tag, operation, parameter, response, and schema descriptions for the current REST and RPC surface.

### Implemented but still incomplete
- The persistence model stores canonical API payloads as JSON and only indexes a minimal set of fields; there is not yet richer filtering, search, or migration support for query-heavy workloads.
- Proximity ingestion now uses a stateful resolver that maps proximity updates to proximity-capable zones, applies anti-flap stickiness, and emits derived locations from the resolved zone position.
- Proximity resolution is still intentionally simple:
  - it uses the resolved zone's declared position rather than triangulation or sensor fusion
  - it does not support moving zones tied to a provider or trackable
  - it does not combine multiple simultaneous proximity observations into a richer confidence model
  - it does not yet share logic with trackable locating rules or fence tolerance behavior
- CRS transformation now exists for WGS84, projected EPSG inputs, and OMLOX local coordinates backed by zone ground control points, but it currently relies on a fitted 2D similarity model and does not yet attempt richer benchmark/anchor calibration.
- macOS-specific validation is still incomplete in the current verified state because local PROJ headers/libs were not yet available to complete `just test` and `just check`; finish a full Homebrew-based validation pass and document any platform-specific fixes if needed.
- Fence processing is currently a simple in-process point-in-region check over latest locations; provider- and trackable-specific timeout semantics from the OMLOX text are not yet modeled in depth.
- MQTT publication and subscription use a QoS 1 baseline and reconnect behavior, but there is no explicit backpressure policy, retry accounting, or dead-letter handling.
- RPC now publishes retained announcements for hub-owned methods and hosts local implementations of `com.omlox.ping`, `com.omlox.identify`, and `com.omlox.core.xcmd`, but `com.omlox.core.xcmd` still depends on a deployment-specific adapter before it can execute real device commands.
- MQTT method announcement support currently relies on retained publication without MQTT v5 message-expiry enforcement because the current client layer does not yet expose that broker feature cleanly.
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
- Location ingest validation now accepts omitted `crs`, `local`, and named EPSG codes, with runtime transformation determining which derived outputs are publishable.
- CRS transformation uses PROJ-backed named CRS conversion plus zone `ground_control_points` fitting for local georeferencing.
- Proximity-derived locations still originate in local zone coordinates, but georeferenced zones now also emit derived WGS84 publication.
- Service-level and end-to-end tests cover round-trip transformation behavior, randomized and edge-case coordinate conversion, topic suppression when a derived variant is unavailable, and Mosquitto-backed live publish/subscribe assertions.

### Phase 3: MQTT bridge baseline
Delivered:
- The MQTT client now connects to a real broker, subscribes, publishes, reconnects, and shuts down cleanly.
- Inbound MQTT location and proximity topics feed the shared ingestion service.
- Outbound MQTT publication exists for processed locations, fence events, and trackable motions.

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
- Revisit the current 2D similarity-fit georeferencing model if OMLOX deployments require affine, anchor-assisted, or higher-order calibration.
- Revisit optional proximity depth such as mobile zones, richer confidence-based switching, and shared tolerance semantics with fences/trackable locating.
- Revisit the JSON-payload-first storage model if query volume or federation requirements demand more structured persistence.

Exit criteria:
- The implementation moves from an operational baseline to an OMLOX behavior model that is deeper, more scalable, and easier to evolve.

## Near-Term Priority

The immediate priority should be production hardening and behavior-depth follow-up rather than more scaffolding work. The repository now exposes a functioning CRUD, ingest, MQTT, and RPC baseline, so the biggest remaining gaps are correctness depth in OMLOX behavior and operational readiness under real runtime conditions.
