# Connector Guide: MQTT

Use MQTT when your deployment already centers on a broker, when trusted edge
adapters naturally publish broker messages, or when broker-based routing fits
better than a long-lived WebSocket client connection.

This repository ships a concrete MQTT connector example in
[`connectors/gtfs/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/gtfs/README.md).
The MQTT connector path is documented here against the hub's implemented OMLOX
MQTT surface. Transport details live in
[`specifications/omlox/mqtt.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/specifications/omlox/mqtt.md).

## When To Choose MQTT

Choose MQTT when:

- your source system already publishes to a broker
- you are running trusted local or edge adapters near devices
- you want broker-native fan-out, retention, or topic-based routing
- WebSocket is not the natural operational fit for the connector process

For user-facing applications and general connector examples, WebSocket is often
the simpler starting point. See
[docs/connectors-websocket.md](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/connectors-websocket.md).

## Required Inputs

An MQTT-based connector usually needs:

- `HUB_HTTP_URL`: REST base URL for optional metadata bootstrap
- `MQTT_BROKER_URL`: broker URL such as `tcp://localhost:1883`
- MQTT client identity and auth settings that fit your deployment
- `HUB_TOKEN` only if the connector also uses REST against an auth-enabled hub

The hub's runtime broker URL is documented in
[`docs/configuration.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/configuration.md).

## Minimal Runtime Flow

The connector shape is almost the same as the WebSocket path:

1. Load environment variables.
2. Optionally use REST to ensure the provider and other metadata exist.
3. Connect to the MQTT broker.
4. Map upstream data to OMLOX payloads.
5. Publish each payload to the correct OMLOX ingest topic.
6. Optionally subscribe to hub-generated output topics for validation.
7. Reconnect and retry when broker connectivity fails.

The main difference is transport mapping. Instead of sending an OMLOX wrapper,
the connector publishes the payload body directly on the correct topic.

## Inbound Versus Outbound Topics

MQTT connector authors need to keep one boundary clear:

- inbound ingest topics are written by the connector and consumed by the hub
- outbound topics are written by the hub after it processes or derives data

Examples from the existing MQTT mapping:

- connector writes location ingest to
  `/omlox/json/location_updates/pub/{provider_id}`
- connector writes proximity ingest to
  `/omlox/json/proximity_updates/{source}/{provider_id}`
- hub writes processed locations to
  `/omlox/json/location_updates/local/{provider_id}` and
  `/omlox/json/location_updates/epsg4326/{provider_id}`
- hub writes derived events to topic families such as `fence_events`,
  `trackable_motions`, and `collision_events`

Do not publish directly to the hub-generated output topics. Those exist so the
hub can publish normalized or derived results after it processes the inbound
payload.

## Topic Selection

The correct topic depends on the payload type:

- publish `Location` ingest to
  `/omlox/json/location_updates/pub/{provider_id}`
- publish `Proximity` ingest to
  `/omlox/json/proximity_updates/{source}/{provider_id}`
- consume processed and derived hub output from the output topic families when
  you need validation or downstream processing

Use
[`specifications/omlox/mqtt.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/specifications/omlox/mqtt.md)
as the source of truth for topic families and payload expectations.

## Example Ingest Shape

For MQTT, the message body is the normalized OMLOX object itself. For example,
a connector publishing a location update would send a `Location` JSON payload to
`/omlox/json/location_updates/pub/{provider_id}`:

```json
{
  "position": { "type": "Point", "coordinates": [8.5705, 50.0333] },
  "crs": "EPSG:4326",
  "provider_id": "my-connector",
  "provider_type": "virtual",
  "source": "upstream:object-123"
}
```

## Reusing The Bundled Connector Pattern

Even though the transport changes, the overall connector design can stay the
same as the bundled examples:

- keep env-driven configuration
- keep a small REST helper for metadata bootstrap
- keep the upstream polling or subscription loop separate from the transport
  client
- keep mapping code focused on OMLOX payload construction

These repository examples are the best templates for that structure:

- [`connectors/gtfs/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/gtfs/README.md):
  includes both a WebSocket connector and an MQTT connector that publish the
  same normalized GTFS-derived `Location` payloads
- [`connectors/opensky/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/opensky/README.md)

The GTFS project is now the direct MQTT example. The OpenSky project remains a
useful example for shared project layout, env handling, and REST bootstrap.

## Local Run Path

For local development, use the shared demo stack:

```bash
local-hub/start_demo.sh
```

That stack includes the hub, Postgres, Dex, and Mosquitto. See
[`local-hub/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/local-hub/README.md)
for the local runtime details and the token helper if your connector also uses
REST against an auth-enabled hub.
