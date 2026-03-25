# Hub Federation Plan

This document outlines a holistic plan for federating OMLOX hubs across on-premises, regional cloud, and aggregate cloud deployments while preserving standard OMLOX interoperability as the primary contract.

The intent is to support deployments such as:

- on-premises plant hubs that expose the standard OMLOX APIs locally
- regional cloud hubs such as US and EU that receive data from one or more on-premises hubs
- aggregate hubs that consolidate data from regional hubs for cross-region visibility, analytics, and control-plane routing

## Goals

- Keep standard OMLOX APIs as the primary interoperability boundary between hubs.
- Allow both push and pull federation patterns so constrained sites can choose what fits their network and security posture.
- Preserve tenant, site, and ownership boundaries as data moves across hub tiers.
- Avoid forcing vendor-specific connector behavior into the core federation contract.
- Make federation operationally safe: replay-aware, auditable, backpressure-aware, and resilient to partial outages.

## Non-Goals

- Do not require proprietary federation-only payloads for the first interoperable version.
- Do not assume a single global trust domain or identity provider.
- Do not require direct MQTT connectivity between every pair of hubs.
- Do not use MQTT as a transport between hubs; cross-hub federation should use REST and WebSocket only.

## Target Topologies

### Topology A: On-prem push to regional cloud

- The on-prem hub remains the source of truth for local devices, zones, fences, and ingest normalization.
- The regional cloud hub receives normalized OMLOX resources and events from the on-prem hub.
- This topology fits plants that can open outbound connections but do not allow inbound cloud access.

### Topology B: Regional cloud pull from on-prem

- The cloud hub reads from the on-prem hub over the standard APIs.
- This topology fits environments where the on-prem hub is reachable through controlled ingress, VPN, or private connectivity.
- Pull mode is also useful for staged migrations and selective replication.

### Topology C: Regional aggregation to global aggregation

- Regional hubs normalize, authorize, and optionally redact data for their jurisdiction.
- An aggregate hub pulls or receives forwarded data from regional hubs.
- Cross-region aggregation must support data minimization, tenancy partitioning, and jurisdiction-aware routing.

### Topology D: Mesh with strict upstream/downstream roles

- A hub may be downstream for one source and upstream for another.
- Federation roles must be explicit per connection so loops, duplicate propagation, and policy ambiguity are avoided.

## Federation Principles

### 1. Standard APIs first

Federation should use the standard OMLOX surfaces as far as possible:

- REST resources for zones, providers, trackables, and fences
- REST location and proximity ingest
- WebSocket for near-real-time event and ingest compatibility
- MQTT only for local hub, device, and on-prem integration paths, not for cross-hub federation

If a federated capability cannot be expressed cleanly via the standard OMLOX APIs, add a clearly documented extension without weakening the standard path.

### 1a. Stable hub identity first

Each hub must have a globally unique, stable `hub_id` represented as a UUID and assigned through configuration or bootstrap provisioning.

Requirements:

- `hub_id` must persist across restarts, upgrades, and routine redeployments
- `hub_id` must not be regenerated automatically during normal runtime
- `hub_id` must be attached to federated provenance metadata for resources, events, and routed RPC
- `hub_id` is the root namespace used to make cross-hub identities globally unique

This is a foundational federation requirement, not an optional convenience.

### 2. Normalize at the edge

The first hub that receives raw device or vendor-specific traffic should:

- authenticate the sender
- validate and normalize payloads
- resolve local coordinates and proximity semantics
- attach site and tenancy metadata
- expose a clean OMLOX-facing surface upstream

This keeps upper-tier hubs focused on federation, policy, and aggregation rather than vendor adapter logic.

### 2a. Keep MQTT local

MQTT remains useful in the product, but only as a local integration transport:

- local device and adapter ingest
- local event distribution inside a site or plant
- local RPC bridging where explicitly enabled

It should not be part of the hub-to-hub federation contract. Federation traffic between on-prem, regional cloud, and aggregate cloud hubs should use:

- REST for resource CRUD, snapshot sync, reconciliation, and controlled ingest
- WebSocket for near-real-time streaming and subscription-based federation

### 3. Preserve provenance

