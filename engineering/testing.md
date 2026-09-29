# Testing

Follow Red/Green TDD practices

## Required Validation Sequence

For contract, scaffolding, or integration changes, run the repo workflow in this order:

```bash
just bootstrap
just generate
just test-race
just check
```

For documentation-only changes:

```bash
# no test gate required
```

Use the exception only when the change does not modify code, generated files,
OpenAPI, SQL, runtime configuration, or any behavior that needs executable
verification.

## Unit tests
Run the standard Go test suite through the full repository gate:

```bash
just check
```

Covers config parsing/defaults, auth verification behavior, and MQTT topic mapping.
Covers CRS/georeferencing round trips, randomized globe-wide coordinate conversion cases, service-level publication behavior, and RPC bridge validation such as local built-in methods, aggregation behavior, and per-method authorization.
The unit-test packages now avoid shared process-global test state so `t.Parallel()` can be used broadly, including the runtime-entry and config suites.

## Race detector
Run the standard race-detector suite:

```bash
just test-race
```

This reuses the repository's `tools/bin/testable-packages` selection so it
tracks the same PROJ-dependent package rules as the standard test run inside `just check`.

## Lint and cleanliness
Run the contributor lint gate:

```bash
just lint
```

The lint stack now verifies:
- `go vet`
- `staticcheck`
- `govulncheck`
- `go mod tidy` leaves [go.mod](../go.mod) and [go.sum](../go.sum) unchanged
- `just generate` leaves [internal/httpapi/gen/api.gen.go](../internal/httpapi/gen/api.gen.go) and [internal/storage/postgres/sqlcgen](../internal/storage/postgres/sqlcgen) unchanged

## Dependency validation

CI reads Go 1.27.1 from `go.mod`. Its tool cache includes the runner architecture,
`go.mod`, and the lowercase `justfile`, so changes to the toolchain or pinned
bootstrap tools invalidate cached binaries. Both Go jobs use the same pins.
The verification job also runs `just build` explicitly. SigNoz is a local demo
service and is not part of the integration suite or its CI caches.

Run Python dependency checks with:

```bash
just check-python
```

This checks all five `uv.lock` files against their manifests, installs the locked
dependencies, compiles connector scripts, checks imports and GTFS protobuf
serialization, and runs the UWB simulator, GTFS, and replay tests. Python 3.14 is
the only supported minor version. CI and `.python-version` select its current
stable patch, 3.14.7; manifests require `>=3.14.7,<3.15`. Update these pins together
when adopting a newer Python release.
After editing Python requirements, run `uv lock --project <directory>` before
validation. Use `uv lock --upgrade --project <directory>` for a dependency refresh.

Container builds use Go 1.27.1 and Alpine 3.24 for both build and runtime stages.
Integration fixtures use PostgreSQL 17.11, Valkey 9.1.2, Mosquitto 2.1.2, Dex
2.45.1, and Python 3.14.7. PostgreSQL stays on major 17 to preserve compatibility
with existing local and deployed data directories; a major upgrade needs a
separate data migration. The deployment collector is pinned to 0.161.0.
SigNoz stays at 0.117.1: its 0.143.0 source tree no longer includes the
`deploy/docker/docker-compose.yaml` consumed by the launcher. Updating it needs
a deployment-source migration and validation of the admin/dashboard bootstrap.

