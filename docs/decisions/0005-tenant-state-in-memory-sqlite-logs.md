---
title: ADR-0005 — Tenant state in-memory; request logs in SQLite
status: accepted
date: 2026-09-09
owner: Miguel Quintero
slug: kind-llm-gateway
related:
  - docs/design/kind-llm-gateway-design.md
  - docs/decisions/0004-api-key-only-authn.md
---

# ADR-0005 — Tenant state in-memory; request logs in SQLite

## Context

Per-tenant rate limits and daily token quotas need shared state; request/response
logging with redaction needs a queryable store that conformance tests can
inspect. The Docker Desktop VM is capped at 8 GB, so heavyweight dependencies
are costly.

## Decision

- **Tenant runtime state (rate-limit buckets, quota counters): in-memory inside
  llmgw.** Fixed daily UTC window; counters reset on pod restart — a documented
  limitation. llmgw runs single-replica.
- **Request/response logs: SQLite** written by llmgw (emptyDir volume), with
  sensitive fields redacted before write (Authorization headers, API keys,
  configured regex fields: emails, phone patterns). Redaction config is a
  static compiled-in regex list plus env overrides; no hot reload.
- Tenant bootstrapping: a ConfigMap seeds initial tenants/keys; the
  `/admin/tenants` API (backed by SQLite) is the runtime source of truth.
- **Production path (documented, not built): Redis** sidecar for atomic
  distributed counters and multi-replica correctness.

## Status

Accepted (2026-09-09).

## Consequences

- No cross-replica enforcement — acceptable and documented for a single-replica
  lab; any future replica scaling requires the Redis path or equivalent.
- SQLite gives conformance tests direct SQL access to prove redaction happened.
- Quota reset semantics are deterministic (UTC daily) and testable.

## Alternatives considered

- **Redis sidecar for all state** — production-shaped but adds resource load and
  operational surface to an 8 GB VM; noted as the documented production path.
- **Stdout-only JSON logs** — simplest, but "sensitive-field redaction"
  validation is weaker without a queryable store; rejected.
