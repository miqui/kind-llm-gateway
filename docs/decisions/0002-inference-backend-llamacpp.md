---
title: ADR-0002 — Inference backend: llama.cpp server
status: accepted
date: 2026-09-09
owner: Miguel Quintero
slug: kind-llm-gateway
related:
  - docs/design/kind-llm-gateway-design.md
  - docs/decisions/0001-gateway-language-go.md
---

# ADR-0002 — Inference backend: llama.cpp server

## Context

The lab needs a local inference backend with OpenAI-compatible endpoints
(`/v1/chat/completions`, `/v1/embeddings`) that fits inside a Docker Desktop VM
capped at 8 GB on a 16 GB Mac mini.

## Decision

Use **llama.cpp server (llama-server)** with two pinned models:

- Chat: Qwen2.5-1.5B-Instruct Q4_K_M GGUF
- Embeddings: nomic-embed-text-v1.5-class Q8 GGUF (loaded lazily)

Approved by Miguel (Checkpoint 1: "use llama.cpp server unless this constraint
cannot be met"; models approved at Checkpoint 3). llama-server runs as a
ClusterIP-only Service inside Kind; clients reach it only through llmgw.

## Status

Accepted (2026-09-09).

## Consequences

- Native OpenAI-compatible API means the gateway is a true policy proxy, not a
  translation layer.
- Token counts reported by llama-server may drift from the gateway's tokenizer
  lib; the design standardizes on one tokenizer for pre-flight budget checks and
  documents the drift.
- Resource envelope: 1.5B Q4 chat model + small embedding model fits the 8 GB
  VM alongside Jaeger and the gateway.

## Alternatives considered

- **Ollama** — heavier runtime for the same models; rejected.
- **Lightweight vLLM-compatible deployment** — CPU inference story is weaker on
  macOS/Kind; rejected for this lab.
