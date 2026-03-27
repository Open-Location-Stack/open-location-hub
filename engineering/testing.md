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
- `go mod tidy` leaves [go.mod](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/go.mod) and [go.sum](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/go.sum) unchanged
- `just generate` leaves [internal/httpapi/gen/api.gen.go](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/internal/httpapi/gen/api.gen.go) and [internal/storage/postgres/sqlcgen](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/internal/storage/postgres/sqlcgen) unchanged

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
- GitHub Actions Ubuntu runners also need native PROJ packages before `just lint`, `just check`, or `just build`; the CI workflow installs `pkg-config`, `libproj-dev`, and `proj-data` explicitly and caches apt archives to reduce repeated package download cost.
- direct `go test` or `go build` runs should export `PKG_CONFIG="$PWD/tools/bin/pkg-config"` if `pkg-config` is not already available globally.
- Auth setup, Dex fixtures, and permission examples are documented in [docs/auth.md](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/auth.md).
