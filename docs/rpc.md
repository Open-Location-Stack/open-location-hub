# RPC Guide

## What RPC is

RPC is the hub's command and diagnostics interface.

Use it when you need to ask the hub or a downstream RTLS device/controller to
do something right now. Examples:
- check whether the control path is alive
- identify a reachable handler
- send an OMLOX core command through the hub

Do not use RPC for normal CRUD resource management. Use:
- REST CRUD for zones, trackables, providers, and fences
- MQTT/WebSocket streams for ongoing updates and events
- RPC for targeted commands and diagnostics

## The simple mental model

Applications call `open-rtls-hub`. They do not call MQTT devices directly.

Flow:
1. The client calls `GET /v2/rpc/available` to see what methods are reachable.
2. The client calls `PUT /v2/rpc` with a JSON-RPC request.
3. The hub either handles the method locally or forwards it to the right MQTT handler.
4. The hub applies authorization, logging, timeout, and aggregation rules.
5. The hub returns the JSON-RPC result or error to the client.

This makes the hub the control-plane front door and audit point.

## Available methods

The hub currently exposes these OMLOX-reserved methods locally:
- `com.omlox.ping`
- `com.omlox.identify`
- `com.omlox.core.xcmd`

The hub may also expose additional methods announced by external MQTT handlers.

Use:

```bash
curl -sS http://localhost:8080/v2/rpc/available \
  -H "Authorization: Bearer $TOKEN"
```

The response maps method names to currently reachable handler ids.

## Calling methods

Call `PUT /v2/rpc` with a JSON-RPC 2.0 request body.

Example: ping the hub

```bash
curl -sS -X PUT http://localhost:8080/v2/rpc \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": "ping-1",
    "method": "com.omlox.ping",
    "params": {
      "_aggregation": "_return_first_success"
    }
  }'
```

Example: identify a specific handler

```bash
curl -sS -X PUT http://localhost:8080/v2/rpc \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": "identify-1",
    "method": "com.omlox.identify",
    "params": {
      "_handler_id": "open-rtls-hub"
    }
  }'
```

Example: send an OMLOX core command through the hub

```bash
curl -sS -X PUT http://localhost:8080/v2/rpc \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": "xcmd-1",
    "method": "com.omlox.core.xcmd",
    "params": {
      "_handler_id": "open-rtls-hub",
      "command": "XCMD_REQ",
      "payload": {
        "example": true
      }
    }
  }'
```

Note:
- `com.omlox.core.xcmd` already exists at the hub control-plane layer
- actual device execution still depends on the configured adapter for the target deployment
- if no adapter is configured, the hub returns a deterministic unsupported JSON-RPC error

## What the built-in methods mean

### `com.omlox.ping`

Use it to prove the RPC path is alive. It returns a small result containing the
hub handler id, the message `pong`, and a timestamp.

### `com.omlox.identify`

Use it to learn what the hub exposes. It returns the service name, build
version, auth mode, stable `hub_id`, and built-in method list.

Current hub behavior:
- `name` is the persisted hub label from Postgres-backed hub metadata
- `hub_id` is the stable persisted hub UUID used for internal provenance and future federation identity

### `com.omlox.core.xcmd`

Use it when you need to send OMLOX core-zone commands through the hub instead
of opening direct MQTT control access to devices. The hub validates and logs
the call, then routes it through an adapter if one is configured.

## `_handler_id`, `_timeout`, and `_aggregation`

### `_handler_id`

Use `_handler_id` when one specific handler must receive the command.

Examples:
- target the hub itself with `open-rtls-hub`
- target a specific external handler discovered from `GET /v2/rpc/available`

### `_timeout`

Use `_timeout` to override the default wait time in milliseconds for a specific
request.

### `_aggregation`

Use `_aggregation` only when the call may have multiple responders.

Supported values:
- `_all_within_timeout`
- `_return_first_success`
- `_return_first_error`

Rules:
- `_handler_id` and `_aggregation` cannot be combined
- if `_aggregation` is omitted, the hub defaults to `_all_within_timeout`

## What happens when multiple handlers answer

The hub collects responses according to `_aggregation`.

- `_all_within_timeout`: return one result whose `responses` array contains all responses received before timeout
- `_return_first_success`: return as soon as a non-error response arrives
- `_return_first_error`: return as soon as an error response arrives

If no suitable response arrives, the hub returns a JSON-RPC error.

## Authorization

RPC uses two layers of control:

1. normal REST bearer-token authentication and route authorization
2. RPC-specific method authorization inside the hub

That means a caller can be allowed to reach `/v2/rpc` and still be denied for a
specific method.

Recommended policy shape:
- allow discovery only to trusted operators or automation
- allow `com.omlox.ping` and `com.omlox.identify` to a small support audience if needed
- allow `com.omlox.core.xcmd` only to tightly controlled roles
- deny unknown/custom methods by default until explicitly approved

## How to secure it

Plain language rule:

Do not let every application talk directly to MQTT devices. Let them talk to
the hub. The hub checks identity, checks permissions, logs what was called, and
only then forwards the command.

Operational guidance:
- require JWT auth for RPC in production
- treat `GET /v2/rpc/available` as sensitive because it shows reachable control functions
- grant invoke permission per method, not just generic RPC access
- restrict `com.omlox.core.xcmd` more tightly than `ping` or `identify`
- keep MQTT broker access narrow to the hub and trusted adapters/devices
- use the hub as the audit and policy boundary
- fail closed when authorization or handler selection is uncertain

## Logging and audit expectations

The hub logs:
- the method name
- whether the call was accepted or rejected
- handler selection and timeout/failure paths

Operators should treat those logs as the primary audit trail for RPC use until
metrics and richer observability are added.

## Current limitations

- the available-method response exposes handler ids but not local/external source metadata
- retained MQTT announcements are implemented, but strict MQTT v5 expiry behavior is still a documented gap
- `com.omlox.core.xcmd` still needs a deployment-specific adapter before it can drive real provider/core integrations
