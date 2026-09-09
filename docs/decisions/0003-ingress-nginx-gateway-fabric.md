---
title: ADR-0003 — Ingress: NGINX Gateway Fabric
status: accepted
date: 2026-09-09
owner: Miguel Quintero
slug: kind-llm-gateway
related:
  - docs/design/kind-llm-gateway-design.md
---

# ADR-0003 — Ingress: NGINX Gateway Fabric

## Context

External traffic enters the Kind cluster through an ingress layer that must
support the Kubernetes Gateway API, TLS offload, and SSE pass-through without
buffering.

## Decision

Use **NGINX Gateway Fabric** (github.com/nginx/nginx-gateway-fabric) as the
ingress layer, pinned to the latest stable release with a compatible Kind node
image. Chosen explicitly by Miguel (Checkpoint 1 notes). Proxy buffering is
disabled at the Route/filter level so streaming responses pass through
token-by-token; a dedicated conformance test asserts inter-chunk timing.

## Status

Accepted (2026-09-09).

## Consequences

- Gateway API v1 HTTPRoute resources define routing; no legacy Ingress.
- SSE buffering is the classic failure point and is treated as a first-class
  test case, not an afterthought.
- Ingress stays thin: policy lives in llmgw, not in NGINX config.

## Alternatives considered

- **NGINX Ingress Controller (legacy)** — Ingress API, not Gateway API; rejected.
- **Envoy Gateway / Istio ambient** — heavier than needed for a lab; rejected.
