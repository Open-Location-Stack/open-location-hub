# Testing

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
