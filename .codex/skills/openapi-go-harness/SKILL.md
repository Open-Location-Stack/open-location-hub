---
name: openapi-go-harness
description: Use this skill when implementing or updating the Open RTLS Hub Go scaffolding, including OpenAPI-first generation, just tasks, docker compose setup, and test harness wiring.
---

# OpenAPI Go Harness

## Trigger
Use for requests to scaffold or evolve this Go service harness.

## Workflow
1. Update `specifications/openapi/omlox-hub.v0.yaml` first when API behavior changes.
2. Regenerate API code with `just generate`.
3. Keep generated outputs under `internal/httpapi/gen`.
4. Ensure `just check` is green before finishing.

## Guardrails
- Do not hand-edit generated files unless explicitly bootstrapping placeholders.
- Prefer environment variables over hardcoded config.
- Keep Docker compose and README aligned.
