# Integration compatibility for Open Location Hub 0.2

This note records integration impacts of Open Location Hub 0.2.
The target is the published August 2023 omlox Hub 2.0.0 specification, with the
companion JSON used to check REST interoperability. Version 0.2 intentionally
breaks compatibility with earlier Open Location Hub releases.

These findings come from source inspection and local tests. They do not establish
compatibility with a currently running DeepHub or ZIGPOS hub. Neither live hub is
available, so live interoperability testing is explicitly outside the 0.2
validation scope. Validation results below distinguish local execution from vendor interoperability.

## Existing integration paths

- `scripts/log_locations.py`, `log_fence_events.py`, and `log_collision_events.py`
  use `ws_ndjson_logger.py` to record omlox WebSocket envelopes from a configurable
  endpoint. Their payloads can be used by the replay connector.
- `scripts/check_fence_alignment.py` compares recorded locations against hub
  fences. It now requests `/v2/fences/summary` because `/v2/fences` returns IDs.
- The sibling `plugfest-hub-connector` checkout contains a DeepHub WebSocket
  forwarder and a CorivaHub MQTT forwarder. Its `src/lib/olh-client.mjs` invokes
  `olh` from PATH or the explicit `OLH_CLI_PATH` executable to submit normalized data to Open Location Hub.
- The inspected scripts do not identify a ZIGPOS-specific adapter. ZIGPOS test
  coverage must be tied to the actual configuration or recording used in the
  earlier integration; do not treat the CorivaHub adapter as evidence for it.

## Confirmed contract changes and their impact

| Change | Integration impact |
| --- | --- |
| Bulk ingestion uses `PUT /v2/providers/locations` and `PUT /v2/providers/proximities`, returning 204 | Rebuild the sibling CLI and set `OLH_CLI_PATH` for the plugfest bridge. Its commands are now `locations put` and `proximities put`. The previous vendored executable is no longer selected by default; an old executable that sends POST will fail. Do not parse a body from 204. |
| Zone/provider/fence and sensor PUT responses return 204 | Setup scripts must retrieve the resource with GET if they need its representation after updating. Trackable PUT still returns 200 with a representation. |
| Resource collections return arrays of IDs; `/summary` returns full resources | Scripts that inspect object fields must use `/summary`. The bridge's provider probe only checks HTTP success, so it does not depend on the old object-array shape. |
| GCP input is alternating `[longitude, latitude], [x, y]` arrays, with at least four mappings | Old Open Location Hub object-shaped GCP fixtures and three-point demo setups must be replaced. Do not modify recordings already using the standard representation. |
| Zone types and location/provider technologies use the standard enums | The previous plugfest normalizer used `provider_type: "omlox"`, which is not a technology value. The updated plugfest normalizer maps missing or nonstandard technologies to `unknown`, preserves the original nonstandard string in `properties.upstream_provider_type`, and keeps upstream hub identity in `properties`. |
| REST location and fence reads default to EPSG:4326 | Comparison tools must request or verify their CRS explicitly. Local coordinates cannot be interpreted as longitude/latitude. A foreign local zone needs its configuration imported before projection. |
| Proximity-derived locations use the zone's WGS84 position and floor | Old expectations that label these coordinates `local` must change. Check RFID/iBeacon replay separately from UWB position replay. |
| Transform responses use `source` | Replace clients that read a nonstandard `zone_id` field from a transform response. |
| Location selection uses generated timestamps and locating rules | Replaying stale timestamps into a hub with newer live data must not move trackables backwards. Use an isolated replay hub or explicitly rewrite event times in the replay adapter. |
| A provider can update every trackable assigned to it | Recordings involving shared providers can now produce motion and fence events for multiple trackables. Each trackable applies its own locating rules. |
| `COLLISION_DEFAULT_RADIUS_METERS` and `COLLISION_COLLIDING_DEBOUNCE` are removed | Configure physical radius on each trackable; absent radius means zero. Consumers can receive a `colliding` event on every significant intersecting update. |
| WebSocket location/motion output defaults to one WGS84 variant; explicit `crs=local` returns native input | Recorders or bridges needing original local coordinates must include `crs=local` in subscription parameters. Projection parameters no longer suppress records merely because their source frame differs. |
| Omitted timestamps, floor and elevation references receive specification defaults | Recorded output can contain fields omitted from the input. Missing generated time means hub arrival time, while supplied generated time is retained. |
| Unknown local sources are rejected and zone foreign IDs are unique | Import source zones before replay. Resolve duplicate nonempty zone `foreign_id` values before migration 00003. An incomplete zone permits native delivery but cannot provide geographic projection. |
| MQTT requires version 5 and RPC announcements expire after 120 seconds | Use an MQTT 5 broker. Stopped external RPC handlers disappear from discovery; applications must handle unavailable handlers. |
| Proximity hysteresis defaults to zero | RFID/iBeacon observations switch zones immediately unless grace/dwell extensions are explicitly configured. |
| GTFS/OpenSky provider technology labels are corrected | GTFS defaults to `gps`, OpenSky to `unknown`; feed identity stays in properties. Replay maps nonstandard labels to `unknown` while retaining `properties.upstream_provider_type`. Update old environment overrides too. |
| Fence/collision timing and geometry are corrected | Event counts, entry/exit times, and timeout exits can change even for identical recordings. Compare against the specification and the input geometry, not old event-count snapshots alone. |

