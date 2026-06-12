# Getting Started

If you want to try Open Location Hub on your laptop, this repository includes two
ready-made local runtime paths:

- a basic compose stack from [`docker-compose.yml`](docker-compose.yml) with the hub, Postgres, Mosquitto, and Dex
- a local demo stack with observability in [`local-hub/`](local-hub) with the hub, Postgres, Mosquitto, Dex, SigNoz, ClickHouse, and the OpenTelemetry collector

Use the basic stack if you want the shortest path to a working hub runtime.
Use the local demo stack if you also want observability while you
experiment.

## Fast Path

For the basic local stack:

```bash
cp .env.example .env
just compose-up
```

For the local demo stack with observability:

```bash
just local-hub-up
```

If you prefer the underlying scripts for the local demo stack:

```bash
local-hub/start_demo.sh
local-hub/fetch_demo_token.sh
```

## Published Docker Image

If you do not want to build the hub first, use the published Docker Hub image:
[`tryformation/openlocationhub`](https://hub.docker.com/r/tryformation/openlocationhub).

As of 2026-06-11, the published tags include `0.1.8` and `latest`, and
`latest` points to the current release image.

Pull the current release explicitly with:

```bash
docker pull tryformation/openlocationhub:0.1.8
```

Or follow the moving release tag with:

```bash
docker pull tryformation/openlocationhub:latest
```

Use the published image when you already have Postgres, Mosquitto, and your
identity provider handled elsewhere. Use the local compose and demo stacks when
you want the full reference setup from this repository.

## Command Line Client

The companion CLI lives in
[`Open-Location-Stack/open-location-hub-cli`](https://github.com/Open-Location-Stack/open-location-hub-cli)
and installs as `olh`.

Install it with Homebrew:

```bash
brew tap jillesvangurp/tap
brew install jillesvangurp/tap/open-location-hub-cli
```

For the local Dex fixture:

```bash
olh login \
  --hub-endpoint http://localhost:8080 \
  --token-endpoint http://localhost:5556/dex/token \
  --client-id open-location-cli \
  --client-secret cli-secret \
  --oauth-username admin@example.com \
  --oauth-password testpass123
```

The CLI covers resource CRUD, ingest helpers, WebSocket streams, and RPC calls.

## Which Stack To Use

- Basic compose stack:
  best when you want the hub plus its core dependencies only
- Local demo stack with observability:
  best when you want the hub plus a prewired observability setup for traces, metrics, and logs

The basic stack uses the repository root [`docker-compose.yml`](docker-compose.yml).
The local demo stack is documented in [`local-hub/README.md`](../local-hub/README.md).

## What This Setup Is For

- local development and experimentation
- validating connector behavior against a real hub runtime
- inspecting OTLP telemetry during laptop demos and debugging

This starter stack is not positioned as a production deployment recipe.

## Notes

- the basic compose stack includes the hub, Postgres, Mosquitto, and Dex
- Dex is included because it is convenient for local OIDC and repeatable demo users, not because it is the recommended production IdP choice.
- the local demo stack adds SigNoz, ClickHouse, and the OpenTelemetry collector around that core runtime
- SigNoz is included because it is easy to bootstrap and script for modern local observability workflows, but the hub does not depend on SigNoz specifically.
- The hub exports standard OTLP signals, so it can use other OpenTelemetry-compatible collectors and observability stacks.

## Good Next Steps

- [`connectors/gtfs/README.md`](../connectors/gtfs/README.md) for GTFS transit vehicle ingest over WebSocket or MQTT
- [`connectors/opensky/README.md`](../connectors/opensky/README.md) for OpenSky aircraft ingest over WebSocket
- [`connectors/replay/README.md`](../connectors/replay/README.md) for replaying captured `location_updates` traffic
- [`docs/index.md`](index.md) for the full software documentation set
- [`docs/connectors.md`](connectors.md) for connector structure and transport guidance
