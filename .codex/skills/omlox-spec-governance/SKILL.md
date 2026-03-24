---
name: omlox-spec-governance
description: Use this skill when mapping OMLOX specification notes into normative OpenAPI contracts and companion protocol docs.
---

# OMLOX Spec Governance

## Trigger
Use for API contract work and normative alignment decisions.

## Workflow
1. Treat `specifications/openapi/omlox-hub.v0.yaml` as normative for REST.
2. Start with the OMLOX PDFs in the parent directory; use `specifications/omlox/` notes as repo-local summaries, not as the final source of truth.
3. Keep WebSocket and MQTT details in companion docs.
4. Record inferred endpoints, nested objects, and schema details explicitly as inferred when the PDF implies behavior but does not fully spell out the REST contract.
5. Preserve OMLOX `/v2` base path compatibility.
6. When API surface or behavior changes, update the relevant companion docs under `specifications/omlox/` in the same change.
7. Keep `docs/implementation-plan.md` aligned with the current implementation status, especially when a spec gap is closed or a new limitation/follow-up is discovered.
8. When API surface changes, hand off to the Go harness flow: `just bootstrap`, `just generate`, `just test`, `just check`.

## Validation
- Check that operation IDs and schemas align with handler interface names.
- Ensure required fields match the OMLOX PDFs and stay consistent with `specifications/omlox/`.
- Prefer updating companion docs when a PDF clause belongs to MQTT, WebSocket, or behavior rather than REST shape.
- For MQTT or WebSocket work, read `specifications/omlox/websocket.md` and `specifications/omlox/mqtt.md` first.
- Treat third-party public docs as reference implementation material for clarification and interoperability ideas, but do not let them override the OMLOX PDFs unless the repository explicitly adopts an extension.
- If implementation intentionally deviates from or incompletely covers the spec, document that limitation explicitly instead of leaving silent drift between code and docs.

## Project Status
- Until further notice, this project is in `alpha` status.
- During alpha iterations, do not preserve backward compatibility by default; prioritize forward progress unless compatibility is explicitly requested.
