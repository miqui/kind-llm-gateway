# kind-llm-gateway

An LLM gateway and policy lab on a local Kind cluster.

```
client → NGINX Gateway Fabric → llmgw (Go) → retrieval-svc → llama.cpp server
                                        └── Jaeger (OTel traces), SQLite (redacted logs)
```

## Status

Under construction — docs-first workflow in progress:

- Design: `docs/design/kind-llm-gateway-design.md`
- Decisions: `docs/decisions/0001` … `0007`
- Plan: `docs/plans/2026-09-09-implementation-plan.md`

## Quick start (once built)

```
make up
make test-integration
```

Requires: Docker Desktop (VM ≥ 8 GB; 24 GB recommended), kind, kubectl, Go, Rust.
