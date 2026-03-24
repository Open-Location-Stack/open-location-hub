# OpenAPI Normative Contract

`omlox-hub.v0.yaml` is the normative REST contract for this repository.

Scope for v0:
- OMLOX `/v2` REST resources and ingestion endpoints
- RPC extension REST endpoints

Out of OpenAPI scope in v0:
- WebSocket pub/sub protocol details
- MQTT topic protocol details

Those remain documented in `specifications/omlox/` and are represented in implementation docs.

Companion notes:
- `omlox-openapi-gap-handling.md` documents project-specific handling for gaps between the OMLOX PDFs, OpenAPI 3.1, and the current Go toolchain.