Every replicated resource and event needs durable provenance metadata, even if carried in extension properties rather than new top-level OMLOX fields. At minimum:

- origin hub identifier
- origin hub identifier
- origin region or tenant
- source provider identifier
- federation path or hop count
- original event timestamp and ingestion timestamp
- replication mode: push, pull, replay, or reconciliation

### 4. Make replay and deduplication explicit

Federation guarantees should not depend on “best effort plus hope”. Hubs need explicit handling for:

- duplicate events across reconnects
- replay windows after outage recovery
- late-arriving updates
- at-least-once delivery between tiers
- loop suppression when hubs can both push and pull

## Identity and Namespace Strategy

Federation must assume that some identifiers are only locally unique. The minimum global uniqueness rule is:

- every federated identity is scoped by `origin_hub_id`

### Identity layers

The design should distinguish between:

- hub identity: the stable configured UUID of a hub
- canonical resource identity: the UUID managed by a hub for resources such as trackables
- external hardware identity: vendor or network-local identifiers such as tag IDs, EPCs, beacon IDs, or MAC-like IDs
- logical business identity: the higher-level asset/person/vehicle identity that may span multiple hardware identities over time

### Trackable identity

In this implementation, `trackable_id` should remain a UUID managed by the hub. That gives each hub a canonical local identity for trackables.

For federation:

- `(origin_hub_id, trackable_id)` is globally unique
- raw external tag IDs must not be treated as globally unique identifiers
- external tag IDs should be stored as attributes or associated mappings, not as the federated primary key

### Resource identity rule

For any replicated resource or event, the receiver should preserve:

- `origin_hub_id`
- the origin resource identifier
- any locally assigned storage identifier if translation is required

Global deduplication, replay handling, and routing should use scoped identities rooted in `origin_hub_id`, not raw local IDs.

### Configuration implications

The runtime should introduce a required configured hub UUID, for example `HUB_ID`, validated as a UUID at startup.

That identity should be used for:

- provenance on replicated resources and events
- loop suppression
- deduplication keys
- peer sync cursors
- RPC routing and audit chains

### Future merge and alias work

Using `origin_hub_id` plus local IDs solves uniqueness, but it does not automatically solve business-level identity unification.

Future work may still need:

- alias tables mapping multiple external hardware identities to one trackable
- merge logic for business assets observed across multiple hubs
- explicit lookup APIs that resolve user-facing identifiers to scoped federated identities

## Functional Scope for a Federated Hub

### A0. Cloud-authoritative metadata distribution

For many real deployments, configuration metadata should be managed centrally in the cloud and distributed to on-prem hubs. This applies especially to:

- building and site metadata
- zones and zone georeferencing inputs
- fences and geofencing policy metadata
- trackable definitions and locating preferences
- provider registrations and provider policy metadata

Recommended default:

- cloud hubs are authoritative for shared metadata
- on-prem hubs maintain a synchronized local cache
- on-prem hubs use the cached metadata for local ingest, local event generation, and degraded operation during cloud outages

This should be treated as a first-class federation capability, separate from live event replication.

### A. Feature-complete OMLOX ingestion and event surface

Before federation can be considered feature complete, the hub should expose the full minimum OMLOX-facing surface expected by other hubs and partners:

- REST ingest for `Location` and `Proximity`
- mandatory WebSocket endpoint and topics
- MQTT extension behavior when MQTT is enabled for local use
- provider, zone, trackable, and fence resource management
- generated fence events and trackable motions
- collision-event support or a clearly documented non-support mode if the product intentionally excludes it

### B. Federation connection management

The hub needs explicit upstream/downstream configuration for each federated peer:

- peer hub identifier
- peer REST and WebSocket endpoints
- push versus pull mode
- resource scopes and topic scopes
- tenancy and site mapping rules
- auth mode and trust anchors
- replay cursor and health state

### C. Replication behavior

The hub should support:

- initial snapshot sync for resources
- incremental sync for resource changes
- near-real-time forwarding for locations, proximities, fence events, trackable motions, and eventually collisions
- deterministic ordering guarantees where feasible
- idempotent application on the receiving hub

### D. Query and fan-out behavior

Cloud hubs should be able to:

