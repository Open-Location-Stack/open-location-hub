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
- `AUTH_HTTP_TIMEOUT` (duration, default `5s`)
- `AUTH_PERMISSIONS_FILE` (YAML path, default `config/auth/permissions.yaml`)
- `AUTH_ROLES_CLAIM` (JWT claim used for role extraction, default `groups`)
- `AUTH_OWNED_RESOURCES_CLAIM` (JWT object claim for owned resource IDs; see `docs/auth.md` for format and usage)

See [docs/auth.md](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/auth.md) for the full auth model, Dex setup, and permission file format.
