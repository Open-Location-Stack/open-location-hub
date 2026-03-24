---
name: enterprise-security-standards
description: Use when designing, reviewing, or implementing authentication, authorization, secret handling, network trust, operational hardening, or security-sensitive API behavior in this repository. Emphasize standards compliance, enterprise-grade controls, fail-safe behavior, auditability, and production-readiness.
---

# Enterprise Security Standards

Use this skill for any security-relevant work in this repository, especially:

- authentication and authorization
- JWT, OIDC, OAuth2, JWKS, and trust configuration
- API error handling for auth failures
- secrets, credentials, and config surfaces
- transport security, network exposure, and container setup
- enterprise security reviews and production hardening

## Security Bar

Default to enterprise-grade security expectations:

- standards-compliant behavior over provider-specific shortcuts
- deny by default
- least privilege
- fail safe on uncertainty
- explicit trust boundaries
- deterministic authorization behavior
- auditable decisions and operational visibility

Development conveniences are acceptable only when they are clearly isolated from production guidance and cannot be mistaken for production defaults.

## Required Principles

1. Prefer standards-compliant token usage.
   Resource servers should validate access tokens, not repurpose identity tokens as API bearer credentials, unless the standard and deployment model clearly justify it.

2. Treat OIDC discovery and JWKS as trust infrastructure.
   Validate issuer, audience, expiry, not-before, signature, and allowed algorithms. Cache keys responsibly, but avoid designs where transient IdP outages cause unnecessary outages if last-known-good verification material is still valid.

3. Authorization must be explicit and deterministic.
   Rule precedence must not depend on incidental file order or ambiguous wildcard behavior. Prefer exact and specific matches over broad matches, and document precedence.

4. Least privilege must be real, not theoretical.
   Do not document or expose permission modes that cannot actually be enforced by the request shape or data model. If `OWN` semantics cannot be evaluated for an operation, do not pretend they exist.

5. Secret handling must be production-aware.
   Never normalize committed secrets, static passwords, password grants, or plaintext local transport as production examples. If dev fixtures require them, label them as dev-only and keep them out of production guidance.

6. Errors must be safe and operable.
   Return correct status codes such as `401` and `403`, avoid leaking sensitive internals to clients, and preserve enough structured information in logs and metrics for operators to diagnose failures.

7. Config must be safe by default.
   Security-relevant config should validate early, reject invalid combinations, and avoid surprising insecure fallback behavior.

## Review Checklist

When reviewing or implementing security-sensitive changes, check for:

- token type correctness: access token vs identity token
- issuer and audience enforcement
- algorithm restrictions and key selection behavior
- JWKS refresh and outage behavior
- wildcard or precedence bugs in authorization rules
- privilege escalation via broad role mapping or mutable identity claims
- inability to enforce documented permissions
- secrets committed in repo or shown in non-dev docs
- missing audit logs, metrics, or health signals for auth systems
- production guidance contaminated by local development shortcuts

## Project-Specific Guidance

- Keep REST auth behavior aligned with OpenAPI where possible, but do not force fragile OpenAPI constructs when the real security model is server-side policy driven.
- Prefer environment-driven configuration, but treat auth configuration as high risk and validate it aggressively.
- Docker and Dex are acceptable for local development and end-to-end testing, but production assumptions must remain IdP-agnostic and standards-based.
- For this project, enterprise readiness means robust security for all externally reachable surfaces, not just passing local integration tests.

## Output Expectations

When doing security work here:

- call out enterprise risks directly
- separate dev/test convenience from production posture
- prefer concrete remediations over generic advice
- if something is not production-ready, say so plainly