- aggregate multiple downstream hubs into a single standard OMLOX-facing API
- optionally expose filtered or partitioned views per tenant or region
- route control-plane operations only to the relevant downstream hubs

## Cloud-Authoritative Metadata Sync Design

This section defines the recommended model for synchronizing buildings, geofences, and related metadata from cloud to on-prem hubs.

### Scope

Metadata sync should cover:

- buildings, sites, campuses, or other facility descriptors represented by the hub's managed resource model and extensions
- zones, including georeferencing inputs such as `ground_control_points`
- fences, including geometry and timeout or tolerance-related policy
- trackables where centrally managed locating policy is required
- providers where centrally managed provider registration or policy is required

If the core OMLOX model lacks a first-class building resource, building-level metadata should be carried through a documented extension model until a more formal resource is introduced.

### Source-of-truth model

For cloud-authoritative metadata:

- the cloud hub is the only write authority for shared metadata
- on-prem hubs receive replicated copies and should treat them as read-only
- local-only metadata is allowed, but it must be marked explicitly as local and non-replicated
- any on-prem override mechanism must be explicit, auditable, and narrow in scope

Recommended default policy:

- fences, zones, and facility metadata are cloud-owned
- transient ingest state and local runtime caches are on-prem-owned
- provider registrations may be cloud-owned or site-owned, but the choice must be explicit per deployment

### Distribution patterns

Two distribution patterns should be supported.

#### Pattern 1: Cloud push

- Cloud detects metadata changes and pushes them to selected on-prem peers.
- Best for centrally managed fleets where cloud can reach the site or where a site agent maintains an outbound session.

#### Pattern 2: On-prem pull

- On-prem hub periodically reconciles metadata from cloud and subscribes to a change stream where available.
- Best for sites that allow only outbound access and want local control of sync timing.

Both patterns should share the same replication model:

- full snapshot for initial bootstrap
- incremental changes after bootstrap
- version-aware reconciliation when gaps or outages occur

### Sync mechanics

Each replicated metadata class should support:

- a stable resource identifier
- a resource version or update sequence
- `created_at`, `updated_at`, and replicated-at timestamps
- provenance fields such as origin hub and origin region
- deletion markers or tombstones

Minimum receiver behavior:

1. Fetch or receive a full snapshot for the allowed scope.
2. Persist the last applied version per resource class and peer.
3. Apply incremental upserts idempotently.
4. Apply deletions through tombstones rather than assuming absence means delete.
5. Run periodic reconciliation to repair missed updates.

### Conflict handling

For cloud-authoritative resources, conflict handling should be simple:

- cloud wins for replicated resources
- on-prem writes to cloud-owned resources are rejected by default
- if a local override is permitted, the override must be stored separately from the replicated base definition and surfaced clearly in diagnostics

Avoid hidden last-write-wins behavior across trust domains.

### Offline behavior

On-prem hubs must remain operational when cloud sync is unavailable.

Required behavior:

- retain the last successfully synchronized metadata snapshot locally
- continue ingest, geofencing, and publication using the cached snapshot
- expose sync staleness in health and metrics
- refuse destructive resync behavior when the cloud is temporarily unreachable

### Site and tenant scoping

Metadata sync must be scope-aware so a hub only receives what it should operate on.

Scoping dimensions may include:

- site or facility
- tenant
- region
- environment such as production or staging

The sync contract should support peer-specific filters so a single cloud hub can distribute different subsets to different on-prem hubs.

### Security model

Metadata sync should use dedicated machine identities and narrow scopes.

Examples:

- `federation.metadata.read`
- `federation.metadata.write`
- `federation.metadata.subscribe`
- `federation.metadata.reconcile`

Audit records should capture:

- authenticated peer identity
- replicated resource type and identifier
- source version
- resulting action: create, update, delete, reconcile, reject

### Recommended extension surface

Standard OMLOX CRUD resources are the preferred sync substrate, but practical operation will likely require a documented extension for metadata change feeds.

Recommended additions:

- resource change feed topics or endpoints for zones, fences, providers, and trackables
- versioned snapshot endpoints or list filters sufficient for reconciliation
- optional future building or site resource if the product needs first-class facility hierarchy

### Delivery plan for metadata sync

