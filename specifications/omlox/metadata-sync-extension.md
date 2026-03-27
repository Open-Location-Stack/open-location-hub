# Metadata Sync WebSocket Extension

This document describes a repository-specific WebSocket extension for metadata
change notifications.

This is **not** part of the OMLOX Hub v2 specification.

Use this extension when deployments need a lightweight change stream for hub
metadata that is controlled outside the local hub instance.

## Purpose

The extension exists to support federated and centrally managed deployments
where:

- a cloud or central hub is the write authority for shared metadata
- edge or on-prem hubs keep a local in-memory metadata snapshot for low-latency
  location, geofence, and collision processing
- downstream peers need a cheap signal that metadata changed and local caches or
  reconciliation loops should react

The extension is intentionally small. It is a change notification stream, not a
replacement for snapshot or CRUD APIs.

Typical use cases:

- trigger on-prem hubs to reconcile centrally managed zones, fences, trackables,
  or providers
- notify local subscribers that geofence or georeferencing inputs changed
- support cloud-authoritative metadata rollouts without putting Postgres reads
  on the hot ingest path

## Topic

WebSocket topic:
- `metadata_changes`

Transport:
- OMLOX wrapper protocol over `GET /v2/ws/socket`

Client publish allowed:
- no

Subscription filters:
- `id`: optional resource identifier
- `type`: optional resource type
- `operation`: optional change kind

Supported `type` values:
- `zone`
- `fence`
- `trackable`
- `location_provider`

Supported `operation` values:
- `create`
- `update`
- `delete`

## Payload

Outbound payload:
- array of `MetadataChange`

Object shape:

```json
{
  "id": "b7d0b3c6-4b86-7a5f-9d10-f2d1fe74ac47",
  "type": "zone",
  "operation": "update",
  "timestamp": "2026-03-27T12:00:00Z"
}
```

Field meanings:
- `id`: stable identifier of the changed resource
- `type`: metadata class affected by the change
- `operation`: `create`, `update`, or `delete`
- `timestamp`: hub event creation time in RFC3339 form

## Hub Behavior

The hub emits `metadata_changes` when:

- a zone, fence, trackable, or location provider CRUD write succeeds
- the background metadata reconcile loop detects out-of-band drift compared with
  the current in-memory metadata snapshot

The topic intentionally carries only enough information to identify what
changed. Consumers that need the full object must use the normal REST resources
or a snapshot sync flow.

## Federated Deployment Guidance

For centrally controlled metadata:

- the central hub should remain the write authority for shared metadata
- edge hubs should treat `metadata_changes` as an invalidation or reconcile
  signal, not as an authoritative full-state payload
- receivers should keep a local last-known snapshot so ingest and derived event
  generation can continue during central-link outages
- receivers should combine this change stream with periodic full reconciliation
  because a subscriber may miss notifications during disconnects or restarts

Recommended receiver flow:

1. Load or fetch an initial metadata snapshot.
2. Subscribe to `metadata_changes`.
3. On each notification, reconcile the affected resource or resource class.
4. Run periodic full reconciliation to repair gaps.

This keeps the critical path local and in-memory while still allowing
cloud-authoritative metadata management across a fleet.

## Boundary With OMLOX Core

- OMLOX defines the core WebSocket publish/subscribe surface for ingest and
  event topics.
- `metadata_changes` is a repository extension layered onto the same wrapper
  protocol.
- Implementations that need strict OMLOX-only behavior may ignore this topic.
