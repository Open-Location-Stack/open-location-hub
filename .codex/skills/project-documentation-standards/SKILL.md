---
name: project-documentation-standards
description: Use when writing or updating repository documentation, Go doc comments, OpenAPI descriptions, README/setup notes, or implementation-facing docs in this repository.
---

# Project Documentation Standards

Use this skill when the task materially touches documentation, including:

- exported Go API surface in non-generated code
- `specifications/openapi/omlox-hub.v0.yaml`
- implementation-facing docs under `docs/`
- OMLOX companion notes under `specifications/omlox/`
- README or local setup and dependency guidance

## Workflow

1. Identify every documentation surface affected by the code or spec change.
2. Update docs in the same change. Do not defer documentation to a follow-up.
3. Keep documentation aligned with the verified current repository state, not aspirational behavior.
4. When behavior is partial, inferred, or intentionally incomplete, document that limitation explicitly.

## Go Documentation

- Add package comments for non-generated packages with exported surface area.
- Add doc comments for exported non-generated types, interfaces, functions, methods, constants, and variables that changed or are newly introduced.
- Write comments for `go doc`, starting with the symbol name and explaining the observable role of the symbol, not the implementation trivia.
- Prefer concise operational comments over restating the signature.
- Do not add noise comments to unexported helpers unless they are unusually complex and benefit from explanation.

## OpenAPI Documentation

- Treat `specifications/openapi/omlox-hub.v0.yaml` as normative for REST.
- Add or improve `summary` and `description` on operations when the endpoint intent is not already obvious.
- Document parameters, responses, and schemas that would otherwise be ambiguous to an implementer or integrator.
- Prefer practical descriptions: required context, defaults, inferred behavior, and important constraints.
- Keep descriptions generator-safe. Do not introduce fragile schema constructs solely to express documentation.
- When REST behavior is inferred from OMLOX text rather than explicitly enumerated, mark that clearly in the contract or companion docs.

## Repository Docs

- Update `docs/implementation-plan.md` after substantive changes so it reflects the verified current state, remaining gaps, and near-term follow-up.
- Update `docs/configuration.md` when config knobs or runtime assumptions change.
- Update `docs/architecture.md` when component boundaries, data flow, or operational behavior change.
- Update `README.md` when local setup, platform prerequisites, or common workflows change.
- Update the relevant `specifications/omlox/*.md` notes when MQTT, WebSocket, RPC, or OMLOX behavior guidance changes outside the REST contract.

## Style

- Be concrete and repository-specific.
- Prefer short paragraphs and bullet lists over long prose blocks.
- Separate normative behavior from current implementation limitations.
- Avoid marketing language, certification claims, and vague promises.
- Do not describe work as done unless it is implemented and verified in this repository.

## Validation

- Run the repository workflow expected for the touched surfaces:
  1. `just bootstrap`
  2. `just generate`
  3. `just test`
  4. `just check`
- If local environment issues block validation, document the blocker precisely and keep the docs honest about the verified state.
