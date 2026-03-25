---
name: rtls-architecture-goals
description: Use when designing or reviewing architecture, ingestion, processing, storage, eventing, or integration changes in this repository that affect scalability, future hub federation, or end-to-end latency for location-driven decisions.
---

# RTLS Architecture Goals

Use this skill for system design and backend architecture work in this repository, especially when the task affects:

- ingestion of location or sensor updates
- processing pipelines and event propagation
- storage models, partitioning, retention, or replay
- change-heavy subsystems and high write/update rates
- topology decisions that may constrain future hub federation
- latency-sensitive flows such as fencing, alerting, and real-time decision support

## Default Design Goals

1. Design for sustained high change volume.
   Prefer approaches that remain understandable and operable as write rates, entity counts, and downstream consumers grow. Avoid designs that require global coordination, hot rows, or full-state rewrites for frequent updates.

2. Preserve federation as a future option.
   Do not bake in assumptions that only work for a single hub with purely local identifiers, single-writer ownership, or tightly coupled in-process consumers. Favor boundaries, identifiers, and event models that can later support cross-hub exchange and partial replication.

3. Prefer low-latency processing paths.
   For decision-critical flows, minimize avoidable hops, blocking dependencies, and batch-first assumptions. Treat freshness and predictable tail latency as first-class concerns when evaluating architecture.

## Guardrails

- Prefer append-oriented or event-oriented change handling over mutable shared state when change volume is high.
- Separate ingest, decision, and distribution concerns so they can scale independently.
- Avoid coupling read APIs to the internal write path in ways that make low-latency processing or federation harder later.
- Prefer idempotent handlers, stable event identity, and explicit ordering assumptions.
- Treat backpressure, replay, and partial failure as expected conditions, not edge cases.
- Keep time semantics explicit: event time, processing time, staleness windows, and out-of-order behavior should not be implicit.
- Prefer interfaces that allow asynchronous fan-out for non-critical consumers while keeping decision-critical paths short.
- Avoid premature distributed complexity, but do not choose shortcuts that block later partitioning or federation without a clear reason.

## Review Questions

When evaluating a design, ask:

- What happens when update volume increases by one or two orders of magnitude?
- Where are the coordination bottlenecks, hot partitions, or single-writer assumptions?
- Can this component be split, partitioned, replayed, or scaled independently?
- Does this design assume all relevant data is local to one hub?
- What identifier, tenancy, or provenance decisions would become a problem in a federated setup?
- Which path is latency-critical, and what is currently on that critical path?
- What are the queueing, retry, timeout, and degradation behaviors under load or downstream failure?
- Can fencing or similar decisions be made from fresh enough data without unnecessary blocking?

## Output Expectations

When using this skill:

- call out scalability, federation, and latency tradeoffs explicitly
- distinguish decision-critical paths from eventual-consistency paths
- prefer designs that remain evolvable under higher scale
- if a shortcut is acceptable for alpha, state what future migration it may force
- surface hidden assumptions about ordering, ownership, and locality
- when architecture or behavior changes, ensure `docs/architecture.md` and `engineering/implementation-plan.md` are updated to reflect the implemented state and the follow-up work that remains
