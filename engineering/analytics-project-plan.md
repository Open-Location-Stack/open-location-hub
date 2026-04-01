# Analytics Project Draft Plan

This document outlines a separate downstream project that consumes Open RTLS Hub event streams and exposes historical and exploratory analytics in Grafana without turning the hub itself into an analytics-heavy product.

## Recommendation

Build analytics as a separate project on top of the hub's existing real-time APIs.

Why this is the recommended split:

- the hub should stay focused on OMLOX interoperability, normalization, event generation, auth, and operational health
- analytics workloads have different storage, retention, indexing, and query requirements
- high-cardinality slicing by `trackable_id` or business asset identifiers belongs in an event store, not in Prometheus metrics
- Grafana dashboards, retention policy, and query tuning should be able to evolve independently from hub releases

## Are the current APIs sufficient?

For a first analytics project against a single hub deployment, yes.

The hub already exposes the core event families needed to build a downstream analytics pipeline:

- WebSocket `location_updates`
- WebSocket `location_updates:geojson`
- WebSocket `proximity_updates`
- WebSocket `fence_events`
- WebSocket `fence_events:geojson`
- WebSocket `trackable_motions`
- WebSocket `collision_events` when collisions are enabled
- WebSocket `metadata_changes` for lightweight metadata invalidation
- MQTT equivalents for local deployments that prefer broker-based ingestion

Recommended transport choice:

- prefer WebSocket for the analytics project's first implementation because it is the cleaner cross-environment API and aligns with the intended long-term architecture
- treat MQTT as an optional local-ingest adapter for on-prem or edge-heavy deployments

## Current hub gaps that do not block an MVP

The following are useful follow-ups but should not block a first analytics project:

- stable configured `hub_id` for provenance across multiple hubs; tracked by `#17`
- stronger federation metadata once analytics spans more than one hub; tracked mainly by `#21` and the federation parent `#24`
- clearer documented event delivery and reconnect semantics for downstream consumers; tracked by `#26`
- richer metadata snapshot/sync support if the analytics service needs a full local metadata cache; tracked by `#19` and `#20`

For a single-hub MVP, the current WebSocket surface is enough to begin.

## Proposed Analytics Architecture

Components:

1. Stream consumer
   - subscribes to hub WebSocket topics
   - handles reconnect and replay strategy at the application level
   - optionally supports MQTT as an alternative source
2. Event normalizer
   - maps OMLOX payloads into an internal analytics schema
   - enriches records with ingestion time, source transport, and deployment metadata
3. Metadata cache
   - loads zones, fences, trackables, and providers from the hub REST API
   - listens to `metadata_changes` to invalidate or refresh local cache entries
4. Analytics store
   - persists append-only event records optimized for time-range and asset-centric queries
5. Grafana layer
   - connects directly to the analytics store
   - exposes dashboards for movement, fences, collisions, and fleet views

## Storage Recommendation

Start with PostgreSQL.

Why:

- it matches the team's current stack and deployment model
- Grafana has solid PostgreSQL support
- the first version can stay simple and operationally familiar
- schema evolution and joins against metadata are straightforward

If event volume later becomes much larger, reassess:

- TimescaleDB for time-series retention and hypertables
- ClickHouse for larger event volumes and exploratory analytics

## Suggested Data Model

Keep raw payloads for fidelity, but project key fields into queryable columns.

Tables or views should support at least:

- `event_id`
- `event_type`
- `event_time`
- `ingested_at`
- `trackable_id`
- `provider_id`
- `zone_id`
- `fence_id`
- `collision_other_trackable_id` where relevant
- `hub_id` once available
- `tenant_id` or site scope when present
- `latitude`
- `longitude`
- `geometry` or GeoJSON payload where useful
- raw payload JSON

Recommended event tables:

- `location_events`
- `proximity_events`
- `fence_events`
- `trackable_motion_events`
- `collision_events`
- `metadata_snapshots` or cache tables for enrichment

## Grafana Use Cases

The separate project should make these queries easy:

- asset timeline by `trackable_id`
- latest known position by asset
- historical path replay for one asset or a selected fleet
- fence entry and exit timeline
- collision timeline and collision pair analysis
- dwell time by zone or fence
- provider-specific quality or source comparison
- site-wide heatmaps or traffic summaries

## Ingestion Strategy

### Phase 1: single-hub WebSocket consumer

Implement one consumer process that:

- authenticates against the hub
- subscribes to the relevant WebSocket topics
- persists every event into the analytics store
- periodically refreshes metadata via REST
- uses `metadata_changes` to invalidate or refresh cached metadata

This phase should intentionally avoid trying to solve federation, replay guarantees, or global identity beyond one hub.

### Phase 2: operational hardening

Add:

- reconnect handling and durable consumer state
- idempotent writes where practical
- lag visibility and ingestion failure diagnostics
- retention and partitioning

### Phase 3: multi-hub and provenance-aware analytics

Once the hub federation work matures, extend the analytics project to:

- ingest from multiple hubs
- preserve hub provenance
- support cross-hub queries safely
- respect tenant, site, and regional boundaries

## Authentication and Access Model

The analytics project should be treated as a machine consumer of hub APIs.

Expectations:

- use the hub's existing auth model for WebSocket and REST access
- give the analytics service a dedicated principal with narrowly scoped subscribe/read permissions
- do not bypass the hub by giving Grafana direct MQTT broker access unless the deployment explicitly needs that

## Delivery Plan

1. Build the consumer around WebSocket topics and PostgreSQL persistence.
2. Add a metadata sync/cache layer using REST plus `metadata_changes`.
3. Define Grafana-ready SQL views for the first dashboards.
4. Add operational hardening around reconnect, lag, and retention.
5. Revisit multi-hub analytics after federation foundations exist in the hub.

## What might be added to the hub later?

These are optional improvements, not MVP blockers:

- clearer downstream consumer guidance in docs for reconnect and delivery behavior via `#26`
- stable `hub_id` for provenance-aware analytics via `#17`
- explicit event versioning guidance if payload evolution becomes frequent
- richer metadata snapshot/change-feed support for analytics-side enrichment via `#19` and `#20`

## Conclusion

The current hub APIs are sufficient to start building a separate analytics project now.

The right boundary is:

- hub: real-time OMLOX events and operational metrics
- analytics project: durable event ingestion, enrichment, historical querying, and Grafana dashboards
