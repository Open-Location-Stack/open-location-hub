---
name: openapi-go-harness
description: Use this skill when implementing or updating the Open RTLS Hub Go scaffolding, including OpenAPI-first generation, just tasks, docker compose setup, and test harness wiring.
---

# OpenAPI Go Harness

## Trigger
Use for requests to scaffold or evolve this Go service harness.

## Workflow
1. Update `specifications/openapi/omlox-hub.v0.yaml` first when API behavior changes.
2. If the request is about OMLOX contract alignment, read `engineering/openapi-governance.md` and use the sibling `omlox-spec-governance` skill.
3. Run `just bootstrap` before generation if dependencies or tools may be missing.
4. Regenerate API code with `just generate`.
5. Keep generated outputs under `internal/httpapi/gen`.
6. If generation changes interface types or operations, update handler signatures in `internal/httpapi/handlers/handlers.go`.
7. Update documentation in the same change. Treat documentation as required implementation work, not polish:
   - add or refresh Go doc comments for exported non-generated packages, types, functions, methods, constants, and variables that changed
   - keep package-level `doc.go` files present when a package exposes non-trivial public surface area
   - improve OpenAPI summaries, descriptions, parameter docs, response docs, and schema/property descriptions when the REST surface changed or when existing spec docs are stale or too thin
8. Update implementation-facing docs in the same change when behavior, config, or contributor workflows changed:
   - `engineering/testing.md` when validation workflow or test harness expectations changed
   - `engineering/openapi-governance.md` when contract-first workflow guidance changed
   - `docs/configuration.md` for new or changed env vars
   - `docs/architecture.md` when runtime flow or component boundaries changed
   - `README.md` when native build/runtime prerequisites, local setup steps, or platform-specific package dependencies changed
9. If the change is documentation-heavy or spans multiple doc surfaces, use the sibling `project-documentation-standards` skill.
10. Before pushing, verify the touched paths with the repo workflow expected for the change. Do not push on assumption alone.
11. Finish with `just test` and `just check` only when implementation code, OpenAPI, generated artifacts, schemas, SQL, or runtime configuration changed. For contributor-maintenance-only changes, run only minimal targeted validation.

## Guardrails
- Do not hand-edit generated files unless explicitly bootstrapping placeholders.
- Prefer environment variables over hardcoded config.
- Keep docs and scaffolding aligned with actual `just` workflows.
- Treat validation as a release gate for pushes: if required checks cannot be run, stop and report the blocker instead of pushing an unverified change.
- Do not run `just bootstrap`, `just generate`, `just test`, or `just check` for non-functional contributor-maintenance changes unless those files are directly needed for the touched surfaces.
- Do not leave exported Go surface changes undocumented.
- Do not leave REST contract changes with bare or ambiguous descriptions when the intent can be documented clearly.
- When native dependencies change, keep the README's build dependency section accurate for both macOS/Homebrew and Debian/Ubuntu-style Linux.
- When adding endpoints from a spec expansion, prefer temporary scaffold stubs over partial implementations that break the generated interface.
- Treat GitHub issues, not `engineering/implementation-plan.md`, as the source of truth for open implementation follow-up work.
- If the implementation still falls short of the spec or the intended behavior, document the gap and the next follow-up explicitly.

## Project Status
- Until further notice, this project is in `alpha` status.
- During alpha iterations, do not add backward-compatibility shims or migration layers unless explicitly requested.
