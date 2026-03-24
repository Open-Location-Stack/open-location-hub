# Configuration

All runtime configuration is environment-driven.

## Core
- `HTTP_LISTEN_ADDR` (default `:8080`)
- `LOG_LEVEL` (default `info`)
- `POSTGRES_URL` (default `postgres://postgres:postgres@localhost:5432/openrtls?sslmode=disable`)
- `VALKEY_URL` (default `redis://localhost:6379/0`)
- `MQTT_BROKER_URL` (default `tcp://localhost:1883`)

## Stateful Processing
- `STATE_LOCATION_TTL` (duration, default `10m`)
- `STATE_PROXIMITY_TTL` (duration, default `5m`)
- `STATE_DEDUP_TTL` (duration, default `2m`)
- `RPC_TIMEOUT` (duration, default `5s`)

Stateful ingest behavior:
- duplicate location/proximity payloads inside `STATE_DEDUP_TTL` are suppressed before latest-state and publish fan-out work
- latest provider-source location state, trackable latest-location state, and fence membership state use the configured location/proximity TTLs for expiry semantics

## Proximity Resolution
- `PROXIMITY_RESOLUTION_ENTRY_CONFIDENCE_MIN` (number, default `0`)
- `PROXIMITY_RESOLUTION_EXIT_GRACE_DURATION` (duration, default `15s`)
- `PROXIMITY_RESOLUTION_BOUNDARY_GRACE_DISTANCE` (number, default `2`)
- `PROXIMITY_RESOLUTION_MIN_DWELL_DURATION` (duration, default `5s`)
- `PROXIMITY_RESOLUTION_POSITION_MODE` (default `zone_position`; the only supported value today)
- `PROXIMITY_RESOLUTION_FALLBACK_RADIUS` (number, default `0`)
- `PROXIMITY_RESOLUTION_STALE_STATE_TTL` (duration, default `10m`)

Proximity resolution behavior:
- proximity updates are resolved to a zone before the hub emits a derived `Location`
- the first valid proximity observation enters immediately
- the hub keeps the current zone for a short grace period to reduce flapping between nearby zones
- zone-specific overrides may be supplied through `Zone.properties.proximity_resolution`
- `Proximity.properties` is preserved into derived location metadata but does not override configured policy

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
