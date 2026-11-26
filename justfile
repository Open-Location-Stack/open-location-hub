set shell := ["bash", "-euo", "pipefail", "-c"]

proj-env := 'PATH="$PWD/tools/bin:$PATH" PKG_CONFIG="$PWD/tools/bin/pkg-config"'
staticcheck-version := "v0.8.2"
govulncheck-version := "v1.8.0"
protobuf-go-version := "v1.36.12"

bootstrap:
	@if ! command -v oapi-codegen >/dev/null || ! oapi-codegen -version 2>/dev/null | grep -q "v2.8.0"; then \
		go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0; \
	fi
	@if ! command -v sqlc >/dev/null || ! sqlc version 2>/dev/null | grep -q "v1.31.1"; then \
		go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1; \
	fi
	@if ! command -v goose >/dev/null || ! goose -version 2>/dev/null | grep -q "v3.28.0"; then \
		go install github.com/pressly/goose/v3/cmd/goose@v3.28.0; \
	fi
	@if ! command -v staticcheck >/dev/null || ! staticcheck -version 2>/dev/null | grep -q "{{staticcheck-version}}"; then \
		go install honnef.co/go/tools/cmd/staticcheck@{{staticcheck-version}}; \
	fi
	@if ! command -v govulncheck >/dev/null || ! govulncheck -version 2>/dev/null | grep -q "{{govulncheck-version}}"; then \
		go install golang.org/x/vuln/cmd/govulncheck@{{govulncheck-version}}; \
	fi
	@if ! command -v protoc-gen-go >/dev/null || ! protoc-gen-go --version | grep -q ' {{protobuf-go-version}}$'; then \
		go install google.golang.org/protobuf/cmd/protoc-gen-go@{{protobuf-go-version}}; \
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
	@output_dir="$(mktemp -d)"; \
	trap 'rm -rf "$output_dir"' EXIT; \
	protoc -I specifications/events --go_out="$output_dir" --go_opt=paths=source_relative specifications/events/observation.v1.proto; \
	sed '/^\/\/[[:space:]]*protoc[[:space:]]/d' "$output_dir/observation.v1.pb.go" > internal/stream/pb/observation.v1.pb.go

migrate-up:
	goose -dir migrations postgres "$POSTGRES_URL" up

migrate-down:
	goose -dir migrations postgres "$POSTGRES_URL" down

run: proj-check
	{{proj-env}} go run ./cmd/hub

compose-up:
	docker compose --env-file .env.example up --build -d

# Start the local stack with a pre-migrated database and the Kafka shadow topic.
compose-shadow-up:
	docker compose --env-file .env.example -f docker-compose.yml -f deploy/kafka/compose.shadow.yaml up --build -d

# Run the asset input router against the topics created by compose-shadow-up.
compose-shadow-router:
	docker compose --env-file .env.example -f docker-compose.yml -f deploy/kafka/compose.shadow.yaml run --no-deps --rm app asset-router

# Run the restartable estimator against the asset input and checkpoint topics.
compose-shadow-estimator:
	docker compose --env-file .env.example -f docker-compose.yml -f deploy/kafka/compose.shadow.yaml run --no-deps --rm app asset-estimator

compose-down:
	docker compose down -v

compose-logs:
	docker compose logs -f --tail=200

local-hub-up:
	./local-hub/start_demo.sh

local-hub-down:
	./local-hub/stop_demo.sh

local-hub-token:
	./local-hub/fetch_demo_token.sh

local-hub-logs service="hub":
	docker compose -f local-hub/demo-compose.yml --env-file local-hub/demo.env logs -f --tail=200 {{service}}

fmt:
	gofmt -w cmd internal tests

lint-generated:
	just generate
	git diff --exit-code -- internal/httpapi/gen/api.gen.go internal/storage/postgres/sqlcgen internal/stream/pb

lint-mod:
	go mod tidy
	git diff --exit-code -- go.mod go.sum

lint: lint-generated lint-mod
	@packages="$(bash tools/bin/testable-packages lint)"; \
	analysis_packages="$(bash tools/bin/testable-packages analyze)"; \
	{{proj-env}} go vet $packages; \
	{{proj-env}} staticcheck $analysis_packages; \
	{{proj-env}} govulncheck $analysis_packages

test:
	@packages="$(bash tools/bin/testable-packages)"; \
	{{proj-env}} go test $packages

test-race:
	@packages="$(bash tools/bin/testable-packages)"; \
	{{proj-env}} go test -race $packages

test-int: proj-check
	{{proj-env}} go test ./tests/integration -v

build:
	@packages="$(bash tools/bin/testable-packages build)"; \
	{{proj-env}} go build $packages

check: fmt lint build test

# Validate every locked Python environment; UV_PYTHON selects the interpreter.
check-python:
	@for project in scripts connectors/opensky connectors/gtfs connectors/uwb_sim connectors/replay; do \
		uv sync --locked --project "$project"; \
		uv run --locked --project "$project" python -m compileall -q "$project" -x '/[.]venv/'; \
		uv run --locked --project "$project" python -c 'import requests, websocket'; \
	done
	uv run --locked --directory connectors/gtfs python -c 'import gtfs_support, connector_mqtt; from google.transit import gtfs_realtime_pb2; feed = gtfs_realtime_pb2.FeedMessage(); feed.header.gtfs_realtime_version = "2.0"; gtfs_realtime_pb2.FeedMessage.FromString(feed.SerializeToString())'
	uv run --locked --directory connectors/uwb_sim python -m unittest discover -p 'test_*.py'
	uv run --locked --directory connectors/gtfs python -m unittest discover -p 'test_*.py'
	uv run --locked --directory connectors/replay python -m unittest discover -p 'test_*.py'

# Fast package tests without launching integration containers.
test-unit *args: proj-check
    {{proj-env}} go test {{args}} ./cmd/... ./internal/...
