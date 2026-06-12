# Hetzner Deployment Bundle

This directory contains a production-oriented Docker Compose setup for running
Open Location Hub on a Hetzner host with:

- `tryformation/openlocationhub:0.1.8`
- local PostgreSQL
- local Mosquitto, which the hub runtime requires at startup
- an OpenTelemetry Collector that forwards traces, metrics, and logs to an
  Elasticsearch node already running on the host at `http://localhost:9200`
- OIDC authentication against `https://api.tryformation.com`

## Files

- `docker-compose.yml`: stack definition
- `.env.example`: deployment environment template
- `permissions.yaml`: example authorization policy keyed by JWT role values
- `otel-collector-config.yaml`: OTLP receiver plus Elasticsearch exporter

## Host Assumptions

- Docker Engine and Compose plugin are installed on the Hetzner server.
- Elasticsearch is already running on the host without auth on port `9200`.
- Docker supports the `host-gateway` alias so the collector can reach the host
  via `host.docker.internal`.
- The access tokens minted by `https://api.tryformation.com` include:
  - an audience matching `AUTH_AUDIENCE`
  - a claim matching `AUTH_ROLES_CLAIM`

## First Start

```bash
cd deploy/hetzner
cp .env.example .env
docker compose up -d
```

The stack runs a one-shot `migrate` container before the hub starts so the
Postgres schema is created automatically from the repository migrations.

## Required Auth Review

Two values depend on how your Formation auth server issues tokens:

- `AUTH_AUDIENCE`: set this to the audience or client ID expected in the access
  token for hub calls.
- `AUTH_ROLES_CLAIM`: the default is `groups`. If your tokens carry role
  membership in a different claim such as `roles` or `email`, change this and
  update `permissions.yaml` to match the emitted values.

The compose file already pins:

- `AUTH_MODE=oidc`
- `AUTH_ISSUER=https://api.tryformation.com`

The hub uses OIDC discovery and JWKS from that issuer.

## Elasticsearch Routing

The collector exports to:

- `http://host.docker.internal:9200`

That resolves back to the Linux host through Docker's host-gateway mapping. If
your Docker installation does not support `host-gateway`, replace that endpoint
in `otel-collector-config.yaml` with the host's reachable LAN or loopback bridge
address.

## Ports

- hub REST/WebSocket: `${HUB_PORT:-8080}`
- MQTT TCP: `${MQTT_PORT:-1883}`
- MQTT WebSocket: `${MQTT_WS_PORT:-9001}`
- OTLP gRPC: `${OTEL_GRPC_PORT:-4317}`
- OTLP HTTP: `${OTEL_HTTP_PORT:-4318}`

If you do not want to expose OTLP publicly, remove the collector port mappings.
