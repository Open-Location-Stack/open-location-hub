# OMLOX V2 WebSocket Specification

This document translates the OMLOX Hub PDF into an implementation-facing WebSocket specification for this project.

Spec references:
- Chapter 6.6
- Sections 6.7.15 to 6.7.18
- Chapter 13

## Status

- WebSocket support is mandatory for an OMLOX Hub implementation.
- This document covers the hub-facing publish/subscribe behavior the hub must implement.
- Current repository status:
  - `GET /v2/ws/socket` is implemented
  - the hub accepts `message`, `subscribe`, and `unsubscribe` and emits `message`, `subscribed`, `unsubscribed`, and `error`
  - MQTT and WebSocket now consume the same internal hub-event stream
  - `collision_events` is implemented behind the runtime flag `COLLISIONS_ENABLED` and returns `10002` when the topic is requested while disabled
  - `metadata_changes` exists as a repository-specific extension topic and is documented separately in `metadata-sync-extension.md`

## Endpoint

- `GET /v2/ws/socket`
- The client establishes a WebSocket connection and then exchanges JSON messages over that connection.

## Connection and subscription lifecycle

- The hub enables clients to subscribe and unsubscribe from named topics.
- A client may subscribe to the same topic multiple times with different parameters.
- The hub must generate a unique `subscription_id` per successful subscription.
- `subscription_id` uniqueness is only required for the runtime of the hub instance.
- All subscriptions on a connection must be terminated automatically when that connection closes.
- A client must be able to publish to topics that allow incoming data without first subscribing.

## Wrapper object

All client/server WebSocket data is exchanged using a wrapper object.

Required field:
- `event`

Conditionally used fields:
- `topic`
- `subscription_id`
- `payload`
- `params`

### Wrapper fields

- `event`: string
  Allowed values: `message`, `subscribe`, `subscribed`, `unsubscribe`, `unsubscribed`, `error`
- `topic`: string
  Interpreted according to the topic name
- `subscription_id`: integer
  Generated and maintained by the hub for successful subscriptions
- `payload`: array
  Contains objects of the type associated with the topic; may be empty
- `params`: object
  Flat key/value object for topic-specific subscription parameters and authorization data

## Event types

### `message`

Used when data is sent or delivered on a topic.

- Clients use `message` to submit data to topics that accept inbound payloads.
- The hub uses `message` to publish topic data to subscribers.

Required fields:
- `event`
- `topic`
- `payload`

Allowed fields:
- `subscription_id`
- `params`

### `subscribe`

Used by the client to create a subscription.

Required fields:
- `event`
- `topic`

Optional fields:
- `params`

### `subscribed`

Used by the hub to acknowledge a successful subscription.

Required fields:
- `event`
- `topic`
- `subscription_id`

### `unsubscribe`

Used by the client to remove a subscription.

Required fields:
- `event`
- `subscription_id`

### `unsubscribed`

Used by the hub to acknowledge a successful unsubscribe.

Required fields:
- `event`
- `topic`
- `subscription_id`

### `error`

Used by the hub to report protocol or authorization failures.

Required fields:
- `event`
- `code`

Optional fields:
- `description`

Error codes mandated by the spec:
- `10000`: unknown `event`
- `10001`: unknown topic
- `10002`: subscription request failed
- `10003`: unsubscribe request failed
- `10004`: client is not authorized
- `10005`: payload contains invalid data

Repository note:
- the implementation uses `10002` for known-but-disabled topics such as `collision_events` when collision support is turned off at runtime

## Authorization

- The hub must be able to handle authentication and authorization using OpenID.
- Authentication may be disabled.
- When authorization is enabled, the hub must authenticate and authorize each message.
- The JWT access token must be passed in `params.token`.

Example:

```json
{
  "event": "subscribe",
  "topic": "fence_events",
  "params": {
    "token": "eyJhbG..."
  }
}
```

## Mandatory topics

The hub must implement all of the following topics.

### `location_updates`

Purpose:
- receive real-time location updates from clients
- publish processed location updates to subscribers

Inbound payload:
- array of `Location`

Outbound payload:
- array of `Location`

