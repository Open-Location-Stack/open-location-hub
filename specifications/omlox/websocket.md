# OMLOX V2 WebSocket API

## Endpoint

- `GET /v2/ws/socket` (WebSocket upgrade)

Spec references:
- Chapter 6.6
- Chapter 13 (WebSocket Publish / Subscribe API)

## Message envelope

Core fields:
- `event`: `message` | `subscribe` | `subscribed` | `unsubscribe` | `unsubscribed` | `error`
- `topic`
- `subscription_id`
- `payload` (array)
- `params` (object)

## Available topics

Mandatory topics from chapter 13.6:
- `location_updates`
- `location_updates:geojson`
- `collision_events`
- `fence_events`
- `fence_events:geojson`
- `trackable_motions`
- `proximity_updates`

## Send/receive rules

- `location_updates` accepts client submissions and emits processed updates.
- `proximity_updates` accepts client submissions.
- `collision_events`, `fence_events`, `trackable_motions` are hub-generated output; client submit is forbidden.
- Topic-specific filtering parameters are defined in chapter 13.6.

## Auth

When enabled, token is passed in `params.token` (JWT/OpenID flow).
