# Architecture

## Layers
- `cmd/hub`: process bootstrap and wiring
- `internal/config`: environment-driven configuration
- `internal/httpapi`: API surface and handlers
- `internal/storage/postgres`: durable store
- `internal/state/valkey`: transient state
- `internal/mqtt`: MQTT topic mapping and broker integration
- `internal/auth`: token verification middleware

## Proximity Resolution Path
1. A REST, WebSocket, or MQTT `Proximity` update enters the shared hub service.
2. The hub resolves the referenced zone by `zone.id` or `zone.foreign_id`.
3. Only proximity-oriented zones are accepted for this path (`rfid` and `ibeacon`).
4. The hub loads transient per-provider proximity state from Valkey.
5. The resolver applies hub defaults plus any `Zone.properties.proximity_resolution` overrides.
6. Hysteresis rules decide whether to stay in the current zone or switch to the new candidate zone.
7. The hub emits a derived local `Location` using the resolved zone position and then continues through the normal location pipeline.

Resolver notes:
- durable configuration lives in Postgres as part of the zone resource
- transient proximity membership state lives in Valkey
- derived location metadata includes hub extension fields such as `resolution_method`, `resolved_zone_id`, and `sticky`

Current limits and likely next steps:
- the resolver currently treats zone position as the emitted point; it does not estimate a better coordinate inside the zone
- only static proximity zones are supported today
- a practical next extension is a mobile-zone mode where a proximity zone follows a referenced provider or trackable
- a second practical extension is to reuse future confidence/tolerance concepts across proximity resolution, locating rules, and fence handling

## Contract-first flow
1. Update OpenAPI spec.
2. Regenerate generated server/types.
3. Implement handler behavior.
4. Validate with tests and check pipeline.
