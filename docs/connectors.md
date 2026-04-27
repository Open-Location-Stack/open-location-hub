# Connectors

This repository keeps connectors outside the hub runtime. A connector is a
small process that reads data from some upstream system, maps that data to the
hub's OMLOX-facing model, and submits it through the hub's supported transport
surfaces.

The key point is simple: creating a connector is easy. You do not need to patch
the hub. In most cases you only need:

1. a small runtime loop that reads upstream data
2. some mapping code that builds OMLOX `Location` or `Proximity` payloads
3. optional REST bootstrap calls for providers, trackables, zones, or fences
4. a transport publisher for WebSocket or MQTT

## Bundled Connector Examples

The repository already includes a few connector-oriented projects under
[`connectors/`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors):

- [`connectors/gtfs/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/gtfs/README.md):
  GTFS-RT vehicle ingest plus station-zone and fence bootstrap
- [`connectors/opensky/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/opensky/README.md):
  OpenSky aircraft ingest plus airport fence bootstrap
- [`connectors/replay/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/replay/README.md):
  NDJSON trace replay back into the hub for diagnostic or demo use
- [`connectors/uwb_sim/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/uwb_sim/README.md):
  mock 3-floor local UWB simulator with generated floorplan images and building metadata bootstrap

The bundled runtime connectors now cover both transport styles. The GTFS
project includes WebSocket and MQTT ingest variants, and the OpenSky project
shows the WebSocket path. Together they are useful examples for connector
structure, env handling, bootstrap logic, and local development flow.

The recommended local runtime itself now lives outside `connectors/` under
[`local-hub/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/local-hub/README.md)
because it is broader than connector bootstrapping. It is the normal starting
point when you want a local hub plus observability stack on your laptop.

## Easy Path

Most custom connectors follow the same shape:

1. Pick a transport.
   Use WebSocket when you want the simplest general-purpose hub-facing ingest
   path. Use MQTT when your deployment already centers on a broker or trusted
   edge adapters.
2. Prepare env-driven config.
   Keep hub URLs, broker settings, auth tokens, upstream URLs, and poll
   intervals in environment variables.
3. Create or reconcile hub metadata when needed.
   Many connectors need a `LocationProvider` and sometimes `Trackable`, `Zone`,
   or `Fence` resources before live ingest starts.
4. Map upstream records to OMLOX payloads.
   Normalize each upstream event to a hub payload such as `Location` or
   `Proximity`.
5. Publish to the hub.
   Send those normalized payloads over WebSocket or MQTT using the transport's
   expected wrapper or topic layout.
6. Run locally against the shared demo stack.
   Use [`local-hub/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/local-hub/README.md)
   for a reproducible local hub, Postgres, Dex, Mosquitto, and optional SigNoz environment.

## Shared Connector Shape

The bundled connectors show a practical split that is easy to copy:

- one runtime script that polls or subscribes to the upstream source
- one small client helper module for hub REST and transport calls
- optional bootstrap scripts for zones, fences, or other metadata
- simple env-file loading so the connector can run locally or in a container

The shared flow looks like this:

1. Load env-driven configuration.
2. Connect to the hub transport surface.
3. Ensure the provider exists through REST.
4. Optionally upsert trackables or bootstrap fences and zones.
5. Read upstream data.
6. Map it to OMLOX payloads.
7. Publish the payloads.
8. Retry or reconnect when the upstream source or hub transport fails.

## Running A Connector Locally

The recommended local development path is the shared demo runtime:

1. Start the local stack:

```bash
local-hub/start_demo.sh
```

2. If auth is enabled, fetch a token:

```bash
local-hub/fetch_demo_token.sh
```

3. Create a connector-local env file with the hub URLs, optional token, and any
   upstream-specific settings.
4. Run the connector process.

The bundled demos use `HUB_HTTP_URL` for REST bootstrap work and `HUB_WS_URL`
for WebSocket ingest. MQTT-based connectors would typically use `HUB_HTTP_URL`
plus `MQTT_BROKER_URL` or equivalent broker settings.

## Choosing A Transport

- [docs/connectors-websocket.md](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/connectors-websocket.md)
  is the best starting point if you want the same transport flow as the bundled
  runtime connectors.
- [docs/connectors-mqtt.md](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/connectors-mqtt.md)
  explains how to build the same kind of connector on top of the hub's OMLOX
  MQTT surface.

## Where To Copy From

If you want a fast starting point:

- copy the project structure and env handling style from
  [`connectors/gtfs/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/gtfs/README.md)
  or
  [`connectors/opensky/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/opensky/README.md)
- use
  [`local-hub/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/local-hub/README.md)
  as the local runtime guide
- use
  [`specifications/omlox/websocket.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/specifications/omlox/websocket.md)
  or
  [`specifications/omlox/mqtt.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/specifications/omlox/mqtt.md)
  as the transport source of truth

That is usually enough to get a first connector running quickly.