The September 2026 scan reports no reachable vulnerable symbols. It also reports
[GO-2026-6443](https://pkg.go.dev/vuln/GO-2026-6443) in the imported gRPC 1.84.0
package; the reported fix is a development version. Keep the latest stable gRPC
release until a stable fix is available, and rerun `govulncheck` when updating it.

## Integration tests
Run integration tests with Docker/Testcontainers:

```bash
just test-int
```

The suite boots Postgres, Valkey, and Mosquitto containers and performs migration smoke checks.
If Docker is unavailable, tests should skip.
The integration harness now retries migration startup briefly so hosted CI runners tolerate transient Postgres readiness and connection-reset races during container boot.
The Docker-backed integration tests now use unique per-test image references for Dockerfile builds so `t.Parallel()` can run without sharing `latest`-tag image state across tests.
The shared hub application image is now built once per integration test process and reused across scenarios so full-suite parallel runs do not overwhelm local Docker VMs with redundant identical builds.

The auth end-to-end suite also boots Dex, fetches a bearer token over the token endpoint, and proves that the hub accepts or rejects requests with `401` and `403` as expected.
The CRS end-to-end suite uses Mosquitto-backed publication checks to verify that local and WGS84 topics carry true derived variants and that unavailable derived topics are suppressed.
The integration suite now also includes shared-hub scenario coverage for high-traffic paths: concurrent REST location publishers with simultaneous MQTT and WebSocket subscribers, mixed REST and MQTT ingest against one running hub instance, and a ten-object movement scenario that traverses multiple arranged geofences while asserting both trackable-motion updates and fence entry/exit accuracy.

## Notes

- `just generate` must run after OpenAPI changes so generated handler interfaces stay aligned.
- `just check` is the standard final validation gate and now owns the normal Go test run directly.
- Documentation-only changes do not require `just check` or `just test-race` unless they also touch executable or generated surfaces.
- CI now splits the regular verification path and the race-detector path into separate GitHub Actions jobs so the slower standard test and `just test-race` stages run concurrently; the regular verification job still runs generation checks, lint, unit/integration tests, and build validation.
- CRS builds require PROJ headers/libs plus a `pkg-config`-compatible binary.
- On macOS, PROJ installation currently relies on the repo-local `tools/bin/pkg-config` shim, so CRS behavior is not treated as a verified host-native path there.
- Linux and Docker builds install native PROJ packages and are the expected path for CRS behavior and its test coverage.
- GitHub Actions Ubuntu runners also need native PROJ packages before `just lint`, `just check`, or `just build`; the CI workflow installs `pkg-config`, `libproj-dev`, and `proj-data` explicitly.
- direct `go test` or `go build` runs should export `PKG_CONFIG="$PWD/tools/bin/pkg-config"` if `pkg-config` is not already available globally.
- Auth setup, Dex fixtures, and permission examples are documented in [docs/auth.md](../docs/auth.md).

## OMLOX 0.2 alignment checks

`just test-unit` runs Go package tests without starting integration containers. The
locating-rule tests cover the published UWB/GPS priority and age example, typed
expression validation, out-of-order timestamps, independent provider publication,
and motion reads with collisions disabled. The complete release gate remains
`just bootstrap`, `just generate`, and `just check`; a unit-only pass is insufficient.

Keep the sibling CLI contract synchronized and test its actual HTTP methods,
query parameters, and 204 handling. Run the plugfest adapter's Node tests after
changing normalization or CLI forwarding. Record live DeepHub/ZIGPOS outcomes
separately from local mocks/replays in the integration compatibility note.

## Fence and collision behavior

Unit coverage exercises all nine table-13/table-14 transitions, infinite and zero
timeouts, timer replacement/cancellation, and autonomous fence exits. Geometry
checks cover meter-based geographic radii, polygon holes, boundary tangency, and
trackable radius expansion of spatial candidates. Collision checks cover first-mover
ordering, large jumps ending active pairs, every-update continuation events, provider
overrides, timeout maxima, return-to-intersection cancellation, floor/height separation,
and retaining active membership independently of observation cache TTL.

Live DeepHub and ZIGPOS hubs are unavailable. Their live interoperability tests are
explicitly skipped for the 0.2 validation; use adapter tests and available recordings, and
report those results as local evidence only.


The 0.2 regression suite also covers the JSON/OpenAPI contract changes, GCP arrays,
source-zone mapping, field defaults, requested projection, GeoJSON unions, and
cross-connection WebSocket subscription IDs. A local MQTT 5 client reads a retained
RPC announcement from Mosquitto and asserts its remaining expiry is at most 120
seconds. CLI HTTP tests and the seven plugfest Node tests provide offline adapter
coverage; they do not connect to vendor hubs.

## Queue and lookup regression coverage

`just test-unit` includes deterministic `testing/synctest` checks for idle event-bus
flush recovery, MQTT worker and queue bounds, handler cancellation, and copied
message payloads. Concurrent subscribe/emit/unsubscribe and metadata snapshot
writes are exercised under `just test-unit -race`. Lookup tests cover multiple
provider sources, stale observations, expiry, deletion, and changed trackable
assignments. WebSocket tests assert that unrelated events are filtered before
projection and matching subscriptions share projection work within a batch.
