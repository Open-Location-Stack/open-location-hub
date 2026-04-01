# OMLOX V2 MQTT Specification

This document translates the OMLOX Hub PDF into an implementation-facing MQTT specification for this project.

Spec references:
- Chapter 14
- Chapter 15.3
- Relevant behavior references from chapters 7, 8, 10, 11, and 13.6

## Status

- MQTT is an optional OMLOX Hub extension.
- If this hub supports MQTT, it should implement all MQTT behavior described here.
- RPC over MQTT is part of the optional extension surface.

## Design constraints from the PDF

The MQTT extension is intentionally similar to the WebSocket pub/sub API, with two notable differences:
- no per-subscription projection/transformation support
- fewer filtering capabilities than WebSocket

The spec explicitly narrows MQTT projection support to:
- local coordinates
- WGS84 (`EPSG:4326`)

## Topic hierarchy

General form:
- `/omlox/{format}/{topic_root}/{sub_path_1}/.../{sub_path_n}`

Path components:
- `omlox`: fixed prefix for all OMLOX MQTT communication
- `{format}`: payload format
- `{topic_root}`: logical topic family such as `location_updates` or `rpc`
- `{sub_path_n}`: topic-specific path segments

Formats used by the hub spec:
- `json` for OMLOX payload objects
- `jsonrpc` for RPC request/response and method announcements

## MQTT requirements

- MQTT version must be at least 5 for retained RPC method announcements with expiry
- method announcement messages must use `retained=true`
- method announcement messages must use `messageExpiryInterval=120`

Repository behavior note:
- the hub publishes retained announcements for hub-owned methods
- retained announcement publication is part of the hub-owned method registry behavior

## Topic families

### Location updates

Purpose:
- ingest provider location updates
- publish processed location updates in local and WGS84 forms

Relevant behavior:
- inbound data must be processed using chapter 7.8
- resulting fence events and trackable motions must be generated and published to their MQTT topics

#### `/omlox/json/location_updates/pub/{provider_id}`

Inbound only, consumed by the hub.

Behavior:
- location updates sent here must be processed by the hub
- if the update uses local coordinates, the hub must transform it to WGS84
- the hub must then publish the processed update to both the local and WGS84 location topics when applicable
- if the input CRS is a supported EPSG projection, the hub must transform and publish the WGS84 form
- if the location update cannot be processed, it must not be published to either output topic

Payload:
- `Location`

#### `/omlox/json/location_updates/local/{provider_id}`

Hub-generated output only.

Behavior:
- contains processed location updates in local coordinates of the referenced zone
- location updates received via non-MQTT transports must also be published here when local transformation is applicable

Payload:
- `Location`

#### `/omlox/json/location_updates/epsg4326/{provider_id}`

Hub-generated output only.

Behavior:
- contains processed location updates in WGS84 coordinates
- all location updates received via non-MQTT transports must also be published here

Payload:
- `Location`

### Proximity updates

Purpose:
- ingest proximity-only observations from RFID, iBeacon, and similar systems

Relevant behavior:
- inbound data must be processed using chapter 7.13
- the resulting location must be published to the MQTT location update topics
- resulting fence events and trackable motions must also be generated and published

#### `/omlox/json/proximity_updates/{source}/{provider_id}`

Inbound only, consumed by the hub.

Behavior:
- the hub processes the proximity update
- in this repository, proximity processing includes stateful zone resolution and anti-flap stickiness before deriving a `Location`
- the resulting location must be published to:
  - `/omlox/json/location_updates/local/{provider_id}`
  - `/omlox/json/location_updates/epsg4326/{provider_id}`
- if the proximity update cannot be processed, the resulting location must not be published

Payload:
- `Proximity`

Operational note from the PDF:
- a hub that handles all zones may subscribe broadly to `/omlox/json/proximity_updates/#`

### Fence events

Purpose:
- publish hub-generated geofence entry and exit events

Relevant behavior:
- fence events must be generated according to chapter 8.2
- MQTT clients should treat these topics as read-only

The hub must publish each generated fence event to all applicable topics below.

#### `/omlox/json/fence_events/{fence_id}`

Behavior:
- publishes fence events for a specific fence

Payload:
- `FenceEvent`

#### `/omlox/json/fence_events/trackables/{trackable_id}`