#### Stage 1: Central ownership and local cache

- define which metadata classes are cloud-owned versus local-owned
- add local persistence for synchronized metadata version state
- prevent accidental local writes to replicated resources

#### Stage 2: Snapshot synchronization

- add peer-scoped snapshot export and import for zones, fences, providers, and trackables
- add version tracking and provenance fields
- add tombstone handling for deletions

#### Stage 3: Incremental sync and reconciliation

- add change feeds or equivalent incremental polling support
- add periodic reconciliation jobs
- add health and lag metrics for metadata sync

#### Stage 4: Facility hierarchy and rollout tooling

- introduce explicit building or site modeling if needed
- add staged rollout support by site, tenant, or region
- add audit and approval hooks for sensitive geofence or facility changes

## Push and Pull Patterns

### Push federation

Recommended when on-prem may only open outbound connections.

Flow:

1. On-prem hub authenticates to the cloud hub using a service identity.
2. On-prem hub pushes normalized OMLOX resources and ingest/event traffic upstream.
3. Cloud hub validates provenance, applies policy, and stores or republishes the data.
4. Retries use idempotency keys or deterministic event identities.

Required capabilities:

- outbound-friendly auth
- retry-safe ingest semantics
- replay cursor support
- per-peer rate limiting and dead-letter strategy
- REST and WebSocket session management without MQTT broker dependencies

### Pull federation

Recommended when cloud can securely reach on-prem and central operators want selective collection.

Flow:

1. Cloud hub authenticates to the on-prem hub with read and subscribe permissions.
2. Cloud hub reads resources and subscribes to standard streams.
3. Cloud hub reconciles snapshots with streaming updates.
4. Cloud hub records sync position and gap recovery state.

Required capabilities:

- resource listing and change detection strategy
- WebSocket subscription support for standard topics
- resumable pull cursors or periodic reconciliation
- per-peer ownership and scope enforcement

### Mixed mode

Some deployments will need push for events and pull for configuration or reconciliation. The model should permit that, but each peer relationship must have one clearly defined source of truth per data class.

## Authentication and Authorization Model

Federation introduces a second auth problem beyond end-user auth: hub-to-hub trust.

### Service identities

Each federated hub should have one or more dedicated machine identities. These should not reuse human roles.

Preferred patterns:

- OIDC client credentials or workload identity for cloud-to-cloud or cloud-to-edge calls
- static key verification only for constrained or offline deployments, with strict key rotation guidance
- `hybrid` mode where needed during migrations, but not as an excuse to leave trust boundaries ambiguous

### Audience and issuer handling

The current auth stack already validates issuer and audience. Federation needs explicit guidance for:

- per-hub audience values
- multiple trusted issuers across regions or tenants
- token exchange or gateway-issued tokens when upstream and downstream IdPs differ
- clear separation between user tokens and service tokens

### Authorization scopes

The current role and ownership model is a good base, but federation needs additional policy concepts:

- read-only federation peers
- ingest-only federation peers
- replication of specific providers, zones, sites, or tenants
- rights to invoke only a narrow RPC subset
- rights to subscribe to only a subset of WebSocket topics or MQTT topic families

### Ownership propagation

Ownership-aware auth currently works best for resource routes with explicit identifiers. Federation needs consistent mapping for:

- provider ownership
- site or facility ownership
- region and tenant scoping
- derived events whose ownership originates from upstream providers or trackables

The receiving hub should preserve upstream ownership metadata and map it into local policy decisions rather than flattening everything into a global admin domain.

### Trust-domain boundaries

Regional and aggregate hubs may not share the same issuer, operator, or legal boundary. The plan should assume:

- hub-to-hub trust is configured explicitly per peer
- claims may need normalization across issuers
- some attributes may need redaction before forwarding upstream
- audit logs must record both authenticated peer identity and propagated origin metadata

## Data Model and Identity Work Needed

To federate safely, the current payload-first model needs additional structure around identity and provenance:

- stable hub identity
- stable peer identity
- canonical origin resource keys
- mapping from upstream resource IDs to local stored IDs where translation is required
- replicated-versus-local resource markers
- tombstone or deletion propagation semantics

