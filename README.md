# kind-llm-gateway

An LLM gateway and policy lab on a local Kind cluster: a purpose-built Go
gateway (llmgw) enforcing the *operational contract* around AI — API-key authn,
per-tenant quotas and rate limits, token budgets, redacted request logging,
model allowlists, and end-to-end distributed traces — in front of llama.cpp.

```
client (curl / Rust conformance suite)
  │
NGINX Gateway Fabric ── Gateway API HTTPRoute (SSE pass-through, buffering off)
  │
llmgw (Go) ── API-key authn → tenant → rate limit → quota → token budgets
  │           → model allowlist/routing → redacting SQLite logger → OTel spans
  ├──→ retrieval-svc (stub: fake vector store, own span)
  └──→ llama.cpp server (Qwen2.5-1.5B-Instruct Q4_K_M chat + nomic-embed Q8)
              (ClusterIP only — never exposed)

Jaeger all-in-one (OTLP :4318) collects spans from llmgw and retrieval-svc.
```

## Requirements

- Docker Desktop (VM memory ≥ 8 GB works; 24 GB recommended — see below)
- kind + kubectl
- Go 1.27+ (gateway + retrieval build) — only needed for `make images`
- Optional: Rust toolchain for the conformance suite (`make test-integration`)

## Repository layout

```
kind/kind-config.yaml          2-node kind cluster (1 control-plane + 1 worker)
deploy/namespace.yaml          llmgw namespace
deploy/jaeger/                 Jaeger all-in-one (OTLP 4317/4318, UI 16686)
deploy/llama/                  llama.cpp server (chat :8080 + embed :8081 containers)
deploy/retrieval/              retrieval stub deployment + svc
deploy/llmgw/                  gateway deployment + svc + admin secret
deploy/ngf/                    NGINX Gateway Fabric install + Gateway + HTTPRoutes
deploy/tenants/                tenants bootstrap ConfigMap (team-a, team-b)
gateway/                       llmgw Go module (all policy logic)
retrieval/                     retrieval-svc Go module (stub)
tests/                         Rust conformance suite (one module per validation item)
docs/design/                   design doc
docs/decisions/                ADRs 0001–0007
docs/plans/                    implementation plan
```

## Setup (complete bring-up)

```bash
# 1. Create the cluster (2 nodes: llmgw-control-plane + llmgw-worker)
kind create cluster --config kind/kind-config.yaml

# 2. Namespace + Jaeger
kubectl apply -f deploy/namespace.yaml
kubectl apply -f deploy/jaeger/

# 3. llama.cpp server (downloads pinned GGUFs from HuggingFace on first run,
#    ~1.2 GB; chat model loads at startup, embed model lazily)
kubectl apply -f deploy/llama/
kubectl -n llmgw rollout status deploy/llama-server --timeout=600s

# 4. NGINX Gateway Fabric (pinned v2.7.0) — see install.sh for provenance
deploy/ngf/install.sh

# 5. Build + load local images
docker build -t llmgw:local gateway/
docker build -t retrieval-svc:local retrieval/
kind load docker-image llmgw:local retrieval-svc:local --name llmgw

# 6. Deploy services + policies (order matters: services before routes)
kubectl apply -f deploy/retrieval/
kubectl apply -f deploy/llmgw/
kubectl apply -f deploy/tenants/
kubectl apply -f deploy/ngf/

# 7. Wait for the gateway
kubectl -n llmgw rollout status deploy/llmgw --timeout=300s

# 8. Or all of the above via make:
make up
```

## Exposing the gateway

The Gateway is provisioned but Kind has no cloud LB, so pick one:

```bash
# Option A — port-forward (dev)
kubectl -n llmgw port-forward svc/llmgw 8080:8080

# Option B — NodePort (set in deploy/ngf/gateway.yaml), then use any node IP
# kubectl -n llmgw get gateway llmgw-gateway -o wide
```

## Try it

```bash
# health
curl localhost:8080/healthz

# chat completion (tenant team-a)
curl localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-team-a-0000000000000000" \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen2.5-1.5b","messages":[{"role":"user","content":"Say OK"}],"max_tokens":5}'

# embeddings
curl localhost:8080/v1/embeddings \
  -H "Authorization: Bearer sk-team-b-0000000000000000" \
  -H "Content-Type: application/json" \
  -d '{"input":"hello world","model":"nomic-embed-text"}'

# streaming (SSE)
curl -N localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-team-a-0000000000000000" \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen2.5-1.5b","messages":[{"role":"user","content":"Count to 5"}],"max_tokens":30,"stream":true}'
```