Behavior:
- publishes fence events for a specific trackable

Payload:
- `FenceEvent`

#### `/omlox/json/fence_events/providers/{provider_id}`

Behavior:
- publishes fence events for a specific location provider

Payload:
- `FenceEvent`

### Trackable motions

Purpose:
- publish hub-generated `TrackableMotion` updates

Relevant behavior:
- processing must match the WebSocket `trackable_motions` behavior from chapter 13.6.6
- all generated trackable motions must be published to the WGS84 topic
- local-coordinate publication is required when the transformation is possible

#### `/omlox/json/trackable_motions/local/{trackable_id}`

Behavior:
- local-coordinate `TrackableMotion` for the zone referenced by the motion location

Payload:
- `TrackableMotion`

#### `/omlox/json/trackable_motions/epsg4326/{trackable_id}`

Behavior:
- WGS84 `TrackableMotion`

Payload:
- `TrackableMotion`

### Collision events

Purpose:
- publish hub-generated `CollisionEvent` updates

Relevant behavior:
- generation must follow chapter 13.6.3, chapter 11, and chapter 10 timeout behavior

#### `/omlox/json/collision_events/epsg4326`

Behavior:
- all generated collision events must be published here
- the PDF only defines the WGS84 collision topic

Payload:
- `CollisionEvent`

## RPC over MQTT

The OMLOX hub uses MQTT topic structures and JSON-RPC messages to communicate with method handlers.

### JSON-RPC constraints

Compared with plain JSON-RPC 2.0:
- request batching is forbidden
- positional parameters are forbidden
- named parameters only

Reserved parameter names:
- `_caller_id`
- `_timeout`
- `_aggregation`
- `_handler_id`

Semantics:
- `_caller_id`: required for MQTT requests that expect a response
- `_timeout`: positive integer in milliseconds; a handler must respond before it expires or produce timeout behavior
- `_aggregation`: used by the hub to aggregate multiple responses
- `_handler_id`: directs a request to a specific handler

### Mandatory methods

The OMLOX spec reserves and mandates support for:
- `com.omlox.ping`
- `com.omlox.identify`
- `com.omlox.core.xcmd`

Additional requirements:
- methods starting with `rpc.` are reserved by JSON-RPC
- methods starting with `com.omlox.` are reserved by OMLOX
- vendor methods must use reverse-DNS naming

For `com.omlox.core.xcmd`:
- the implementing handler must send corresponding XCMD payloads to the target core-zone device
- every received `XCMD_BC` must be broadcast on:
  - `/omlox/jsonrpc/rpc/com.omlox.core.xcmd/broadcast`

Repository note:
- `open-rtls-hub` exposes `com.omlox.core.xcmd` at the RPC layer and publishes `XCMD_BC` payloads returned by an adapter
- actual device-command execution still depends on a deployment-specific adapter

### Announcing available methods

Topic:
- `/omlox/{format}/rpc/available/{method_name}`
- for OMLOX v2 the format is `jsonrpc`

Message shape:

```json
{
  "id": "4D98F546-D76A-47E1-B023-BE5A029024B0",
  "method_name": "com.omlox.ping"
}
```

Behavior:
- `id` is the unique handler identifier, for example a zone id
- a method handler must announce each supported method once per minute
- the message must be retained
- the message expiry interval must be 120 seconds

### Sending RPC requests

Generic request topic:
- `/omlox/{format}/rpc/{method_name}/request`

Direct-routed request topic:
- `/omlox/{format}/rpc/{method_name}/request/{handler_id}`

Behavior:
- a method handler providing a method must subscribe to both the generic and handler-specific request topics
- if a response is expected, the request must include both `id` and `_caller_id`
- if no response is wanted, both `id` and `_caller_id` must be omitted

Example:

Topic:
- `/omlox/jsonrpc/rpc/com.omlox.ping/request`

