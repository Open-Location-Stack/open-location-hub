set shell := ["bash", "-euo", "pipefail", "-c"]

proj-env := 'PATH="$PWD/tools/bin:$PATH" PKG_CONFIG="$PWD/tools/bin/pkg-config"'

bootstrap:
	@if ! command -v oapi-codegen >/dev/null || ! oapi-codegen -version 2>/dev/null | grep -q "v2.6.0"; then \
		go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.6.0; \
	fi
	@if ! command -v sqlc >/dev/null || ! sqlc version 2>/dev/null | grep -q "v1.30.0"; then \
		go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0; \
	fi
	@if ! command -v goose >/dev/null || ! goose -version 2>/dev/null | grep -q "v3.27.0"; then \
		go install github.com/pressly/goose/v3/cmd/goose@v3.27.0; \
	fi

proj-check:
	@{{proj-env}} pkg-config --exists proj || { \
		echo "missing PROJ development libraries" >&2; \
		echo "macOS: brew install pkgconf proj" >&2; \
		echo "Debian/Ubuntu: sudo apt-get install -y pkg-config libproj-dev proj-data" >&2; \
		exit 1; \
	}

generate:
	oapi-codegen -config tools/openapi/oapi-codegen.server.yaml specifications/openapi/omlox-hub.v0.yaml
	sqlc generate

migrate-up:
	goose -dir migrations postgres "$POSTGRES_URL" up

migrate-down:
	goose -dir migrations postgres "$POSTGRES_URL" down

run: proj-check
	{{proj-env}} go run ./cmd/hub

compose-up:
	docker compose --env-file .env.example up --build -d

compose-down:
	docker compose down -v

compose-logs:
	docker compose logs -f --tail=200

fmt:
	gofmt -w cmd internal tests

lint:
	@packages="$(bash tools/bin/testable-packages lint)"; \
	{{proj-env}} go vet $packages

test:
	@packages="$(bash tools/bin/testable-packages)"; \
	{{proj-env}} go test $packages

test-int: proj-check
	{{proj-env}} go test ./tests/integration -v

build:
	@packages="$(bash tools/bin/testable-packages build)"; \
	{{proj-env}} go build $packages

check: fmt lint test build
