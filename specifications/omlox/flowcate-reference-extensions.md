# Flowcate DeepHub Reference Extensions

This document records public DeepHub APIs and protocol features that go beyond the OMLOX Hub PDF but may still be useful implementation ideas for this repository.

These items are not normative OMLOX requirements. Treat them as candidate extensions or implementation patterns.

## Additional API surfaces worth tracking

### Object change event feeds

DeepHub exposes object change events over both WebSocket and MQTT for:
- `provider_changes`
- `trackable_changes`
- `fence_changes`
- `zone_changes`
- `anchor_changes`

Why this may be useful:
- cache invalidation for clients
- reactive admin UIs
- integration/event streaming without polling

### Anchor API and anchor entities

DeepHub adds an Anchor API and `anchor` entities associated with zones.

Why this may be useful:
- better modeling of physical RTLS infrastructure
- improved georeferencing and benchmark-based transformations
- richer admin and diagnostics workflows

### Helper endpoints for geometry construction

DeepHub adds helper endpoints such as:
- `/zones/fromlocal`
- `/fences/fromlocal`

Why this may be useful:
- easier creation of georeferenced zones/fences from local coordinates
- better operator tooling and setup workflows

### Version endpoint

DeepHub exposes `/version` for API versions.

Why this may be useful:
- health and compatibility checks
- deployment diagnostics

### Mobile Zone Extension (MZE)

DeepHub documents a Mobile Zone Extension where a proximity zone can move based on updates from a provider or trackable, configured through zone properties.

Why this may be useful:
- mobile RFID/iBeacon readers
- forklifts, carts, handheld scanners, or moving gateways

### Locating rule extension

DeepHub documents a locating rule extension around trackables and provider selection.

Why this may be useful:
- deterministic provider arbitration
- user-configurable tracking behavior beyond the minimum OMLOX baseline

### Adapter endpoints and integration surfaces

DeepHub documents product-specific integration surfaces such as:
- Cisco CMX webhook ingestion at `/adapters/cisco/locations`
- ISO-24730 adapter support
- Quuppa connector support

Why this may be useful:
- easier ingestion from legacy or vendor-specific positioning systems
- lower-friction adoption for real customer environments

### Unified Namespace (UNS) support

DeepHub documents MQTT support in the context of a Unified Namespace.

Why this may be useful:
- plant-wide event distribution
- integration with broader IIoT architectures

### RPC gateway positioning

DeepHub presents RPC as an API surface for interacting with devices and services beyond the minimal OMLOX examples.

Why this may be useful:
- future device control workflows
- firmware or device capability management

## Suggested priority for this repository

Most immediately useful:
- object change event feeds
- `/version`
- helper endpoints for local-to-global object creation
- anchors

Potentially useful later:
- mobile zones
- vendor adapters
- Unified Namespace support

Useful only when there is a concrete product need:
- DeepHub-specific auth ownership semantics
- DeepHub-only WebSocket/MQTT aliases and extra error codes

## Sources

- Flowcate DeepHub API category and product overview
- Flowcate DeepHub WebSocket API docs
- Flowcate DeepHub MQTT topics docs
- Flowcate DeepHub changelog
- Flowcate Mobile Zone Extension docs
- Flowcate Cisco CMX adapter docs