Payload:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "com.omlox.ping",
  "params": {
    "_caller_id": "AE365CCC-9ACF-415F-AEEB-04A33C655A5B"
  }
}
```

### Responding to RPC requests

Response topic:
- `/omlox/{format}/rpc/{method_name}/response/{_caller_id}`

Behavior:
- notifications must not produce a response
- a response must use the same `id` as the request that reached the handler
- the caller must subscribe to the response topic before sending the request

Example:

Topic:
- `/omlox/jsonrpc/rpc/com.omlox.ping/response/AE365CCC-9ACF-415F-AEEB-04A33C655A5B`

Payload:

```json
{
  "jsonrpc": "2.0",
  "id": "1",
  "result": "Pong"
}
```

### Listing available methods

Base topic:
- `/omlox/{format}/rpc/available`

Behavior:
- the available method list is derived by listing retained announcement topics under this prefix
- actual method availability depends on retained-message freshness and expiry

### Hub responsibilities for RPC bridging

Even though the hub exposes RPC over REST, the hub-side implementation must also do the following over MQTT:
- subscribe to `/omlox/jsonrpc/rpc/available/#`
- discover available handlers from retained announcements
- publish retained availability announcements for hub-owned methods
- publish method requests to the generic or handler-specific request topics
- subscribe to the caller-specific response topic before sending a request
- collect responses according to `_aggregation`

Current repository behavior:
- hub-owned methods are announced with retained MQTT availability topics on startup and on reconnect
- the hub maintains one registry that includes both local handlers and MQTT-discovered external handlers
- malformed downstream response payloads are dropped from aggregation and logged

### Aggregation behavior used by the hub

Supported strategies:
- `_all_within_timeout`
- `_return_first_success`
- `_return_first_error`

Rules:
- default is `_all_within_timeout`
- unknown aggregation strategy must yield JSON-RPC error `-32602` and the request must not be forwarded
- `_handler_id` and `_aggregation` together are invalid and must yield `-32602`
- unknown handler id must yield `-32000`
- request timeout must yield `-32001`
- “no non-error response” must yield `-32002`
- malformed downstream responses are treated as JSON-RPC errors in the current implementation and are logged for operators

## Payload mapping

Unless the topic is explicitly JSON-RPC:
- `location_updates` topics carry `Location`
- `proximity_updates` topics carry `Proximity`
- `fence_events` topics carry `FenceEvent`
- `trackable_motions` topics carry `TrackableMotion`
- `collision_events` topics carry `CollisionEvent`

For RPC topics:
- payloads are JSON-RPC request/response objects with the OMLOX restrictions and reserved parameters described above

## Implementation checklist

If this hub implements MQTT, it should implement:
- topic hierarchy rooted at `/omlox/...`
- MQTT v5 support for retained/expiring RPC announcements
- location ingest and processed location publication
- proximity ingest and derived location publication
- fence event fan-out to all applicable topics
- trackable motion publication in WGS84 and local coordinates when possible
- collision event publication in WGS84
- RPC method announcements, request handling topics, and response topics
- support for OMLOX reserved RPC parameters and error codes

## Reference implementation notes

The following notes come from a public reference implementation. They are useful as reference implementation guidance, but they are not automatically normative for this repository unless they match the OMLOX PDF or we explicitly adopt them.

Sources:
- Public MQTT topic reference
- Product overview via public articles

### Confirmed useful details

- The reference implementation documents the same high-level split between unprocessed inbound data and hub-published processed data.
- The reference implementation reinforces the OMLOX rationale that MQTT supports only local and WGS84 projections, not arbitrary per-subscriber projections.
- The reference implementation documents object types on the MQTT topics exactly as expected:
  - `Location`
  - `Proximity`
  - `FenceEvent`
  - `TrackableMotion`
  - `CollisionEvent`

### Product-specific behavior worth knowing

- The reference implementation documents extra MQTT topic families for object change events, such as `fence_changes`, `trackable_changes`, `provider_changes`, `zone_changes`, and `anchor_changes`. These are product extensions, not part of the OMLOX MQTT extension.
- The reference implementation also documents Unified Namespace support rooted under a configurable prefix. That is product-specific and not part of OMLOX.

### Divergence to watch

- The OMLOX PDF defines the collision topic as `/omlox/json/collision_events/epsg4326`.
- The public MQTT reference describes publication to `omlox/json/collision_events/epsg4326/{trackable_id}`.
- Treat the PDF form as normative for this repository unless we intentionally add the product-specific variant as an extension.

### Guidance for this repository

- Implement the OMLOX MQTT topic hierarchy first.
- Keep product-specific MQTT additions separate and clearly labeled if we choose to support them later.