Seeded tenants (see `deploy/tenants/bootstrap-configmap.yaml`):

| tenant | api key | rps | daily token quota | notes |
|--------|---------|-----|-------------------|-------|
| team-a | `sk-team-a-0000000000000000` | 2 | 100,000 | standard tier |
| team-b | `sk-team-b-0000000000000000` | 1 | 10,000 | economy tier |

## Policy behavior (what to validate)

- **Auth**: `Authorization: Bearer <key>` → tenant; unknown/missing → 401
  `invalid_api_key`
- **Rate limit**: token bucket per tenant (burst = 2×rps) → 429 + `Retry-After`
- **Quota**: daily UTC token ledger; exhausted → 429 `quota_exceeded`; reserves
  pre-flight, commits actual usage from response, refunds on upstream failure.
  Counters are in-memory: they reset on pod restart (documented limitation,
  ADR-0005).
- **Budgets**: prompt tokens (tiktoken cl100k_base, vendored) > `MAX_PROMPT_TOKENS`
  → 400 `context_length_exceeded`; `max_tokens` > `MAX_COMPLETION_TOKENS` → 400
  `max_tokens_exceeded`. Enforcement is pre-flight, before any upstream call.
- **Allowlist/routing**: model not in tenant's `allowed_models` → 403
  `model_not_allowed`; unknown model → 404 `model_not_found`; `/v1/embeddings`
  always routes to the embed upstream.
- **Redaction**: Authorization headers, `sk-…` keys, emails, phone patterns are
  masked *before* writing to the SQLite request log (`/data/llmgw.db` in-pod).
  Extra patterns via `REDACT_EXTRA_PATTERNS` (comma-separated regexes).
- **Tracing**: W3C `traceparent` honored end-to-end. Send one with your request
  and find it in Jaeger: `kubectl -n llmgw port-forward svc/jaeger 16686:16686`
  → http://localhost:16686 (or query `/api/traces?service=llmgw`).
- **SSE**: `stream: true` is proxied chunk-by-chunk (NGF buffering disabled via
  ProxySettingsPolicy, 300 s timeouts).

## Teardown (complete)

```bash
kind delete cluster --name llmgw        # or: make down
```

That removes everything (all resources live inside the cluster). Leftovers on
the host are only local docker images (`llmgw:local`, `retrieval-svc:local`) —
optional cleanup:

```bash
docker rmi llmgw:local retrieval-svc:local
```

## Conformance suite (Rust)

```bash
# against a running stack:
export GATEWAY_URL=http://localhost:8080
export TEST_ADMIN_KEY=sk-admin-0000000000000000   # lab admin key
make test-integration                              # runs cargo test in tests/
```

One test module per validation bullet: routing, auth, quotas, budgets,
redaction (reads the SQLite log directly), allowlist, streaming (asserts
inter-chunk timing, not just final text), tracing (asserts the Jaeger span tree
via its API).

## Resource notes

- Sized for a Docker Desktop VM with ~6.3 GB free: llama.cpp is pinned to the
  worker node (2.3 Gi + 512 Mi limits), Jaeger 512 Mi, gateway 256 Mi.
- On an 8 GB VM the cluster is tight (~1.7 GB headroom); if OOM/eviction
  flakiness appears, recreate the same cluster on a larger host (e.g. a 24 GB
  M4 Mac mini) — **no manifest changes needed**, only Docker context.
- Model downloads happen once per pod into an emptyDir volume; the chat model
  reloads at pod start (~10–20 s to first token).

## Known limitations (by design, see ADRs)

- Quota counters are in-memory and single-replica; Redis is the documented
  production path (ADR-0005).
- llama.cpp does not emit OTel spans; the trace's inference hop is represented
  by the gateway's `llm.upstream.chat/embed` client span.
- Tokenizer counts (cl100k_base) may drift slightly from llama.cpp's reported
  usage; budget checks are deliberately conservative.
- API keys only — no OIDC/JWT by explicit decision (ADR-0004).

## Docs

- Design: `docs/design/kind-llm-gateway-design.md`
- Decisions: `docs/decisions/0001` … `0007` (language, backend, ingress, auth,
  state/logs, retrieval stub, cluster topology)
- Plan: `docs/plans/2026-09-09-implementation-plan.md`
