# Architecture

## Layers
- `cmd/hub`: process bootstrap and wiring
- `internal/config`: environment-driven configuration
- `internal/httpapi`: API surface and handlers
- `internal/storage/postgres`: durable store
- `internal/state/valkey`: transient state
- `internal/mqtt`: MQTT topic mapping and broker integration
- `internal/auth`: token verification middleware

## Contract-first flow
1. Update OpenAPI spec.
2. Regenerate generated server/types.
3. Implement handler behavior.
4. Validate with tests and check pipeline.
