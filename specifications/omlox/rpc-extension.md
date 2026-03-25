# OMLOX V2 RPC Extension (Optional)

Spec references:
- Chapter 15

## Purpose in this repository

RPC is the hub's control-plane surface.

Use it when a client needs to ask the hub or a downstream RTLS component to do
something immediately, for example:
- check liveness
- identify a reachable handler
- send a targeted OMLOX core command

Use normal REST resources for durable configuration and MQTT/WebSocket streams
for event fan-out. Use RPC for command and diagnostic style interactions.

## REST endpoints

- `GET /v2/rpc/available`
- `PUT /v2/rpc`

Behavior:
- the HTTP client calls the hub, not the device directly
- the hub may handle the method locally, forward it to MQTT, or do both
- for calls expecting a result: HTTP `200` with a JSON-RPC result or JSON-RPC error payload
- for notification-style calls with no `id` and no `_caller_id`: HTTP `204`

## Dispatch model

The hub maintains a single method registry containing:
- hub-owned methods implemented locally
- externally announced MQTT handlers discovered from retained availability topics

Dispatch rules in the current implementation:
- if `_handler_id` targets the hub handler id, dispatch locally
- if `_handler_id` targets an external handler id, dispatch only to that MQTT handler
- without `_handler_id`, the hub dispatches to all matching local and external handlers
- response aggregation then decides what the REST caller receives

## MQTT RPC topics

Method availability:
- `/omlox/{format}/rpc/available/{method_name}`

Requests:
- `/omlox/{format}/rpc/{method_name}/request`
- `/omlox/{format}/rpc/{method_name}/request/{handler_id}`

Responses:
- `/omlox/{format}/rpc/{method_name}/response/{_caller_id}`

XCMD broadcast:
- `/omlox/jsonrpc/rpc/com.omlox.core.xcmd/broadcast`

For v2 the format is `jsonrpc`.

## Hub-specific RPC params

- `_timeout`
- `_aggregation`
  - `_all_within_timeout`
  - `_return_first_success`
  - `_return_first_error`
- `_handler_id`
- `_caller_id`

Validation rules enforced by the hub:
- unknown `_aggregation` yields JSON-RPC error `-32602`
- `_handler_id` together with `_aggregation` yields JSON-RPC error `-32602`
- unknown handler id yields JSON-RPC error `-32000`
- timeout yields JSON-RPC error `-32001`
- no non-error response yields JSON-RPC error `-32002`

## Mandatory methods

The OMLOX-reserved methods are:
- `com.omlox.ping`
- `com.omlox.identify`
- `com.omlox.core.xcmd`

Current implementation status:
- `com.omlox.ping`: implemented locally by the hub
- `com.omlox.identify`: implemented locally by the hub
- `com.omlox.core.xcmd`: implemented locally at the RPC layer and routed through an adapter seam; deployments without an adapter receive a deterministic unsupported error

## Method behavior

### `com.omlox.ping`

Purpose:
- prove that the control-plane path is reachable

Current result shape includes:
- handler id
- message `pong`
- method name
- current timestamp

### `com.omlox.identify`

Purpose:
- tell operators and clients what the hub exposes

Current result shape includes:
- handler id
- service name
- build version
- auth mode
- built-in method list
- flags indicating local-handler and MQTT-bridge support

### `com.omlox.core.xcmd`

Purpose:
- provide a stable hub entrypoint for OMLOX core-zone commands

Current repository behavior:
- validates and authorizes the call at the hub RPC layer
- forwards normalized parameters to a deployment-specific adapter
- publishes any returned `XCMD_BC` payloads to the broadcast topic
- returns a deterministic unsupported error when no adapter is configured

This means the hub-side control-plane contract exists today, but actual device
command execution still depends on the adapter implementation used in a given
deployment.

## Security model

Do not give every user-facing client direct MQTT access to devices. Let clients
call the hub over HTTP, and let the hub enforce:
- bearer-token authentication
- per-method authorization
- audit logging
- handler selection rules

Operational guidance:
- treat `GET /v2/rpc/available` as sensitive because it reveals reachable control functions
- grant invoke permissions per method, not just “RPC access”
- restrict `com.omlox.core.xcmd` more tightly than `com.omlox.ping` or `com.omlox.identify`
- keep MQTT broker access narrow to the hub and trusted device/adaptor components

## Current limitations

- the available-method REST response currently exposes handler ids but not local/external source metadata
- retained method announcements are published, but MQTT v5 message-expiry enforcement is not yet wired through the current client abstraction
- `com.omlox.core.xcmd` needs a real adapter before it can control concrete provider/core implementations
