# Open Location Hub

Open RTLS Hub is an OpenAPI-first Go implementation of an OMLOX-ready location hub. It provides OMLOX `/v2` REST resources, OMLOX companion MQTT and WebSocket surfaces, and hub-mediated RPC control-plane support for location-driven integrations.

The hub is vendor-neutral and environment-driven. It runs with Postgres, MQTT, and JWT-based access control, and it follows a contract-first workflow with the normative REST contract in [specifications/openapi/omlox-hub.v0.yaml](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/specifications/openapi/omlox-hub.v0.yaml).

Key capabilities:
- OMLOX `/v2` REST resources and ingestion endpoints
- OMLOX WebSocket companion surface
- OMLOX MQTT companion surface
- OMLOX RPC control-plane support through the hub
- JWT auth modes: `oidc`, `static`, and `hybrid`
- RBAC and ownership-aware authorization
- Dockerized local runtime for Postgres, Mosquitto, Dex, and the hub
- `just` workflows for bootstrap, code generation, validation, and compose operations
- Unit tests and Testcontainers-based integration coverage
- Connector demonstrators under [`connectors/`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors)

## Omlox

This project is an independent implementation. It is not affiliated with or certified by PROFIBUS Nutzerorganisation e.V. or PROFIBUS & PROFINET International unless stated otherwise. `omlox` is a trademark of PROFIBUS Nutzerorganisation e.V. The official omlox specifications remain subject to PI/PNO license terms.

## Quickstart
1. `cp .env.example .env`
2. `just bootstrap`
3. `just generate`
4. `just compose-up`
5. `just run`

## Build Dependencies

The hub uses native CRS transformation support via PROJ, so local builds need the Go toolchain and PROJ development libraries.

macOS with Homebrew:
- `brew install just pkgconf proj`
- the repository `just` workflows use the repo-local `tools/bin/pkg-config` shim when `pkg-config` is not available on the shell path

Debian/Ubuntu:
- `sudo apt-get update`
- `sudo apt-get install -y golang-go just build-essential pkg-config libproj-dev proj-data`

Notes:
- `just bootstrap` installs the pinned Go code generators used by this repository
- Docker builds install the required PROJ packages inside the image
- Linux and Docker workflows are the validated CRS execution paths in this repository
- direct `go test` and `go build` invocations should set `PKG_CONFIG="$PWD/tools/bin/pkg-config"` when `pkg-config` is not globally available
- the repo-local `tools/bin/pkg-config` shim emits a one-time warning on macOS when it is used

## Key Commands
- `just lint` runs `go vet`, `staticcheck`, `govulncheck`, `go mod tidy`, and generated-file cleanliness checks
- `just test-race` runs the Go race detector across the repository's testable packages
- `just check` runs formatting, lint, tests, and build
- `just test-int` runs integration tests with Docker
- `just compose-logs` tails compose services

## Software Docs
- [docs/index.md](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/index.md)
- [docs/architecture.md](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/architecture.md)
- [docs/configuration.md](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/configuration.md)
- [docs/auth.md](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/auth.md)
- [docs/rpc.md](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/rpc.md)

## Connector Demonstrators
- [connectors/README.md](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/README.md)
- [connectors/local-hub/README.md](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/local-hub/README.md)
- [connectors/gtfs/README.md](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/gtfs/README.md)
- [connectors/opensky/README.md](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/opensky/README.md)

## Utility Scripts
- [scripts/log_locations.py](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/scripts/log_locations.py)
- [scripts/log_fence_events.py](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/scripts/log_fence_events.py)
- [scripts/log_collision_events.py](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/scripts/log_collision_events.py)
- [scripts/check_fence_alignment.py](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/scripts/check_fence_alignment.py)

## Engineering Docs
- [engineering/index.md](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/engineering/index.md)
- [engineering/testing.md](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/engineering/testing.md)
- [engineering/openapi-governance.md](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/engineering/openapi-governance.md)
