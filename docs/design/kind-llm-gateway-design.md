---
title: kind-llm-gateway Design
status: approved
date: 2026-09-09
owner: Miguel Quintero
slug: kind-llm-gateway
related:
  - docs/decisions/0001-gateway-language-go.md
  - docs/decisions/0002-inference-backend-llamacpp.md
  - docs/decisions/0003-ingress-nginx-gateway-fabric.md
  - docs/decisions/0004-api-key-only-authn.md
  - docs/decisions/0005-tenant-state-in-memory-sqlite-logs.md
  - docs/decisions/0006-retrieval-stub-in-trace-path.md
---

# kind-llm-gateway — Design

## Problem statement

How might we expose a local llama.cpp model through a purpose-built gateway so the
operational contract around AI (API-key authn, tenant quotas, budgets, redaction,
routing, tracing) is explicit, testable, and demoable — instead of treating the
model as a black box?

## Target user / use case

- Platform engineers / AI platform teams evaluating how to put a governed API
  layer in front of LLM inference.
- Miguel, as a credible platform-engineering portfolio artifact.

## Approved direction

A Go gateway service ("llmgw") in front of llama.cpp server on a local Kind
cluster, with NGINX Gateway Fabric ingress, a retrieval stub in the request path,
a real tokenizer for budget enforcement, in-memory tenant state, SQLite request
logs, and Jaeger tracing. One command brings up the whole stack; a Rust-native
conformance suite proves each validation item against the live cluster.

## Component map

```
client (curl / Rust conformance suite)
  │
NGINX Gateway Fabric  ── Gateway API HTTPRoute (TLS offload, SSE pass-through)
  │
llmgw (Go)  ── API-key authn → tenant → rate limit → quota
  │            prompt-size + token-budget checks → model allowlist/routing
  │            redacting request/response logger (SQLite)
  │            OTel spans
  ├──→ retrieval-svc (stub: tiny Go svc, fake vector store, own span)
  │        └─ returns context docs for the trace path
  └──→ llama.cpp server (Qwen2.5-1.5B-Instruct Q4_K_M + small embedding model)
              (ClusterIP only — never exposed outside the cluster)

Jaeger all-in-one (OTLP) ← spans from llmgw and retrieval-svc
```

## Environment constraints

- Mac mini, 16 GB RAM; Docker Desktop VM capped at 8 GB.
- Docker + Kind already installed.
- Models must be small (≤1.5B-class quantized) to fit alongside gateway, Jaeger,
  and the embedding model.

## MVP scope

- Kind cluster config + Makefile (`make up` builds images, loads them, deploys;
  `make down` tears down).
- llama.cpp server deployment with two models (chat + embeddings), health checks.
- llmgw endpoints: `/v1/chat/completions`, `/v1/embeddings`,
  OpenAI-compatible pass-through incl. `stream: true` (SSE), `/healthz`,
  `/admin/tenants` (CRUD for API keys and quotas).
- Policy enforcement: API-key → tenant mapping; per-tenant rate limit (token
  bucket); per-tenant daily token quota (UTC window); max prompt tokens; max
  completion budget; model allowlist per tenant; routing rules by tenant
  workload class (team / workload-class / cost tier).
- Redacting request/response logger (SQLite): strips/masks Authorization headers,
  API keys, and configured regex fields (emails, phone patterns).
- retrieval-svc stub in the chat path producing a real multi-hop trace in Jaeger.
- Rust-native conformance suite (`tests/`): one test per validation bullet,
  runnable via `make test-integration` against the live cluster.

## Not doing (and why)

- OIDC/JWT validation — explicitly descoped by Miguel; API keys only (ADR-0004).
- CI workflows — deferred to a later phase.
- Multi-replica quota correctness — in-memory state is single-replica; documented
  limitation; Redis noted as the production path (ADR-0005).
- Model fine-tuning, RAG with a real vector DB, GPU anything — lab scope.
- Response caching / semantic caching — not on the validation list.

## Edge cases / failure modes / risks

- SSE through NGINX Fabric requires disabling proxy buffering at the Route
  filter level; a dedicated conformance test streams token-by-token and asserts
  inter-chunk timing, not just final text.
- Tokenizer/model vocab mismatch: one tokenizer lib is standardized; drift
  between its counts and llama.cpp-reported usage is documented. Budget
  enforcement uses conservative pre-flight tokenizer counts.
- Quota reset semantics: fixed daily UTC window; counters lost on restart —
  documented.
- Upstream timeouts/cancellation: client disconnect mid-stream must cancel the
  llama.cpp request via context propagation — tested.
- Memory budget: 1.5B Q4 chat model + small embedding model + Jaeger + gateway
  fits within the 8 GB VM; embedding model loaded lazily.

## Resolved TBDs

1. Model pin: Qwen2.5-1.5B-Instruct Q4_K_M (chat) + nomic-embed-text-v1.5-class
   GGUF (embeddings) — approved.
2. Quota window: fixed daily UTC window, in-memory counters — approved.
3. Redaction config: static regex list compiled in + env-overridable, no hot
   reload — approved.
4. Gateway API version: pin latest stable NGINX Gateway Fabric + compatible Kind
   node image — approved.
5. Tenant config: ConfigMap bootstraps tenants; `/admin/tenants` API (backed by
   SQLite) is source of truth at runtime — approved.

## Traceability

- Direction decisions are recorded in ADRs 0001–0006 (links in frontmatter).
- The implementation plan lives at `docs/plans/` (Phase 2, pending).