The design should avoid rewriting OMLOX identifiers casually. If translation is required, it should be explicit and auditable.

## Eventing Work Needed

### WebSocket completion

WebSocket is the biggest standards gap for cross-hub interoperability. A federated design should implement:

- `/v2/ws/socket`
- mandatory topics and filters
- auth via `params.token`
- subscription lifecycle and mandated error codes
- outbound publication for processed locations, fence events, trackable motions, GeoJSON variants, and collision events
- inbound publication handling for `location_updates` and `proximity_updates`

### MQTT completion

MQTT should remain a local-only integration path and should not be used for hub-to-hub federation. For MQTT-enabled deployments, complete the optional extension coherently for local use:

- full topic-family support for enabled MQTT behavior
- retained RPC announcement expiry handling
- explicit broker trust model and topic ACL guidance
- replay/reconnect semantics for local clients and site-local adapters

### Resource change feeds

Standard OMLOX does not give a full change-data-capture model for resource CRUD. For practical federation, add a documented extension for:

- `provider_changes`
- `trackable_changes`
- `fence_changes`
- `zone_changes`
- future `anchor_changes`

These feeds are especially useful for pull-based cloud hubs and admin UIs.

## Control Plane Federation

RPC federation should be treated carefully.

Recommended baseline:

- local hubs expose only hub-owned and explicitly allowed downstream methods upstream
- regional and aggregate hubs maintain a registry of reachable methods by hub, tenant, and region
- default routing is deny-by-default
- `com.omlox.core.xcmd` should require explicit adapter and policy support before multi-hop forwarding is enabled

Control-plane forwarding should only be enabled after data-plane federation and auditability are stable.

## Operations and Reliability

Feature-complete federation requires operational behaviors, not just endpoints:

- peer health and readiness reporting
- sync lag metrics
- replay queue depth metrics
- audit logs for replication actions
- alerting on auth failures, schema failures, and repeated delivery failures
- backpressure handling and bounded retry policies
- dead-letter capture for irrecoverable upstream/downstream failures

## Recommended Delivery Phases

### Phase 1: Complete the single-hub OMLOX surface

- implement mandatory WebSocket support
- close remaining MQTT extension gaps for local use
- deepen fence and collision behavior to the intended product scope
- document supported and unsupported OMLOX behaviors precisely

### Phase 2: Add federation foundations

- introduce hub identity and peer configuration model
- add provenance metadata strategy
- add replication state tracking and deduplication keys
- define service-account auth and authorization rules for peers
- define REST and WebSocket as the only supported cross-hub federation transports

### Phase 3: Resource federation

- snapshot and reconcile zones, providers, trackables, and fences
- add resource change feeds
- define conflict and deletion semantics

### Phase 4: Event federation

- federate locations, proximities, fence events, trackable motions, and later collisions
- implement replay, gap recovery, and loop suppression
- verify multi-hop propagation behavior

### Phase 5: Cloud aggregation and policy

- build regional and aggregate deployment patterns
- add tenancy-aware routing and redaction
- add hub-to-hub operational visibility
- constrain RPC federation to audited, policy-approved paths

## Immediate Next Steps

The next implementation steps to unlock feature-complete progress are:

1. Implement the mandatory WebSocket API and make it share the existing ingest and publication service paths.
2. Define what “feature complete” means for this repository’s OMLOX scope, especially for collision support and any intentionally omitted optional behaviors.
3. Add a federation architecture model to the codebase configuration and docs: hub identity, peer definitions, REST/WebSocket transport settings, push versus pull mode, and trust settings.
4. Add a required stable configured hub UUID and use it as the origin namespace for replicated resources, events, deduplication, and RPC routing.
5. Extend auth design for machine-to-machine federation identities, per-peer scopes, and multi-issuer trust.
6. Add provenance and deduplication fields/state so a receiving hub can tell local data from replicated data and suppress loops.
7. Design cloud-authoritative metadata sync for buildings or sites, zones, fences, providers, and trackables, including source-of-truth rules, versioning, tombstones, and local offline cache behavior.
8. Design resource synchronization and change-feed behavior for zones, providers, trackables, and fences.
9. Add operational requirements for replication lag, retries, dead-lettering, and peer health.
