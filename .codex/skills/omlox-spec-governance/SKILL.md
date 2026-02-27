---
name: omlox-spec-governance
description: Use this skill when mapping OMLOX specification notes into normative OpenAPI contracts and companion protocol docs.
---

# OMLOX Spec Governance

## Trigger
Use for API contract work and normative alignment decisions.

## Workflow
1. Treat `specifications/openapi/omlox-hub.v0.yaml` as normative for REST.
2. Keep WebSocket and MQTT details in companion docs.
3. Record inferred endpoints explicitly as inferred.
4. Preserve OMLOX `/v2` base path compatibility.

## Validation
- Check that operation IDs and schemas align with handler interface names.
- Ensure required fields match OMLOX notes in `specifications/omlox/`.