Hub behavior:
- inbound data must be processed exactly like chapter 7.8 location processing
- data received via REST or WebSocket must be sent immediately to subscribers
- clients may publish without subscribing first

Client publish allowed:
- yes

Subscription filters:
- `provider_id`: string, mandatory to implement
- `provider_type`: string, optional
  Allowed values named by the PDF: `uwb`, `gps`, `wifi`, `rfid`, `ibeacon`, `virtual`, `unknown`
- `source`: string, optional
- `accuracy`: number, optional, match when location accuracy is less than or equal to the filter value
- `floor`: number, optional
- `crs`: string, optional
  Must use the same rules as the REST API `crs`/`zone_id` handling
- `zone_id`: string, optional
  Must use the same rules as the REST API `crs`/`zone_id` handling

### `location_updates:geojson`

Purpose:
- publish processed locations as GeoJSON

Outbound payload:
- array of GeoJSON feature collections

Hub behavior:
- each location update must be sent immediately as GeoJSON
- the `Location.position` becomes the GeoJSON feature geometry
- all remaining `Location` members go into the GeoJSON feature `properties`

Client publish allowed:
- no

Subscription filters:
- same as `location_updates`

### `collision_events`

Purpose:
- publish real-time `CollisionEvent` objects generated by the hub

Outbound payload:
- array of `CollisionEvent`

Hub behavior:
- the hub must check trackable movements for collisions
- collision events must be sent immediately
- all collision events are hub-generated
- current repository behavior is bounded to single-hub trackable-versus-trackable collision evaluation in WGS84
- collision thresholds are meter-based and use `Trackable.radius` when present, otherwise `COLLISION_DEFAULT_RADIUS_METERS`
- WGS84 collision checks use a short-range planar approximation instead of geodesic math so evaluation stays cheap on the runtime hot path
- collision evaluation is enabled only when `COLLISIONS_ENABLED=true`

Client publish allowed:
- no

Subscription filters:
- `object_id_1`: string, mandatory to implement
- `object_id_2`: string, mandatory to implement
- `collision_type`: string, optional
  Allowed values: `collision_start`, `colliding`, `collision_end`
- `floor`: number, optional
- `crs`: string, optional
  Applies to all position data; same processing rules as REST `crs`/`zone_id`
- `zone_id`: string, optional
  Applies to all position data; same processing rules as REST `crs`/`zone_id`

### `fence_events`

Purpose:
- publish real-time `FenceEvent` objects generated by the hub

Outbound payload:
- array of `FenceEvent`

Hub behavior:
- the hub must evaluate fence behavior according to chapter 8
- the hub must publish each fence entry and fence exit event immediately
- all fence events are hub-generated

Client publish allowed:
- no

Subscription filters:
- `fence_id`: string, mandatory to implement
- `foreign_id`: string, optional
- `provider_id`: string, optional
- `trackable_id`: string, optional
- `event_type`: string, optional
  Allowed values: `region_entry`, `region_exit`
- `object_type`: string, optional
  Allowed values from the PDF text: `trackable`, `location_provider`

### `fence_events:geojson`

Purpose:
- publish fence events as GeoJSON

Outbound payload:
- array of GeoJSON feature collections

Hub behavior:
- each fence event must be sent immediately as GeoJSON
- the related `Fence.region` shape becomes the GeoJSON feature geometry
- all remaining `Fence` members go into the GeoJSON feature `properties`

Client publish allowed:
- no

Subscription filters:
- same as `fence_events`

### `trackable_motions`

Purpose:
- publish `TrackableMotion` objects for trackable movement updates

Outbound payload:
- array of `TrackableMotion`

Hub behavior:
- a `TrackableMotion` must be emitted immediately after the location of any assigned provider updates the trackable
- the hub must generate the motion geometry from the trackable's configured `position`, `geometry`, and `extrusion`
- all trackable motions are hub-generated

Client publish allowed:
- no

Subscription filters:
- `id`: string, mandatory to implement
- `provider_id`: string, optional
- `accuracy`: number, optional
- `floor`: number, optional
- `crs`: string, optional
  Same processing rules as REST `crs`/`zone_id`
