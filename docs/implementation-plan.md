# Stepwise Implementation Plan

## Milestone 0
Harness bootstrap: module, justfile, compose, Dockerfile, env defaults.

## Milestone 1
Normative OpenAPI v0 and generated server interfaces, compile-safe stubs.

## Milestone 2
Postgres schema and sqlc query layer for zone/provider/fence/trackable CRUD.

## Milestone 3
Location/proximity ingestion pipeline with transient Valkey state.

## Milestone 4
MQTT bridge for ingest/output topic hierarchy.

## Milestone 5
RPC extension bridge behavior and response aggregation.

## Milestone 6
Auth hardening: OIDC, static keys, hybrid mode + operational policies.

## Milestone 7
Observability, performance baselines, and production hardening.
