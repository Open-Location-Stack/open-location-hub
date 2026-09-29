# OMLOX source comparison, 29 September 2026

The local checkout of `omlox-api-documentation` supplies useful machine-readable
API material, but its default-branch document is not newer than the downloaded
published specification.

| Source inspected | Finding |
| --- | --- |
| Downloaded `omlox-hub-spec_20112_V200_Aug23.pdf` | Published Hub 2.0.0, August 2023. This remains the behavioral baseline. |
| Git `main`, `49a887a`, `omlox-Hub-spec_20112_d200_Feb23.docx` | February 2023 review draft. Recent repository commits do not make this an August-2023-or-later specification. |
| Git `main`, `omlox_Hub_API_2.0.0.json` | OpenAPI 3.0.2 describing Hub API 2.0.0. Valuable for REST paths, query parameters, and response shapes. The repository explicitly defers to the final specification. |
| Git `develop`, `d7c3919`, LaTeX 2.0.1 draft | March 2026 conversion work. It retains older draft wording in several places; the version label alone does not establish a newer published behavioral baseline. |
| Git `develop`, archived August 2023 PDF | All 131 extracted pages match the downloaded published PDF after removing the download watermark and PDF-generation footer. This is a text comparison, not a claim of byte-identical PDFs. |

The main-branch JSON is byte-identical to both develop's working
`omlox_Hub_API.json` and its archived released `omlox_Hub_API-2.0.0.json`.

## Material differences between sources

- The older prose uses `/v1/ws/socket`; the published document uses `/v2/ws/socket`.
- The older prose omits the published `proximity_updates` WebSocket subsection
  (13.6.7) and the XCMD_BC broadcast RPC topic
  `/omlox/jsonrpc/rpc/com.omlox.core.xcmd/broadcast`.
- RPC caller-interface wording differs between the drafts and the published text.
- The JSON zone-creation description says a supplied ID is replaced. The published
  API permits caller-provided IDs; the implementation preserves them.
- The JSON makes collision `position` and `geometry` optional; published section
  6.7.1 requires both. The repository contract requires them.
- The JSON describes WebSocket payload elements as strings and combines numeric
  error codes with text enum values. Actual protocol objects and numeric codes
  follow the published protocol sections.
- An older restriction that `provider_id` must be null on trackable fence events
  was removed from the published document. Provider identity is retained.
- The JSON omits RPC and some GeoJSON WebSocket variants. Those are specified in
  the published prose and companion protocol documents.
- JSON `PUT /ws/socket` is a placeholder for a WebSocket channel, not a missing
  REST write operation. The server upgrades `GET /v2/ws/socket`.
- Both the published object table and JSON restrict `collision_area` to
  `LineString | Point`, while chapter 11 describes an intersection area. This
  implementation encodes the overlap boundary as a closed LineString and a
  tangent contact as a Point.
- Fence object-field wording on default tolerance timeouts is internally
  inconsistent. The explicit note in section 8.2.1.4 and transition table 13 govern
  runtime behavior. Section 10.1 uses the fence's own timeout despite looser
  descriptions of provider/trackable `fence_timeout` fields.

## Implementation reconciliation

The comparison found the following implementation gaps. Open Location Hub 0.2 addresses
them in the normative contract, runtime, CLI, and local regression tests.

| Gap before 0.2 | 0.2 behavior and evidence |
| --- | --- |
| Collections returned resources instead of IDs; several PUTs returned representations | ID collections, complete `/summary` resources, published 200/204 status distinctions; handler and CLI HTTP tests. |
| Missing provider-to-trackable and fence-to-trackable/motion relationships | `/providers/{id}/trackables`, `/fences/{id}/trackables`, `/fences/{id}/motions`; relation and spatial-query tests. |
| Bulk ingestion used POST | PUT with 204, synchronized CLI and connector scripts. |
| Missing or ineffective `crs`, `zone_id`, `geojson`, `foreign_id`, `spatial_query`, `force_location_update`, and `subdivide` behavior | REST projection/filter/reprocessing and subdivision support; GeoJSON alternatives are represented in OpenAPI. |
| GCP object encoding and insufficient mappings | Alternating geographic/local arrays with at least four pairs, finite-coordinate validation and preserved transform `source`. |
| Sensor schemas constrained arbitrary JSON; enums and numeric constraints were too loose | Arbitrary JSON sensor documents, standard technology/type enums, double-precision numbers, nonnegative geometry and -1/infinite timeouts. |
| Missing location defaults and ambiguous foreign zone mapping | Hub-generated timestamp fallback, zone/default floor, local/floor defaults, unknown-local-source rejection, unique nonempty zone `foreign_id`. |
| Proximity coordinates and default hysteresis differed from the baseline | Zone WGS84 position and floor; immediate switching by default, optional hysteresis only when configured. |
| Provider association and location choice ignored some trackables/rules | Every assigned trackable can be updated; per-trackable locating rules, generated-time ordering, deterministic ties, isolated Kalman state and forced reprocessing. |
| Fence tolerance/deadline behavior depended on new observations | Table-13 transitions, specificity and fallback, autonomous expiry, retained logical membership, spatial-query distinction and deletion cleanup. |
| Trackable extent and collision behavior used default radii, bounding boxes, or debouncing | Radius defaults to zero, circular footprint, tangency/holes/floor/height checks, first-mover ordering, every significant intersecting update, table-14 delays and autonomous expiry. |
| WebSocket CRS parameters filtered instead of projecting; duplicate variants and connection-local subscription IDs | One default WGS84 variant, native local output on request, target projection of positions/geometry, floor/object-type filtering, IDs unique across connections. |
| MQTT used 3.1.1 and retained announcements lacked expiry | MQTT 5, 120-second announcement expiry, external registry freshness and broker-backed integration coverage. |

The comparison is an engineering conformance assessment, not certification. Optional
hub extensions (for example Kalman normalization and proximity hysteresis) are
separate from the default baseline. XCMD device execution still requires a
hardware-specific adapter; federation is outside this single-hub implementation.
Those existing deployment boundaries are not presented as new REST gaps.

See [integration compatibility](../../docs/omlox-0.2-integration-compatibility.md)
for script and CLI impacts. Live DeepHub and ZIGPOS checks are explicitly skipped
because those hubs are unavailable.