- `zone_id`: string, optional
  Same processing rules as REST `crs`/`zone_id`

### `proximity_updates`

Purpose:
- receive real-time updates from proximity locating systems such as RFID or iBeacon

Inbound payload:
- array of `Proximity`

Hub behavior:
- inbound payload is processed like chapter 7.13 proximity processing
- for this repository, that includes stateful zone resolution with short-lived stickiness to reduce rapid zone flapping

Client publish allowed:
- yes

Subscription behavior:
- the PDF defines this as a mandatory topic but does not define subscription filters for it

## Data submission without subscription

- A client may send `message` events without subscribing first when the topic accepts inbound data.
- The hub must process such data the same way as the corresponding REST endpoints.
- In practice this means:
  - `location_updates` is equivalent to REST location ingestion
  - `proximity_updates` is equivalent to REST proximity ingestion

Example:

```json
{
  "event": "message",
  "topic": "location_updates",
  "payload": [
    {
      "position": {
        "type": "Point",
        "coordinates": [5, 4]
      },
      "source": "fdb6df62-bce8-6c23-e342-80bd5c938774",
      "provider_type": "uwb",
      "provider_id": "77:4f:34:69:27:40",
      "timestamp_generated": "2019-09-02T22:02:24.355Z",
      "timestamp_sent": "2019-09-02T22:02:24.355Z"
    }
  ]
}
```

## Implementation checklist

The hub should implement all of the following:
- WebSocket upgrade at `/v2/ws/socket`
- wrapper event handling for all six event types
- unique runtime `subscription_id` generation
- auto-cleanup of subscriptions on disconnect
- topic-specific authorization checks
- all mandatory topics
- all mandatory subscription filters
- inbound processing for `location_updates` and `proximity_updates`
- outbound publishing for location updates, GeoJSON locations, collision events, fence events, GeoJSON fence events, and trackable motions
- error responses with the mandated WebSocket error codes
- JWT-in-`params.token` handling when authorization is enabled

Current repository behavior notes:
- authorization is evaluated per WebSocket message using dedicated WebSocket topic permissions from the auth registry
- outbound delivery is protected by a per-connection buffer; slow subscribers are disconnected instead of blocking shared fan-out
- duplicate subscriptions are allowed

## Reference implementation notes

The following notes come from a public reference implementation. They are useful implementation guidance, but they are not automatically normative for this repository unless they match the OMLOX PDF or we explicitly adopt them.

Sources:
- Public WebSocket API reference
- Public security and authorization reference
- Public connector reference

### Confirmed useful details

- The reference implementation documents the same topic set and core wrapper shape as the OMLOX PDF.
- The reference implementation shows concrete examples of publishing inbound `location_updates` and `proximity_updates` over WebSocket.
- A public connector example confirms a practical message shape for raw location publication to `location_updates`.

### Product-specific behavior worth knowing

- The reference implementation documents additional WebSocket topics for object change events:
  - `provider_changes`
  - `trackable_changes`
  - `fence_changes`
  - `zone_changes`
  - `anchor_changes`
- The reference implementation adds extra error codes beyond the OMLOX PDF:
  - `10006`: not authenticated
  - `10007`: invalid license
- The reference implementation rejects duplicate subscriptions when the same topic is subscribed with identical parameters twice.
- The reference implementation accepts some topic filters as either scalar or array values.
- The reference implementation documents additional alias filter names such as:
  - `trackable_id` as an alias for `id`
  - `collision_id_1` / `collision_id_2` as aliases around the OMLOX collision object filters
- The reference implementation documents a stronger ownership model for authorization, where topic access may depend on resource ownership claims in the JWT.

### Guidance for this repository

- Treat the OMLOX PDF as normative for required behavior.
- Treat the product-specific additions above as optional extensions unless we explicitly adopt them.
- If we adopt any product-specific extension, document it separately from the OMLOX core behavior so the distinction stays clear.
- `metadata_changes` is one such adopted extension in this repository. It is not part of OMLOX core WebSocket behavior and must be treated as an implementation-specific companion topic.
