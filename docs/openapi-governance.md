# OpenAPI Governance

`specifications/openapi/omlox-hub.v0.yaml` is normative for REST behavior.

## Rules
1. API changes must be spec-first.
2. Generated code should be refreshed via `just generate`.
3. Handler signatures must stay aligned with generated interfaces.
4. MQTT and WebSocket protocol details are documented separately and not represented as REST endpoints.
