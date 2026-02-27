# Configuration

All runtime configuration is environment-driven.

## Core
- `HTTP_LISTEN_ADDR` (default `:8080`)
- `LOG_LEVEL` (default `info`)
- `POSTGRES_URL` (default `postgres://postgres:postgres@localhost:5432/openrtls?sslmode=disable`)
- `VALKEY_URL` (default `redis://localhost:6379/0`)
- `MQTT_BROKER_URL` (default `tcp://localhost:1883`)

## Auth
- `AUTH_ENABLED` (`true`/`false`, default `true`)
- `AUTH_MODE` (`none|oidc|static|hybrid`, default `none`)
- `AUTH_ISSUER` (OIDC issuer URL)
- `AUTH_AUDIENCE` (comma-separated)
- `AUTH_ALLOWED_ALGS` (comma-separated, default `RS256`)
- `AUTH_STATIC_PUBLIC_KEYS` (comma-separated PEM blocks or JWKS URLs)
- `AUTH_CLOCK_SKEW` (duration, default `30s`)
- `AUTH_OIDC_REFRESH_TTL` (duration, default `10m`)
