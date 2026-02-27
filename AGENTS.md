# AGENTS.md

## Purpose
This repository implements an OpenAPI-first OMLOX-compatible RTLS hub in Go.

## Engineering Rules
- OpenAPI contract in `specifications/openapi/omlox-hub.v0.yaml` is normative for REST.
- API shape changes must start in OpenAPI; code follows generated interfaces.
- Use `just` for all common workflows.
- Keep config environment-driven and Docker-friendly.

## Required Workflow
1. `just bootstrap`
2. `just generate`
3. `just test`
4. `just check`

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
