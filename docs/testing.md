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

## Integration tests
Run integration tests with Docker/Testcontainers:

```bash
just test-int
```

The suite boots Postgres, Valkey, and Mosquitto containers and performs migration smoke checks.
If Docker is unavailable, tests should skip.

The auth end-to-end suite also boots Dex, fetches a bearer token over the token endpoint, and proves that the hub accepts or rejects requests with `401` and `403` as expected.

## Notes

- `just generate` must run after OpenAPI changes so generated handler interfaces stay aligned.
- `just check` reruns tests and build validation, so use it as the final gate before commit.
- Auth setup, Dex fixtures, and permission examples are documented in [docs/auth.md](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/auth.md).
