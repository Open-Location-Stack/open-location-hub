# Local Hub Demo Runtime

This directory contains the reusable local runtime used by connector demos in
this repository.

The stack starts:

- SigNoz plus its ClickHouse and collector dependencies for telemetry inspection
- the hub
- Postgres with persistent bind-mounted data
- Dex for local OIDC tokens
- Mosquitto for parity with the normal hub runtime
- a migration container that applies the repository migrations before the hub starts

## Files

- `demo-compose.yml`: local stack definition
- `demo.env.example`: local environment template
- `start_demo.sh`: starts the stack and creates a local env file on first run
- `stop_demo.sh`: stops the stack without deleting persistent data
- `fetch_demo_token.sh`: fetches a Dex access token for manual calls or connectors

## Usage

Start the local hub:

```bash
connectors/local-hub/start_demo.sh
```

The first run also clones the pinned SigNoz deploy repository revision declared
in `demo.env` and starts that stack separately before starting the hub demo
compose project.

Stop it:

```bash
connectors/local-hub/stop_demo.sh
```

Fetch an admin token:

```bash
connectors/local-hub/fetch_demo_token.sh
```

The first run creates `connectors/local-hub/demo.env` from `demo.env.example`.

Default local URLs:

- hub REST: `http://localhost:8090`
- hub WebSocket: `ws://localhost:8090/v2/ws/socket`
- SigNoz UI: `http://localhost:8080`
- OTLP gRPC: `localhost:4317`
- OTLP HTTP: `localhost:4318`

## Persistent State

Postgres state is stored under:

- `connectors/local-hub/state/postgres`

SigNoz deploy checkout defaults to:

- `connectors/local-hub/state/signoz`

## Default Dex Users

- `admin@example.com` / `testpass123`
- `reader@example.com` / `testpass123`
- `owner@example.com` / `testpass123`
