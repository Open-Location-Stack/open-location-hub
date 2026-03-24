# OMLOX to OpenAPI Gap Handling

This file records project-specific rules for handling gaps between the OMLOX PDF specifications and the normative REST contract in `omlox-hub.v0.yaml`.

## Position

- The OMLOX PDFs in the parent directory are the source of truth.
- `omlox-hub.v0.yaml` is the normative REST contract for this repository.
- WebSocket and MQTT are normative OMLOX protocol surfaces, but they are documented outside the REST OpenAPI contract.
- The goal is faithful OMLOX behavior, not maximal OpenAPI cleverness.

## OpenAPI 3.1 stance

- Prefer OpenAPI `3.1.0` when it adds useful precision.
- Do not chase 3.1 features for their own sake.
- If a 3.1 construct causes `oapi-codegen` to generate unstable or unusable Go types, simplify the schema and move the remaining rule into runtime validation plus documentation.
- The generated Go harness is a hard constraint. A working spec and build are more valuable than a theoretically perfect schema that the toolchain cannot consume.

## Where rules belong

Put a rule in OpenAPI when:
- it is a stable field type, enum, array shape, or simple required property
- it tightens request validation without breaking generated models
- it is part of the external REST contract clients should discover directly from the schema

Put a rule in companion documentation when:
- it is protocol-specific to WebSocket or MQTT
- it is behavioral rather than structural
- it depends on OMLOX processing flow, event timing, subscriptions, or transport-specific semantics

Put a rule in runtime validation when:
- it is cross-field or conditional and code generation handles it poorly
- it depends on stored state, object existence, or relations between resources
- the OMLOX PDF is normative but the schema/toolchain cannot express it safely

## Project conventions

- Keep inferred REST endpoints explicitly labeled as inferred when the PDF implies behavior but does not enumerate verb/path pairs.
- Preserve OMLOX `/v2` compatibility.
- Keep non-REST protocol details in `specifications/omlox/`.
- If runtime validation is required because of an OpenAPI/tooling gap, the handler or service layer should still reject invalid input with an OMLOX-aligned error response.

## Initial gaps worth tracking

### Toolchain limits

- `oapi-codegen` does not fully support OpenAPI 3.1. Use 3.1 features conservatively.
- Some schema composition patterns can collapse generated Go models into `interface{}`. When that happens on core resource types, move the rule to write-schema validation or runtime validation.

### OMLOX rules that may stay partly runtime-enforced

- Zone setup rules that depend on `type`, `incomplete_configuration`, `position`, and `ground_control_points`.
- Fence rules where `zone_id` is required when `crs=local`.
- Validation that a referenced zone, provider, or trackable actually exists.
- CRS and transformation rules that depend on zone configuration and processing flow.
- Behavioral defaults, timeout semantics, and event timing that are normative in the PDF but not fully represented by schema alone.

### Implementation guidance

- Keep request validation as close to the transport boundary as practical.
- Keep stateful OMLOX processing rules in domain or service code, not in generated transport glue.
- When a runtime-only rule is added, document it here if it is important for future spec work or code generation choices.
