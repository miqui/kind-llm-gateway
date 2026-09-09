---
title: ADR-0007 — Cluster topology: 1 control-plane + 1 worker
status: accepted
date: 2026-09-09
owner: Miguel Quintero
slug: kind-llm-gateway
related:
  - docs/design/kind-llm-gateway-design.md
  - docs/decisions/0002-inference-backend-llamacpp.md
---

# ADR-0007 — Cluster topology: 1 control-plane + 1 worker

## Context

The cluster was originally sized as single-node for the 8 GB Docker Desktop VM
(~5.0 GB workload, ~3.1 GB headroom). Miguel explicitly prefers a two-node
topology and accepts validation risk on 8 GB, with a fallback: the cluster can
be recreated on another Mac mini (M4, 24 GB) if resource pressure breaks the
validation suite.

## Decision

The kind config defines **1 control-plane node + 1 worker node**. Node
placement:

- llama.cpp (the heavy pod) is pinned to the **worker** via nodeAffinity.
- llmgw, retrieval-svc, Jaeger, and NGINX Gateway Fabric default-spread across
  both nodes (no hard affinity beyond the llama.cpp pin).

## Status

Accepted (2026-09-09).

## Consequences

- More production-shaped: policy/control-plane workloads never compete with
  inference for the same node's memory.
- Two node-OS overheads instead of one: total footprint rises to ~6.3 GB
  against the 8192 MB VM (~1.7 GB headroom). The conformance suite running
  concurrently will feel this.
- If OOM/eviction flakiness appears during validation, the cluster migrates to
  the 24 GB M4 mini **without manifest changes** (same kind config; only the
  Docker context changes).

## Alternatives considered

- **Single-node kind** — smaller footprint (~5.0 GB, ~3.1 GB headroom), but
  rejected: Miguel prefers the two-node shape.
- **Larger multi-node topologies** — no benefit for a lab; each node costs
  ~1–1.5 GB of overhead; rejected.
