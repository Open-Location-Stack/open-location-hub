# Open RTLS Hub

Open RTLS Hub is an OpenAPI-first Go implementation scaffold for an RTLS hub that targets the omlox hub specification.

This project is an independent implementation and is not affiliated with, endorsed by, or certified by PROFIBUS Nutzerorganisation e.V. or PROFIBUS & PROFINET International unless explicitly stated otherwise. `omlox` is a trademark of PROFIBUS Nutzerorganisation e.V. The official omlox specifications remain subject to PI/PNO license terms and are not reproduced in this repository.

When this implementation is complete enough, the project may seek to work with omlox, PI, and PNO toward certification of the hub implementation.

## What is included
- Normative REST contract: `specifications/openapi/omlox-hub.v0.yaml`
- Go server scaffold with strict handler interface shape
- Dockerized local runtime for Postgres, Valkey, Mosquitto, and app
- `just` orchestration for generation, checks, tests, and compose
- JWT auth modes: `oidc`, `static`, and `hybrid`
- RBAC and ownership-aware authorization
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
- `docs/auth.md`
- `docs/openapi-governance.md`
- `docs/implementation-plan.md`
