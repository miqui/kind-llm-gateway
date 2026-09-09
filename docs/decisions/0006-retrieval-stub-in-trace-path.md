---
title: ADR-0006 — Retrieval stub in the trace path
status: accepted
date: 2026-09-09
owner: Miguel Quintero
slug: kind-llm-gateway
related:
  - docs/design/kind-llm-gateway-design.md
---

# ADR-0006 — Retrieval stub in the trace path

## Context

The tracing validation item requires distributed traces that connect an external
request to retrieval, tool calls, and model inference. A direct
gateway → llama.cpp path cannot demonstrate retrieval hops.

## Decision

Add a tiny **retrieval-svc** stub (Go) in the chat completion path:

`llmgw → retrieval-svc (fake vector store, own span, simulated latency) → llama.cpp`

The stub returns a small set of canned context documents; llmgw emits OTel spans
for each hop, exported to Jaeger all-in-one via OTLP. The result is a real
multi-hop trace: external request → gateway policy decisions → retrieval →
inference.

## Status

Accepted (2026-09-09).

## Consequences

- One more small service to build and deploy — justified by making the tracing
  validation item genuinely provable rather than aspirational.
- Trace context propagates W3C `traceparent` from the client through ingress,
  gateway, retrieval, and inference; the conformance suite can assert the full
  span tree via Jaeger's API.
- Tool-call hops are represented in the same pattern if ever added; no separate
  tool service in the MVP.

## Alternatives considered

- **Direct path (gateway → llama.cpp only)** — simpler, but leaves the retrieval
  portion of the tracing item unproven; rejected per Checkpoint 2.
