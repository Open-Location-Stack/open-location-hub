# Reference Extensions

This document records public reference APIs and protocol features that go beyond the OMLOX Hub PDF.

These items are not normative OMLOX requirements. Treat them as candidate extensions or implementation patterns.

## External API Surfaces

### Object change event feeds

A reference implementation exposes object change events over both WebSocket and MQTT for:
- `provider_changes`
- `trackable_changes`
- `fence_changes`
- `zone_changes`
- `anchor_changes`

Repository relevance:
- cache invalidation for clients
- reactive admin UIs
- integration/event streaming without polling

### Anchor API and anchor entities

A reference implementation adds an Anchor API and `anchor` entities associated with zones.

Repository relevance:
- better modeling of physical location infrastructure
- improved georeferencing and benchmark-based transformations
- richer admin and diagnostics workflows

### Helper endpoints for geometry construction

A reference implementation adds helper endpoints such as:
- `/zones/fromlocal`
- `/fences/fromlocal`

Repository relevance:
- easier creation of georeferenced zones/fences from local coordinates
- better operator tooling and setup workflows

### Version endpoint

A reference implementation exposes `/version` for API versions.

Repository relevance:
- health and compatibility checks
- deployment diagnostics

### Mobile Zone Extension (MZE)

A reference implementation documents a Mobile Zone Extension where a proximity zone can move based on updates from a provider or trackable, configured through zone properties.

Repository relevance:
- mobile RFID/iBeacon readers
- forklifts, carts, handheld scanners, or moving gateways

### Locating rule extension

A reference implementation documents a locating rule extension around trackables and provider selection.

Repository relevance:
- deterministic provider arbitration
- user-configurable tracking behavior beyond the minimum OMLOX baseline

### Adapter endpoints and integration surfaces

A reference implementation documents product-specific integration surfaces such as:
- Cisco CMX webhook ingestion at `/adapters/cisco/locations`
- ISO-24730 adapter support
- Quuppa connector support

Repository relevance:
- easier ingestion from legacy or vendor-specific positioning systems
- lower-friction adoption for real customer environments

### Unified Namespace (UNS) support

A reference implementation documents MQTT support in the context of a Unified Namespace.

Repository relevance:
- plant-wide event distribution
- integration with broader IIoT architectures

### RPC gateway positioning

A reference implementation presents RPC as an API surface for interacting with devices and services beyond the minimal OMLOX examples.

Repository relevance:
- device control workflows
- firmware or device capability management

## Repository Triage

Most immediately useful:
- object change event feeds
- `/version`
- helper endpoints for local-to-global object creation
- anchors

Useful when a concrete integration needs them:
- mobile zones
- vendor adapters
- Unified Namespace support

Useful only when there is a concrete product need:
- product-specific auth ownership semantics
- product-specific WebSocket/MQTT aliases and extra error codes

## Sources

- Vendor API category and product overview
- Vendor WebSocket API docs
- Vendor MQTT topics docs
- Vendor changelog
- Flowcate Mobile Zone Extension docs
- Flowcate Cisco CMX adapter docs