## DeepHub-specific evidence and local checks

The existing DeepHub forwarder tries several deployment-specific WebSocket paths.
Those upstream paths are configuration details; Open Location Hub's endpoint
remains `/v2/ws/socket`. Its normalizer preserves explicitly supplied CRS, source,
provider identity, position, and upstream metadata. These values must remain
intact through forwarding and CLI serialization.

The source subscribes to both `location_updates` and `trackable_motions`, and can
normalize both into location ingestion. Verify that these streams do not cause
duplicate decisions for the same provider observation. Preserve supplied provider
IDs exactly; do not infer an identity migration from stricter technology enums.

A vendor default CRS must not be assumed when an incoming location omits `crs`.
The current normalizer defaults that field to `local`. Verify the actual source
subscription and zone configuration before using those records in a geographic
comparison.

## Local replay evidence to retain

For each available DeepHub and ZIGPOS recording or local replay, record the known product/version, adapter/configuration,
topic and subscription parameters, hub/CLI Git revisions, and the recording used.
Keep credentials out of the report. Verify:

1. Resource setup and readback, including IDs, summaries, GCPs, and 204 updates.
2. PUT ingestion through the rebuilt CLI and unchanged WebSocket envelope parsing.
3. Native, WGS84, and requested-zone projections with known reference positions.
4. Provider associations and trackable selection with delayed and duplicate data.
5. Provider and trackable fencing, radius/height behavior, and exits without a new
   position update; collision start/continuation/end where supported.
6. Arbitrary JSON sensor values and extension-property preservation.

A passing local replay does not establish live interoperability. Live checks are
skipped because the upstream hubs are unavailable; they are not a release gate
for Open Location Hub 0.2.

## Local verification for 0.2

- The CLI HTTP tests verify PUT ingestion, 204 handling, arbitrary JSON sensor
  updates, the new relationship routes, and projection/filter query forwarding.
- The plugfest adapter tests verify the new CLI commands and preservation of
  coordinates, identifiers, generated timestamps, and custom properties, plus
  normalization of nonstandard technology values. All seven Node tests pass.
- Hub tests cover rule-based UWB/GPS selection, older/equal timestamps, concurrent
  provider updates under the race detector, and reprocessing changed trackable
  metadata without altering provider observations.
- The local Mosquitto integration test verifies the retained MQTT 5 message-expiry
  property. WebSocket tests check default/native projection, cross-connection IDs,
  and zone/floor defaults. The migration test rejects duplicate foreign zone IDs.
- Python validation covers all locked connector environments and UWB, GTFS 204
  readback, and replay normalization tests.
- `just check` passed, including generated-contract checks, module tidiness,
  vet/staticcheck, vulnerability analysis, build, unit tests and Docker integration
  tests (64.163 seconds for the integration package on the final run).
- `just test-unit -race` passed. The CLI passed `just test` and `just build`;
  the rebuilt executable reports `0.2.0`. `just check-python` passed all five
  locked environments and 12 connector tests. All seven plugfest Node tests passed.
- No vendor endpoint was contacted. These results do not claim a live DeepHub or
  ZIGPOS integration, conformance certification, or a published release.


These are local results. Live DeepHub and ZIGPOS tests are intentionally skipped.
