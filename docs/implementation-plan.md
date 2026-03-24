# Implementation Plan

This plan reflects the current repository state as verified on 2026-03-24 with `just bootstrap`, `just generate`, `just test`, and `just check`.

## Current Status

### Completed and verified
- Project harness is in place: `justfile`, `Dockerfile`, `docker-compose.yml`, `.env.example`, and runtime bootstrap in `cmd/hub/main.go`.
- The normative OpenAPI contract exists in `specifications/openapi/omlox-hub.v0.yaml`, and generated server/types are present under `internal/httpapi/gen`.
- The HTTP server is wired with Chi routing, request logging, auth middleware, generated route registration, and a health endpoint.
- Auth foundations are implemented for `none`, `oidc`, `static`, and `hybrid` modes, including JWT validation, OIDC discovery/JWKS refresh, permissions loading, and ownership-aware authorization.
- Persistence foundations exist: Postgres connection setup, initial Goose migration, `sqlc` configuration, and generated query code.
- Valkey and MQTT client wrappers exist and are wired into process startup.
- Unit and integration coverage exist, including a Dex-backed authorization test path using Testcontainers.

### Present but still scaffolded
- All generated REST handlers in `internal/httpapi/handlers` currently return `501 Not Implemented`.
- The SQL layer only exposes list queries for zones and providers; create/get/update/delete behavior is not implemented.
- The Valkey layer only supports basic set/get position helpers and is not yet connected to ingestion flows.
- The MQTT package currently provides topic helpers and a placeholder client rather than broker-backed ingest/output behavior.
- The RPC bridge is a stub.

## Implementation Sequence

### Phase 1
Deliver durable CRUD behavior for the core REST resources.

Scope:
- Extend migrations and `queries/core.sql` to cover create, get, update, delete, and filtering for zones, providers, trackables, and fences.
- Add a repository/service layer on top of `sqlc` outputs instead of placing database logic directly in handlers.
- Replace `501` handler responses with OpenAPI-shaped request parsing, validation, persistence, and response mapping.
- Add unit tests for mapping/validation and integration tests that prove end-to-end CRUD behavior.

Exit criteria:
- `/v2/zones`, `/v2/providers`, `/v2/trackables`, and `/v2/fences` support the basic REST lifecycle.
- Auth continues to protect those routes with the current permission model.

### Phase 2
Implement provider ingestion and transient state handling.

Scope:
- Implement `PostProviderLocations` and `PostProviderProximities`.
- Define the transient state model in Valkey for latest positions, proximity windows, deduplication, and TTL behavior.
- Normalize ingestion payloads against the OpenAPI contract and repository rules.
- Add tests for malformed payloads, repeated updates, and cache expiry semantics.

Exit criteria:
- Provider ingest endpoints accept valid events, reject invalid ones, and update transient state deterministically.

### Phase 3
Turn MQTT from topic helpers into a real bridge.

Scope:
- Add actual broker connectivity, subscription/publish lifecycle, and shutdown handling.
- Map omlox topic hierarchy to internal ingestion and outbound publication paths.
- Decide and document delivery guarantees, backpressure behavior, and reconnect strategy.
- Add integration coverage against the Mosquitto fixture already present in the repository.

Exit criteria:
- MQTT messages can drive the same ingestion path as HTTP where required, and hub output topics are published predictably.

### Phase 4
Implement RPC extension behavior.

Scope:
- Replace the placeholder bridge with request fan-out, response collection, timeout handling, and aggregation behavior.
- Align implementation with the RPC extension notes in `specifications/omlox/rpc-extension.md`.
- Add tests for `_all_within_timeout`, `_return_first_success`, and `_return_first_error`.

Exit criteria:
- RPC endpoints provide deterministic aggregation semantics and documented failure behavior.

### Phase 5
Harden operational behavior for production use.

Scope:
- Expand observability beyond request logging with metrics, structured event logs, and failure diagnostics around auth, DB, Valkey, MQTT, and RPC.
- Add readiness checks for dependent services, graceful degradation rules, and tighter startup validation.
- Review auth hardening gaps such as key rotation behavior, failure telemetry, and operator-facing configuration guidance.
- Establish baseline performance checks for CRUD, ingest, and bridge workloads.

Exit criteria:
- The hub can be operated with clear visibility into failures, dependency health, and expected throughput characteristics.

## Near-Term Priority

The immediate priority should be Phase 1. The repository already has a solid contract-first scaffold, generated code, auth enforcement, and a passing CI-style workflow. The largest gap is that the public API surface exists but does not yet perform any domain behavior, so CRUD persistence is the shortest path to turning the scaffold into a usable hub implementation.
