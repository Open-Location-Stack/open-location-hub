# OMLOX Hub V2 API Specification Notes

This folder summarizes the official OMLOX Hub API and behavior specification version **2.0.0 (August 2023)** from:
- `omlox-hub-spec_20112_V200_Aug23.pdf`

## Scope

The official specification defines these API groups:
- Zone API
- Trackable API
- Location Provider API
- Fence API
- WebSocket publish/subscribe API
- MQTT extension (optional)
- RPC extension (optional)

Base path examples in the specification use `/v2/...`.

## Important note on completeness

The PDF explicitly states that it is accompanied by an **OpenAPI specification file** and refers to that file for the full endpoint/schema list.
This markdown captures the normative API behavior and endpoint shapes directly visible in the official PDF text and marks inferred CRUD patterns where the PDF references resource lifecycle but does not print every method/path variant inline.

## Files

- `zones.md`
- `trackables.md`
- `location-providers.md`
- `fences.md`
- `locations.md`
- `proximities.md`
- `websocket.md`
- `mqtt.md`
- `mqtt-extension.md`
- `rpc-extension.md`
- `metadata-sync-extension.md`
- `flowcate-reference-extensions.md`
