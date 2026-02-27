# OMLOX V2 RPC Extension (Optional)

Spec references:
- Chapter 15

## REST endpoints

- `GET /v2/rpc/available`
- `PUT /v2/rpc`

Behavior:
- Hub bridges REST RPC calls to MQTT RPC topics.
- For calls expecting result: HTTP `200` with JSON-RPC result/error payload.
- For notification-style calls (no `id` / `_caller_id`): HTTP `204`.

## MQTT RPC topics

Method availability:
- `/omlox/{format}/rpc/available/{method_name}`

Requests:
- `/omlox/{format}/rpc/{method_name}/request`
- `/omlox/{format}/rpc/{method_name}/request/{handler_id}`

Responses:
- `/omlox/{format}/rpc/{method_name}/response/{_caller_id}`

For v2 format is `jsonrpc`.

## Hub-specific RPC params

- `_timeout`
- `_aggregation` (`_all_within_timeout`, `_return_first_success`, `_return_first_error`)
- `_handler_id`
- `_caller_id`

## Mandatory methods

- `com.omlox.ping`
- `com.omlox.identify`
- `com.omlox.core.xcmd`
