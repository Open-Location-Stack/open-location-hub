set shell := ["bash", "-euo", "pipefail", "-c"]

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

generate:
	oapi-codegen -config tools/openapi/oapi-codegen.server.yaml specifications/openapi/omlox-hub.v0.yaml
	sqlc generate

migrate-up:
	goose -dir migrations postgres "$POSTGRES_URL" up

migrate-down:
	goose -dir migrations postgres "$POSTGRES_URL" down

run:
	go run ./cmd/hub

compose-up:
	docker compose --env-file .env.example up --build -d

compose-down:
	docker compose down -v

compose-logs:
	docker compose logs -f --tail=200

fmt:
	gofmt -w cmd internal tests

lint:
	go vet ./...

test:
	go test ./...

test-int:
	go test ./tests/integration -v

build:
	go build ./...

check: fmt lint test build
