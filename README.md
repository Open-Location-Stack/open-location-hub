# Open Location Hub

Open RTLS Hub is an OpenAPI-first Go implementation of an RTLS hub that targets the omlox hub specification and related standards. It is part of [Open Location Stack](https://openlocationstack.com), which is an initiative that we at [FORMATION](https://tryformation.com) are pushing to allow us to more easily integrate with partners supplying RTLS technology and, hopefully, bootstrapping an ecosystem of value add - somethingh that is currently missing and holding back the industry.

## Tracking development progress

Currently this implementation is undergoing heavy development. We are welcoming and solliciting feedback from interested users, integrators, and anyone else that would be interested in having or using this solution.

When this implementation is complete enough, the project may seek to work with omlox, PI, and PNO toward certification of the hub implementation.

This project is being pulled together with AI coding tools. However, that does not mean it is AI slop. I'm building this with decades of backend experience. I'm just doing it at a bit faster pace than was common until recently.

While not yet feature complete, the implementation has a rapidly growing test suite, integration tests, and usable feature set. The next steps will be hardening the implemnentation against any kind of foreseesable issues related to security, networking, throughput/latency, and adding robust integration points for our partners to integrate against.

- Normative REST contract: `specifications/openapi/omlox-hub.v0.yaml`
- Go server scaffold with strict handler interface shape
- Dockerized local runtime for Postgres, Mosquitto, and app
- `just` orchestration for generation, checks, tests, and compose
- JWT auth modes: `oidc`, `static`, and `hybrid`
- RBAC and ownership-aware authorization
- Unit tests and Testcontainers integration test harness

This project is alpha quality currently. But we hope to get lots of feedback via the issue tracker, pull requests. And of course we can also discuss via email or in person. Contact us via [email](info@tryformation.com), or directly.

## Omlox

This project is an independent implementation and is currently not affiliated with, endorsed by, or certified by PROFIBUS Nutzerorganisation e.V. or PROFIBUS & PROFINET International unless explicitly stated otherwise. `omlox` is a trademark of PROFIBUS Nutzerorganisation e.V. The official omlox specifications remain subject to PI/PNO license terms.

Additionally, the scope of this project is broader than just omlox. We love omlox as a standard and the hub concept is exactly what is needed. An OSS implementation such as this will hopefully accelerate adoption of this standard. However, our intention is to be vendor neutral. Developing connectors that feed location data to the hub is easy and we hope that interested partners will start publishing their own connectors. The hub speaks MQTT, Websocket, REST. Additionally, we are also including an experimental omlox RPC implementation to enable applications to call into device specific functions. 

## Quickstart
1. `cp .env.example .env`
2. `just bootstrap`
3. `just generate`
4. `just compose-up`
5. `just run` (or run app in compose)

## Build dependencies

The hub now includes native CRS transformation support via PROJ, so local builds need both the Go toolchain and the PROJ development libraries.

macOS with Homebrew:
- `brew install just pkgconf proj`
- if `pkg-config` is still not on your shell path, use the repo-local shim via the provided `just` commands

Debian/Ubuntu:
- `sudo apt-get update`
- `sudo apt-get install -y golang-go just build-essential pkg-config libproj-dev proj-data`

Notes:
- `just bootstrap` installs the pinned Go code generators used by this repo
- On macOS, PROJ installation currently relies on the repo-local `tools/bin/pkg-config` shim, so CRS behavior is not treated as a verified host-native path there
- Docker builds install the required PROJ packages inside the image, so containerized workflows do not depend on host-installed PROJ headers
- Linux and Docker builds are the expected path for CRS behavior and its verification
- direct `go test`/`go build` invocations should also set `PKG_CONFIG="$PWD/tools/bin/pkg-config"` if `pkg-config` is not globally available on your shell path
- the repo-local `tools/bin/pkg-config` shim emits a one-time warning on macOS when it is used so the fallback path is visible

## Key commands
- `just lint` runs `go vet`, `staticcheck`, `govulncheck`, `go mod tidy`, and generated-file cleanliness checks
- `just test-race` runs the Go race detector across the repo's testable packages
- `just check` runs formatting, lint, tests, and build
- `just test-int` runs integration tests (Docker required)
- `just compose-logs` tails compose services

## Software docs
- `docs/index.md`
- `docs/architecture.md`
- `docs/configuration.md`
- `docs/auth.md`
- `docs/rpc.md`

## Engineering docs
- `engineering/index.md`
- `engineering/testing.md`
- `engineering/openapi-governance.md`
