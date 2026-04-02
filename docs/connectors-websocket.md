# Connector Guide: WebSocket

Use WebSocket when you want the simplest connector path into the hub. It maps
well to the current bundled demos, it works cleanly with the hub's auth model,
and it does not require you to design topic routing beyond the OMLOX WebSocket
topic names.

The bundled connector demonstrators in this repository currently use this
approach:

- [`connectors/gtfs/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/gtfs/README.md)
- [`connectors/opensky/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/opensky/README.md)

Protocol details live in
[`specifications/omlox/websocket.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/specifications/omlox/websocket.md).

## When To Choose WebSocket

Choose WebSocket when:

- your connector is an application-style process that already manages a direct
  connection to the hub
- you want the same ingest path as the bundled examples
- you want per-message auth through `params.token`
- you want to subscribe to hub topics for validation, logging, or downstream
  processing in the same transport family

## Required Inputs

Most WebSocket-based connectors need:

- `HUB_HTTP_URL`: REST base URL for metadata bootstrap such as
  `http://localhost:8090`
- `HUB_WS_URL`: WebSocket endpoint such as `ws://localhost:8090/v2/ws/socket`
- `HUB_TOKEN`: optional JWT access token; required when auth is enabled

The bundled examples also keep upstream URLs, polling intervals, provider IDs,
and source-specific settings in environment variables.

## Minimal Runtime Flow

The common flow is:

1. Load environment variables.
2. Create a REST client for provider or trackable upserts when needed.
3. Open a WebSocket connection to `GET /v2/ws/socket`.
4. Map upstream data to OMLOX payloads.
5. Send wrapper `message` events to the right topic.
6. Reconnect and retry when the socket breaks.

The bundled helper modules also keep the connection alive with periodic ping
frames and reconnect on send failure.

## Publish Shape

For ingest, the connector sends OMLOX wrapper messages. A typical
`location_updates` publish looks like this:

```json
{
  "event": "message",
  "topic": "location_updates",
  "payload": [
    {
      "position": { "type": "Point", "coordinates": [8.5705, 50.0333] },
      "crs": "EPSG:4326",
      "provider_id": "my-connector",
      "provider_type": "virtual",
      "source": "upstream:object-123"
    }
  ],
  "params": {
    "token": "..."
  }
}
```

Notes:

- `payload` is an array, even when you only send one item
- omit `params.token` only when hub auth is disabled
- publish to the topic that matches the payload type and intended behavior
- `location_updates` is the normal entry point for live location ingest

## Subscribe Shape

The same transport can be used to observe hub output topics. A minimal
subscription message looks like this:

```json
{
  "event": "subscribe",
  "topic": "fence_events",
  "params": {
    "token": "..."
  }
}
```

This is useful when you want to:

- validate what the hub emits after ingest
- capture NDJSON logs during local testing
- confirm that your metadata bootstrap and location mapping behave as expected

## Metadata Bootstrap

Most connectors need more than a socket sender. The bundled examples also use
REST for idempotent metadata setup:

- create or update the `LocationProvider`
- upsert `Trackable` resources when the upstream source has stable asset IDs
- optionally create `Zone` and `Fence` resources before ingest starts

That split is usually the easiest connector design:

- REST for durable metadata and bootstrap
- WebSocket for live ingest and optional subscriptions

## Local Run Path

For local development:

1. Start the shared demo runtime:

```bash
connectors/local-hub/start_demo.sh
```

2. Fetch a token when auth is enabled:

```bash
connectors/local-hub/fetch_demo_token.sh
```

3. Run your connector against the local hub with `HUB_HTTP_URL`,
   `HUB_WS_URL`, and optionally `HUB_TOKEN`.

See
[`connectors/local-hub/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/local-hub/README.md)
for the reusable local stack.

## Example Projects

Use these as concrete WebSocket examples:

- [`connectors/gtfs/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/gtfs/README.md):
  REST bootstrap for provider, trackables, stations, and fences, then live
  `location_updates` publish over WebSocket
- [`connectors/opensky/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/opensky/README.md):
  REST bootstrap for provider, trackables, and airport fences, then live
  `location_updates` publish over WebSocket

If you want to build your own connector quickly, copying one of those patterns
is the shortest path.
