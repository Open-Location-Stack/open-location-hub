# Local Hub Starter Stack

This directory is the easiest way to get Open RTLS Hub running on a laptop with
a practical local observability setup. It is intended as the normal starting
point for new users who want to bring up the hub locally, inspect telemetry,
and then try one of the example connectors.

The stack starts:

- the hub
- Postgres with persistent bind-mounted data
- Mosquitto for parity with the normal hub runtime
- Dex for local OIDC tokens
- SigNoz plus its ClickHouse and collector dependencies for telemetry inspection
- a migration container that applies the repository migrations before the hub starts

Useful next stops after the stack is up:

- [`docs/getting-started.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/getting-started.md) for the shortest path through local setup
- [`docs/index.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/index.md) for the broader software docs
- [`connectors/gtfs/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/gtfs/README.md) for a transit-focused connector example
- [`connectors/opensky/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/opensky/README.md) for an aircraft-position example
- [`connectors/replay/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/replay/README.md) for replaying captured traffic back into the hub

## Development Scope

- This setup is intended for local development, demos, and laptop experimentation.
- Dex is a good fit for that workflow, but the included Dex fixture should not be treated as a production identity setup.
- SigNoz is included because it is a modern observability stack that is easy to script and automate on top of ClickHouse, but the hub does not require SigNoz specifically.
- You should be able to point the hub at other OpenTelemetry-compatible collectors and observability stacks instead.

## Files

- `demo-compose.yml`: local stack definition
- `demo.env.example`: local environment template
- `start_demo.sh`: starts the stack and creates a local env file on first run
- `stop_demo.sh`: stops the stack without deleting persistent data
- `fetch_demo_token.sh`: fetches a Dex access token for manual calls or connectors

## Usage

Start the local stack:

```bash
local-hub/start_demo.sh
```

The first run also clones the pinned SigNoz deploy repository revision declared
in `demo.env` and starts that stack separately before starting the hub demo
compose project. After SigNoz starts, the launcher bootstraps the local admin
account and provisions the default Open RTLS Hub dashboards.

Stop it:

```bash
local-hub/stop_demo.sh
```

Fetch an admin token:

```bash
local-hub/fetch_demo_token.sh
```

The first run creates `local-hub/demo.env` from `demo.env.example`.

Default local URLs:

- hub REST: `http://localhost:8080`
- hub WebSocket: `ws://localhost:8080/v2/ws/socket`
- SigNoz UI: `http://localhost:8090`
- OTLP gRPC: `localhost:4317`
- OTLP HTTP: `localhost:4318`

## Persistent State

Postgres state is stored under:

- `local-hub/state/postgres`

SigNoz deploy checkout defaults to:

- `local-hub/state/signoz`

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
