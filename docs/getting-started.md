# Getting Started

If you want to try Open RTLS Hub on your laptop, this repository includes two
ready-made local runtime paths:

- a basic compose stack from [`docker-compose.yml`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docker-compose.yml) with the hub, Postgres, Mosquitto, and Dex
- a local demo stack with observability in [`local-hub/`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/local-hub) with the hub, Postgres, Mosquitto, Dex, SigNoz, ClickHouse, and the OpenTelemetry collector

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

As of 2026-04-27, the published tags are `0.1.0` and `latest`, and `latest`
points to the same image as `0.1.0`.

Pull the current release explicitly with:

```bash
docker pull tryformation/openlocationhub:0.1.0
```

Or follow the moving release tag with:

```bash
docker pull tryformation/openlocationhub:latest
```

Use the published image when you already have Postgres, Mosquitto, and your
identity provider handled elsewhere. Use the local compose and demo stacks when
you want the full reference setup from this repository.

## Which Stack To Use

- Basic compose stack:
  best when you want the hub plus its core dependencies only
- Local demo stack with observability:
  best when you want the hub plus a prewired observability setup for traces, metrics, and logs

The basic stack uses the repository root [`docker-compose.yml`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docker-compose.yml).
The local demo stack is documented in [`local-hub/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/local-hub/README.md).

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
- Alternative OpenTelemetry-compatible collectors and observability stacks should work as well.

## Good Next Steps

- [`connectors/gtfs/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/gtfs/README.md) for GTFS transit vehicle ingest over WebSocket or MQTT
- [`connectors/opensky/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/opensky/README.md) for OpenSky aircraft ingest over WebSocket
- [`connectors/replay/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/replay/README.md) for replaying captured `location_updates` traffic
- [`docs/index.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/index.md) for the full software documentation set
- [`docs/connectors.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/connectors.md) for connector structure and transport guidance
