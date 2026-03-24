---
name: pragmatic-openapi-31
description: Use when updating this repository's OpenAPI contract or reconciling OMLOX PDF requirements with generator and tooling limits. Prefer OpenAPI 3.1.0 features where they help, but stay pragmatic and avoid fragile constructs that break the Go toolchain.
---

# Pragmatic OpenAPI 3.1

Use this skill for spec-first API work in this repository when the task involves:
- changing `specifications/openapi/omlox-hub.v0.yaml`
- mapping OMLOX PDF rules into OpenAPI
- deciding whether a rule belongs in the schema, companion docs, or runtime validation

## Workflow
1. Start from the OMLOX PDFs in the parent directory and treat them as source of truth.
2. Keep `specifications/openapi/omlox-hub.v0.yaml` normative for REST.
3. Prefer OpenAPI 3.1.0 features when they clearly improve precision.
4. Do not force 3.1-only constructs if `oapi-codegen` or the Go harness degrades, generates opaque `interface{}` models, or breaks builds.
5. When a PDF rule cannot be expressed cleanly in the current toolchain, document it in `specifications/openapi/omlox-openapi-gap-handling.md` and enforce it in runtime validation.
6. Keep WebSocket and MQTT protocol details out of REST path modeling; document them in companion OMLOX markdown.
7. Mark inferred REST additions explicitly when the PDF implies lifecycle behavior without spelling out the exact endpoint set.
8. For MQTT or WebSocket changes, consult `specifications/omlox/websocket.md` and `specifications/omlox/mqtt.md` before editing the REST contract.
9. Vendor or third-party public docs may be used as reference implementation context, but OMLOX PDFs remain normative.

## Heuristics
- Prefer schema validation for stable field types, enums, defaults, and simple conditional requirements that codegen preserves well.
- Prefer runtime validation for cross-field, lifecycle, or stateful rules when schema composition would harm generated types.
- Prefer a working generated server over a theoretically perfect schema that the toolchain cannot use.
- Avoid spec churn caused only by generator quirks; capture the quirk once in the companion gap-handling note.

## Validation
After spec changes, run:
1. `just bootstrap`
2. `just generate`
3. `just test`
4. `just check`

If generation or build fails because of a schema refinement, simplify the schema to the strongest form the toolchain supports and document the remaining rule in the companion gap-handling note.
