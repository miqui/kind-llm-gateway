---
title: ADR-0004 — API-key-only authentication (no OIDC/JWT)
status: accepted
date: 2026-09-09
owner: Miguel Quintero
slug: kind-llm-gateway
related:
  - docs/design/kind-llm-gateway-design.md
  - docs/decisions/0005-tenant-state-in-memory-sqlite-logs.md
---

# ADR-0004 — API-key-only authentication (no OIDC/JWT)

## Context

The original idea listed JWT/OIDC validation among the authn items. Miguel
descoped it explicitly to keep the lab simple: "do not impl oidc, lets use api
key only to keep things simple."

## Decision

Authentication is **API keys only**. Each key maps to exactly one tenant. The
`Authorization: Bearer <key>` header (OpenAI style) is the accepted credential
location. `/admin/tenants` issues and revokes keys. OIDC/JWT validation is out
of scope for this project and would require a new ADR to reintroduce.

## Status

Accepted (2026-09-09).

## Consequences

- No identity provider dependency; the lab is fully self-contained.
- Tenant identification is exact: key → tenant, no token-claim mapping.
- Redaction must still mask keys/Authorization headers in logs even though keys
  are the only credential.
- Production hardening path (OIDC via an external provider) is documented as a
  natural extension point.

## Alternatives considered

- **API keys + OIDC/JWT validation** — rejected by Miguel for simplicity.
