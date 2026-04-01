# Authentication and Authorization

This project supports standards-based JWT bearer authentication for the REST API and an authorization model built around JWT claims plus a server-side permissions file.

The same token verifier is also used for the OMLOX WebSocket surface, but WebSocket authentication happens per message through `params.token` instead of the HTTP `Authorization` header.

## Modes

- `none`: disable auth checks
- `oidc`: verify bearer tokens through OIDC discovery and JWKS
- `static`: verify bearer tokens against static PEM keys or JWKS URLs
- `hybrid`: accept either OIDC-verified or static-key tokens

## OIDC and JWKS

For `oidc` mode, the hub loads issuer metadata from `AUTH_ISSUER`, discovers the provider JWKS endpoint, and verifies JWT signatures and standard claims. Provider metadata and verifier state are cached and refreshed according to `AUTH_OIDC_REFRESH_TTL` instead of being reloaded on every request.

Relevant settings:

- `AUTH_ISSUER`
- `AUTH_AUDIENCE`
- `AUTH_ALLOWED_ALGS`
- `AUTH_CLOCK_SKEW`
- `AUTH_HTTP_TIMEOUT`
- `AUTH_OIDC_REFRESH_TTL`

## Authorization Model

Authorization uses a role and ownership based model:

- authenticate the bearer token first
- extract a role-like claim from the JWT via `AUTH_ROLES_CLAIM`
- load path permissions from `AUTH_PERMISSIONS_FILE`
- optionally enforce ownership checks with the claim configured by `AUTH_OWNED_RESOURCES_CLAIM`

Supported permission values:

- `CREATE_ANY`
- `READ_ANY`
- `UPDATE_ANY`
- `DELETE_ANY`
- `CREATE_OWN`
- `READ_OWN`
- `UPDATE_OWN`
- `DELETE_OWN`

Method mapping:

- `GET` and `HEAD` require `READ_*`
- `POST` requires `CREATE_*`
- `PUT` and `PATCH` require `UPDATE_*`
- `DELETE` requires `DELETE_*`

`*_OWN` permissions apply to routes that include explicit path identifiers, such as `/v2/providers/:providerId`. Collection routes such as `/v2/zones` use the corresponding `*_ANY` semantics.

## Permissions File

The permissions file is YAML. Top-level keys are values from the configured role claim. In production this would usually be a role or group claim. For the included Dex development fixture, the role claim is set to `email` because Dex's local password database produces deterministic user identity claims without extra role mapping. That is a development convenience, not a production recommendation.

Example:

```yaml
admin@example.com:
  description: Full access
  /v2/*:
    - CREATE_ANY
    - READ_ANY
    - UPDATE_ANY
    - DELETE_ANY
  rpc:
    discover: true
    invoke:
      "*": true

reader@example.com:
  description: Read-only access
  /v2/zones:
    - READ_ANY
  /v2/zones/:zoneId:
    - READ_ANY
  /v2/rpc/available:
    - READ_ANY
  rpc:
    discover: true
    invoke:
      com.omlox.ping: true
      com.omlox.identify: true
```

Path placeholders are used for ownership checks. The hub derives claim keys from route parameter names. For example `:providerId` maps to `provider_ids`.

RPC policy entries are evaluated after route-level authorization. They use a
dedicated `rpc` section per role:

- `discover: true` allows `GET /v2/rpc/available`
- `invoke` lists allowed method names
- `invoke` entries may be:
  - exact method names such as `com.omlox.ping`
  - prefix wildcards such as `com.vendor.*`
  - `*` for full RPC invocation access

This means a role can be allowed to reach the RPC endpoint path but still be
blocked from invoking a specific method.

WebSocket policy entries are evaluated separately from REST route permissions. They use a dedicated `websocket` section per role:

- `subscribe` lists topic names or wildcard patterns the role may subscribe to
- `publish` lists topic names or wildcard patterns the role may send `message` events to

Example:

```yaml
admin@example.com:
  websocket:
    subscribe:
      "*": true
    publish:
      location_updates: true
      proximity_updates: true
```

