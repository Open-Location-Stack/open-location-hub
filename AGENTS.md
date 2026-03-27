# AGENTS.md

## Purpose
This repository implements an OpenAPI-first OMLOX-compatible RTLS hub in Go.

## Engineering Rules
- OpenAPI contract in `specifications/openapi/omlox-hub.v0.yaml` is normative for REST.
- API shape changes must start in OpenAPI; code follows generated interfaces.
- Use `just` for all common workflows.
- Keep config environment-driven and Docker-friendly.
- Treat OMLOX PDFs and the normative OpenAPI/companion spec docs as the source of truth before borrowing behavior from reference implementations.
- Keep implementation-facing docs aligned with the code in the same change when behavior, workflows, or runtime knobs change.
- Update `engineering/implementation-plan.md` after every substantive change so it reflects what is done, what remains, and any newly discovered follow-up work.

## Required Workflow
1. `just bootstrap`
2. `just generate`
3. `just check`

Documentation-only exception:
- If a change only updates documentation and does not change code, generated artifacts, schemas, or runtime configuration, do not run test or check commands just for that documentation update.

## Documentation Guardrails
- If runtime behavior changes, update the relevant software docs under `docs/`, the engineering docs under `engineering/`, and `specifications/omlox/` in the same change as needed.
- `engineering/implementation-plan.md` is not a roadmap wish list; it should describe the current verified state of the repository, residual gaps, and near-term follow-ups.
- If implementation diverges from existing docs, fix the docs before closing the task.
- If behavior is intentionally left partial, document the limitation and the likely next step.

## Git Workflow Guardrails
- Do not run state-changing Git commands in parallel. In particular, keep `git add`, `git commit`, `git merge`, `git rebase`, `git stash`, and `git push` serialized.
- Avoid running Git reads that touch the index, such as `git status` or `git diff`, in parallel with state-changing Git commands.
- If a Git command fails with an `index.lock` error, first check for an active Git process; only remove the lock file when no Git process is still running for this repository.
- Prefer one Git command per tool invocation whenever lock contention is possible.

## Auth Expectations
Support these modes:
- `oidc`: external token providers through OIDC discovery/JWKS
- `static`: static public keys (PEM/JWKS URL)
- `hybrid`: both verification strategies

## Testing Conventions
- Unit tests for pure package behavior.
- Integration tests use Testcontainers.
- Integration tests may skip automatically if Docker is unavailable.

## Structure
- Runtime entry: `cmd/hub/main.go`
- Internal packages: `internal/...`
- Normative API: `specifications/openapi/...`
- Migrations: `migrations/`
- Integration tests: `tests/integration/`
