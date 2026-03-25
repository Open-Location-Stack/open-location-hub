# Testing

Follow Red/Green TDD practices

## Required Validation Sequence

For contract, scaffolding, or integration changes, run the repo workflow in this order:

```bash
just bootstrap
just generate
just test
just check
```

## Unit tests
Run all unit tests:

```bash
just test
```

Covers config parsing/defaults, auth verification behavior, and MQTT topic mapping.
Covers CRS/georeferencing round trips, randomized globe-wide coordinate conversion cases, service-level publication behavior, and RPC bridge validation such as local built-in methods, aggregation behavior, and per-method authorization.

## Integration tests
Run integration tests with Docker/Testcontainers:

```bash
just test-int
```

The suite boots Postgres, Valkey, and Mosquitto containers and performs migration smoke checks.
If Docker is unavailable, tests should skip.

The auth end-to-end suite also boots Dex, fetches a bearer token over the token endpoint, and proves that the hub accepts or rejects requests with `401` and `403` as expected.
The CRS end-to-end suite uses Mosquitto-backed publication checks to verify that local and WGS84 topics carry true derived variants and that unavailable derived topics are suppressed.

## Notes

- `just generate` must run after OpenAPI changes so generated handler interfaces stay aligned.
- `just check` reruns tests and build validation, so use it as the final gate before commit.
- CRS builds require PROJ headers/libs plus a `pkg-config`-compatible binary; the repo includes a `tools/bin/pkg-config` shim for Homebrew-style installs, and the Docker build installs PROJ directly.
- Auth setup, Dex fixtures, and permission examples are documented in [docs/auth.md](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/auth.md).