The WebSocket policy matcher supports exact topic names and suffix-style wildcard patterns such as `location_*`.

Subscribe-only topics include:
- `location_updates`
- `location_updates:geojson`
- `proximity_updates`
- `trackable_motions`
- `fence_events`
- `fence_events:geojson`
- `collision_events` when collisions are enabled
- `metadata_changes` for resource create, update, and delete notifications on zones, fences, trackables, and location providers

## Ownership Claims

Ownership-aware rules use the claim configured by `AUTH_OWNED_RESOURCES_CLAIM`.

Expected shape:

```json
{
  "<owned_resources_claim>": {
    "provider_ids": ["provider-1"],
    "trackable_ids": ["trackable-1"],
    "zone_ids": ["zone-1"],
    "fence_ids": ["fence-1"],
    "source_ids": ["source-1"]
  }
}
```

For `*_OWN` permissions, the request path parameter must be present in the matching owned-resource list.

## Error Handling

- `401 Unauthorized`: missing bearer header, malformed token, invalid signature, bad issuer, bad audience, expired token, or other authentication failure
- `403 Forbidden`: authenticated token lacks a matching permission or ownership claim
- `403 Forbidden` on RPC also covers missing method-level discovery or invocation permission
- WebSocket auth failures are returned as OMLOX wrapper `error` events with code `10004`

Authentication failures return a `WWW-Authenticate: Bearer` header and the API error body.

## WebSocket Authentication

When auth is enabled:
- every WebSocket `subscribe` and `message` event must carry the JWT access token in `params.token`
- the hub authenticates and authorizes each message independently
- the WebSocket upgrade itself is intentionally allowed without an HTTP bearer header so the OMLOX `params.token` model can be used
- route-style REST permissions do not grant WebSocket topic access automatically; the `websocket` policy block must allow the topic

If a topic is valid but disabled by configuration, the WebSocket layer returns an OMLOX wrapper `error` event with code `10002` and a descriptive message instead of treating it as an unknown topic.

## Dex Development Setup

This repository includes a Dex fixture at [tools/dex/config.yaml](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/tools/dex/config.yaml) and a matching permissions file at [config/auth/permissions.yaml](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/config/auth/permissions.yaml).

`docker compose` starts Dex on port `5556` and configures the app container to verify Dex-issued tokens with:

- `AUTH_MODE=oidc`
- `AUTH_ISSUER=http://dex:5556/dex`
- `AUTH_AUDIENCE=open-rtls-cli`
- `AUTH_ROLES_CLAIM=email`

Included test users:

- `admin@example.com` / `testpass123`
- `reader@example.com` / `testpass123`
- `owner@example.com` / `testpass123`

Fetch a token:

```bash
curl -sS -X POST http://localhost:5556/dex/token \
  -u open-rtls-cli:cli-secret \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data 'grant_type=password&scope=openid%20email%20profile&username=admin@example.com&password=testpass123'
```

Use the returned `access_token` as the bearer token when calling the hub.

## Other Providers

Keycloak and similar OIDC providers fit the same model if they expose:

- issuer discovery
- JWKS
- a stable audience for the hub
- a claim that can be mapped via `AUTH_ROLES_CLAIM`

For production deployments, prefer a real role or group claim instead of the Dex development fixture's email-based mapping. The hub is intended to verify JWT access tokens from the production IdP, not development-specific token handling.

## RPC Security Guidance

For RPC in production:
- require JWT auth
- treat `GET /v2/rpc/available` as sensitive metadata
- grant `com.omlox.ping` and `com.omlox.identify` more broadly only if operators really need them
- grant `com.omlox.core.xcmd` only to tightly controlled roles or automation identities
- keep MQTT broker access narrow so user-facing applications cannot bypass the hub's policy and audit layer

## End-to-End Coverage

The integration suite boots Dex and the hub, obtains a bearer token from Dex, and proves:

- authenticated requests reach protected endpoints
- missing or invalid tokens return `401`
- insufficient permissions return `403`
- ownership-restricted routes reject tokens that lack owned-resource claims
