# OpenAPI Governance

`specifications/openapi/omlox-hub.v0.yaml` is normative for REST behavior.

## Rules
1. API changes must be spec-first. Specs can be found in pdf form in the parent directory. Ask user for location if missing.
2. When the PDFs define object fields or API behavior but do not fully spell out REST paths or nested field names, keep those additions explicit in the companion contract or docs as inferred.
3. Cross-check omlox PDF changes against the repo notes in `specifications/omlox/` when they exist, but treat the official PDFs as the source of truth.
4. Preserve omlox `/v2` route compatibility unless the user explicitly asks for a different API version.
5. Keep MQTT and WebSocket protocol details documented separately and do not model them as REST endpoints in OpenAPI.
6. If OpenAPI changes expand the generated server surface, refresh generated code and align handler signatures before finishing.
7. The goal of this project is a faithful implementation of the omlox hub specification. Do not deviate from official APIs and cleanly isolate non-standard additions and extensions.
8. Do not claim certification, official compliance status, endorsement, or logo usage unless the relevant PI/PNO certification has actually been obtained.
9. Do not reproduce or redistribute official spec PDFs, figures, or extracted text beyond minimal quotation that is legally necessary. Prefer independently authored summaries and derived OpenAPI contracts.
10. Backwards compatibility with previous versions of this software is not a go. Do not introduce compatibility layers when making changes unless confirmed by the user. They are redundant bloat.

## Required Workflow
1. `just bootstrap`
2. `just generate`
3. `just check`

Documentation-only exception:
- If the change is documentation-only and does not alter the OpenAPI contract, generated outputs, or implementation behavior, do not run repository test gates solely for that documentation edit.
