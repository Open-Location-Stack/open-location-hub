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
compose project. After SigNoz starts, the launcher bootstraps the local admin
account and provisions the default Open RTLS Hub dashboards.

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

- hub REST: `http://localhost:8080`
- hub WebSocket: `ws://localhost:8080/v2/ws/socket`
- SigNoz UI: `http://localhost:8090`
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

## Default SigNoz UI User

- `admin@local.test` / `SignozAdmin123!`

SigNoz authentication is local to SigNoz and is not connected to Dex. The demo start script bootstraps this fixed admin account through the SigNoz setup API on first run and reuses it on later starts.

## Default SigNoz Dashboards

The launcher creates or updates these dashboards on every start:

- `Open RTLS Hub Throughput`
- `Open RTLS Hub Latency`
- `Open RTLS Hub Outcomes`

This makes a fresh stack immediately useful for hub, replay, and OpenSky demo
traffic without requiring manual dashboard setup in the UI.
