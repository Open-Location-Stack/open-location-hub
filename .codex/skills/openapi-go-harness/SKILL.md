---
name: openapi-go-harness
description: Use this skill when implementing or updating the Open RTLS Hub Go scaffolding, including OpenAPI-first generation, just tasks, docker compose setup, and test harness wiring.
---

# OpenAPI Go Harness

## Trigger
Use for requests to scaffold or evolve this Go service harness.

## Workflow
1. Update `specifications/openapi/omlox-hub.v0.yaml` first when API behavior changes.
2. If the request is about OMLOX contract alignment, read `docs/openapi-governance.md` and use the sibling `omlox-spec-governance` skill.
3. Run `just bootstrap` before generation if dependencies or tools may be missing.
4. Regenerate API code with `just generate`.
5. Keep generated outputs under `internal/httpapi/gen`.
6. If generation changes interface types or operations, update handler signatures in `internal/httpapi/handlers/handlers.go`.
7. Update implementation-facing docs in the same change when behavior, config, or workflows changed:
   - `docs/implementation-plan.md` for current status, remaining work, and follow-ups
   - `docs/configuration.md` for new or changed env vars
   - `docs/architecture.md` when runtime flow or component boundaries changed
   - `README.md` when native build/runtime prerequisites, local setup steps, or platform-specific package dependencies changed
8. Finish with `just test` and `just check`.

## Guardrails
- Do not hand-edit generated files unless explicitly bootstrapping placeholders.
- Prefer environment variables over hardcoded config.
- Keep docs and scaffolding aligned with actual `just` workflows.
- When native dependencies change, keep the README's build dependency section accurate for both macOS/Homebrew and Debian/Ubuntu-style Linux.
- When adding endpoints from a spec expansion, prefer temporary scaffold stubs over partial implementations that break the generated interface.
- Do not treat `docs/implementation-plan.md` as optional maintenance; revise it after each substantial implementation change.
- If the implementation still falls short of the spec or the intended behavior, document the gap and the next follow-up explicitly.

## Project Status
- Until further notice, this project is in `alpha` status.
- During alpha iterations, do not add backward-compatibility shims or migration layers unless explicitly requested.
