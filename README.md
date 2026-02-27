# Open RTLS Hub

Open RTLS Hub is an OpenAPI-first Go implementation scaffold for an OMLOX-compatible RTLS hub.

## What is included
- Normative REST contract: `specifications/openapi/omlox-hub.v0.yaml`
- Go server scaffold with strict handler interface shape
- Dockerized local runtime for Postgres, Valkey, Mosquitto, and app
- `just` orchestration for generation, checks, tests, and compose
- JWT auth modes: `oidc`, `static`, and `hybrid`
- Unit tests and Testcontainers integration test harness

## Quickstart
1. `cp .env.example .env`
2. `just bootstrap`
3. `just generate`
4. `just compose-up`
5. `just run` (or run app in compose)

## Key commands
- `just check` runs formatting, lint, tests, and build
- `just test-int` runs integration tests (Docker required)
- `just compose-logs` tails compose services

## Project docs
- `docs/architecture.md`
- `docs/configuration.md`
- `docs/testing.md`
- `docs/openapi-governance.md`
- `docs/implementation-plan.md`
