# OMLOX V2 MQTT Extension (Optional)

Spec references:
- Chapter 14

## Topic hierarchy

General form:
- `/omlox/{format}/{topic_root}/{sub_path_1}/.../{sub_path_n}`

`format` for v2: `json` (and `jsonrpc` for RPC extension topics).

## Location updates

- Publish ingest: `/omlox/json/location_updates/pub/{provider_id}`
- Processed local: `/omlox/json/location_updates/local/{provider_id}`
- Processed WGS84: `/omlox/json/location_updates/epsg4326/{provider_id}`

## Proximity updates

- Ingest: `/omlox/json/proximity_updates/{source}/{provider_id}`
- Wildcard subscription example for all proximities: `/omlox/json/proximity_updates/#`

## Fence events

- `/omlox/json/fence_events/{fence_id}`
- `/omlox/json/fence_events/trackables/{trackable_id}`
- `/omlox/json/fence_events/providers/{provider_id}`

## Trackable motions

- `/omlox/json/trackable_motions/local/{trackable_id}`
- `/omlox/json/trackable_motions/epsg4326/{trackable_id}`

## Collision events

- `/omlox/json/collision_events/epsg4326`
