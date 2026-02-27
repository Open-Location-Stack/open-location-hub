set shell := ["bash", "-euo", "pipefail", "-c"]

bootstrap:
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.6.0
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0
	go install github.com/pressly/goose/v3/cmd/goose@v3.27.0

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
