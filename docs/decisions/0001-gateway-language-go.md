---
title: ADR-0001 — Gateway language: Go
status: accepted
date: 2026-09-09
owner: Miguel Quintero
slug: kind-llm-gateway
related:
  - docs/design/kind-llm-gateway-design.md
  - docs/decisions/0002-inference-backend-llamacpp.md
---

# ADR-0001 — Gateway language: Go

## Context

The gateway (llmgw) is the one component we author ourselves; nothing off the
shelf enforces the full validation list (API keys, quotas, budgets, redaction,
allowlists, SSE proxying, OTel spans). The implementation language shapes
iteration speed, streaming ergonomics, and image size.

## Decision

Write llmgw in **Go**. Approved by Miguel at Checkpoint 1 of the kickoff
workflow ("Approve Checkpoint 1 — Go").

## Status

Accepted (2026-09-09).

## Consequences

- First-class SSE/streaming and context-based cancellation fit the
  streaming-disconnect and upstream-timeout requirements.
- Small static container images; fast builds on the Mac mini.
- The conformance test suite remains Rust-native per Miguel's standing
  preference; the language choice governs only the gateway service code.

## Alternatives considered

- **Rust** — tighter resource use and Miguel's preferred territory for
  conformance work, but slower to build/iterate for a lab artifact; rejected
  for iteration speed on this project.
