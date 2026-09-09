# kind-llm-gateway Implementation Plan

> **For Hermes:** Use subagent-driven-development skill to implement this plan task-by-task.

**Goal:** A local Kind cluster running llama.cpp behind a policy-enforcing Go gateway (llmgw) with NGINX Gateway Fabric ingress, proving every validation bullet with a Rust-native conformance suite.

**Architecture:** client → NGINX Gateway Fabric (Gateway API HTTPRoute, SSE pass-through) → llmgw (Go: API-key authn → tenant → rate limit → quota → budgets → allowlist/routing → redacting SQLite logger → OTel) → retrieval-svc stub (chat path only) → llama.cpp server (ClusterIP only). Jaeger all-in-one collects OTLP spans. 1 control-plane + 1 worker node (ADR-0007); llama.cpp pinned to worker.

**Tech Stack:** Go 1.23+ (llmgw, retrieval-svc), llama.cpp llama-server (Qwen2.5-1.5B-Instruct Q4_K_M + nomic-embed Q8), Kind, NGINX Gateway Fabric, Jaeger all-in-one, SQLite (llmgw logs + admin state), Rust (conformance suite, reqwest + tokio), Makefile glue.

**ADRs:** 0001 Go · 0002 llama.cpp/models · 0003 NGF · 0004 API-key-only · 0005 in-memory state + SQLite logs · 0006 retrieval stub · 0007 two-node topology.

**Constraints:** Docker Desktop VM 8 GB (fallback: 24 GB M4 mini, no manifest changes). No OIDC/JWT (ADR-0004). No CI (deferred). Quotas are single-replica in-memory, daily UTC window (ADR-0005). Budget enforcement uses pre-flight tokenizer counts (tiktoken BPE), documented drift vs llama.cpp usage.

**Repo layout:**

```
kind-llm-gateway/
├── Makefile
├── kind/kind-config.yaml
├── deploy/
│   ├── namespace.yaml
│   ├── llama/            (deployment, svc, pvc-less model init via image or initContainer)
│   ├── llmgw/            (deployment, svc, configmap, sqlite pvc via emptyDir)
│   ├── retrieval/        (deployment, svc)
│   ├── jaeger/           (deployment, svc)
│   ├── ngf/              (gatewayclass, gateway, httproutes, nfr install kustomization)
│   └── tenants/bootstrap-configmap.yaml
├── gateway/              (Go module: llmgw)
│   ├── cmd/llmgw/main.go
│   └── internal/
│       ├── config/       auth/  ratelimit/  quota/  tokens/  policy/
│       ├── proxy/        logging/  tenants/  tracing/  server/
├── retrieval/            (Go module: retrieval-svc)
├── tests/                (Rust crate: conformance suite)
│   └── src/tests/*.rs    (one module per validation bullet)
└── docs/
```

---

### Task 1: Repo scaffold + kind cluster config

**Objective:** Makefile, kind config (2 nodes, 7 GB mem reservation), .gitignore, namespace manifests.

**Files:**
- Create: `kind/kind-config.yaml`, `Makefile`, `.gitignore`, `deploy/namespace.yaml`, `README.md` (stub)

**kind/kind-config.yaml:**
```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  extraKubeletConfig:
    kubeReserved:
      memory: "512Mi"
- role: worker
  extraKubeletConfig:
    kubeReserved:
      memory: "512Mi"
```

**Makefile targets:** `up` (create cluster if absent, build images, kind load, kubectl apply in dependency order: namespace → jaeger → llama → ngf → retrieval → llmgw → gateway api resources → tenants), `down` (kind delete), `test-integration` (cargo test in tests/), `images` (docker build llmgw + retrieval), `load` (kind load docker-image).

**Verify:** `kind create cluster --config kind/kind-config.yaml --name llmgw` → `kubectl get nodes` shows 2 nodes Ready. Commit: `chore: scaffold repo, kind config, Makefile`.

### Task 2: llama.cpp server deployment

**Objective:** llama-server with chat + embedding models on the worker node, OpenAI-compatible endpoints, health checks.

**Files:**
- Create: `deploy/llama/deployment.yaml`, `deploy/llama/svc.yaml`, `deploy/llama/models-init.yaml` (initContainer downloads pinned GGUFs from HF into emptyDir)

Details: single pod, two-model llama-server binary invocation — run **two containers** in one pod: `chat` (Qwen2.5-1.5B-Instruct Q4_K_M, port 8080, `--n-gpu-layers 0`, mem limit 2304Mi) and `embed` (nomic-embed Q8, port 8081, mem limit 512Mi, startupProbe with generous failureThreshold for lazy load). nodeAffinity pins pod to worker. Liveness/readiness on `/health`. Service `llama-svc` exposes both ports.

**Verify:** `kubectl -n llmgw port-forward svc/llama-svc 8080:8080` → `curl localhost:8080/v1/chat/completions -d '{"model":"qwen2.5-1.5b","messages":[{"role":"user","content":"hi"}],"max_tokens":5}'` returns a completion; `/v1/embeddings` returns a vector. Commit: `feat: llama.cpp server deployment with chat and embedding models`.

### Task 3: llmgw skeleton (Go) — server, config, healthz

**Objective:** Go module with HTTP server, config from env, /healthz, request-id middleware; Dockerfile (multi-stage, distroless/static).

**Files:**
- Create: `gateway/go.mod`, `gateway/cmd/llmgw/main.go`, `gateway/internal/server/server.go`, `gateway/internal/config/config.go`, `gateway/Dockerfile`
- Test: `gateway/internal/server/server_test.go`

Server: chi or stdlib mux (prefer stdlib `net/http` + Go 1.22 pattern routing). Config env: `LLAMA_CHAT_URL`, `LLAMA_EMBED_URL`, `RETRIEVAL_URL`, `SQLITE_PATH`, `JAEGER_OTLP_ENDPOINT`, `LISTEN_ADDR`, `ADMIN_ADDR`, redaction env overrides.

**Verify (TDD):** test asserts `/healthz` returns 200 `ok`. `go test ./...` passes. Commit: `feat: llmgw skeleton with healthz and config`.

### Task 4: API-key authn + tenant registry (ADR-0004, 0005)

**Objective:** Bearer-key → tenant resolution; tenant store seeded from bootstrap ConfigMap, persisted in SQLite via /admin CRUD.

**Files:**
- Create: `gateway/internal/auth/auth.go`, `gateway/internal/tenants/store.go`, `gateway/internal/tenants/model.go`, `deploy/tenants/bootstrap-configmap.yaml`
- Test: `gateway/internal/auth/auth_test.go`, `gateway/internal/tenants/store_test.go`

Tenant model: `{id, name, api_key (unique), rate_limit_rps, daily_token_quota, allowed_models []string, workload_class, cost_tier}`. Store: SQLite (`tenants` table) bootstrapped from ConfigMap JSON on first run (insert-if-absent). Auth middleware: parse `Authorization: Bearer`, constant-time lookup, 401 `{"error":{"type":"invalid_api_key",...}}` on miss, injects tenant into context.

**Verify (TDD):** tests: missing header → 401; unknown key → 401; valid key → tenant in context; concurrent lookups race-free (`go test -race`). Commit: `feat: api-key authn and tenant registry`.

### Task 5: Rate limiting + daily token quota (ADR-0005)

**Objective:** Per-tenant token-bucket rate limiter and daily UTC token-quota ledger, in-memory.

**Files:**
- Create: `gateway/internal/ratelimit/bucket.go`, `gateway/internal/quota/ledger.go`
- Test: `gateway/internal/ratelimit/bucket_test.go`, `gateway/internal/quota/ledger_test.go`

Bucket: golang.org/x/time/rate keyed by tenant, burst = 2× rps → 429 with `Retry-After`. Ledger: map[tenantID]{date, tokens}; UTC date rollover resets; `Reserve(n)/Commit(n)/Refund(n)` semantics (reserve pre-flight estimate, commit actual usage after response).

**Verify (TDD):** fake clock tests: burst exceed → 429; quota exhaustion → 429 with quota-exceeded error type; UTC midnight rollover resets. Commit: `feat: per-tenant rate limiting and daily token quota`.

### Task 6: Tokenizer + prompt-size / token-budget enforcement

**Objective:** Real tokenizer counts for pre-flight checks (ADR/design: conservative pre-flight counts, documented drift).

**Files:**
- Create: `gateway/internal/tokens/counter.go`, `gateway/internal/policy/budget.go`
- Test: `gateway/internal/tokens/counter_test.go`, `gateway/internal/policy/budget_test.go`

Use `github.com/pandodao/tiktoken-go` (or pinned tiktoken-go equivalent; vendor the BPE file into the image). Budget policy from env/config: `MAX_PROMPT_TOKENS` (per-tenant override wins), `MAX_COMPLETION_TOKENS` (cap on request `max_tokens`, reject if > budget). Checks happen before any upstream call.

**Verify (TDD):** counts known strings exactly; oversize prompt → 400 `context_length_exceeded`-style error; `max_tokens` over budget → 400. Commit: `feat: tokenizer-based prompt-size and token-budget enforcement`.

### Task 7: Model allowlist + routing rules

**Objective:** Per-tenant model allowlist; routing by workload class / cost tier to upstream endpoints.

**Files:**
- Create: `gateway/internal/policy/router.go`
- Test: `gateway/internal/policy/router_test.go`

Routing table (config): workload_class/cost_tier → target (`chat` → llama chat container; `embed` → embed container; route `/v1/embeddings` always to embed). Disallowed model → 403 `model_not_allowed` naming tenant.

**Verify (TDD):** allowed/denied model matrix; unknown model → 404; tier-based target selection. Commit: `feat: model allowlists and tenant-based routing`.

### Task 8: Redacting request/response logger (SQLite) (ADR-0005)

**Objective:** Log every request/response to SQLite with sensitive fields masked before write.

**Files:**
- Create: `gateway/internal/logging/redact.go`, `gateway/internal/logging/store.go`
- Test: `gateway/internal/logging/redact_test.go`, `gateway/internal/logging/store_test.go`

Redaction (applied before persistence, never after): `Authorization` header → `[REDACTED]`; any 32+ char token matching key shape → `[REDACTED]`; compiled-in regex list + env overrides for emails, phone patterns. Store: `requests` table (id, ts, tenant, model, route, prompt_tokens, completion_tokens, status, latency_ms, request_json, response_json — redacted), bounded retention (max N rows / age, swept on insert).

**Verify (TDD):** round-trip test writes a request containing a fake key + email + phone, reads back via SQL, asserts masked. Commit: `feat: redacting sqlite request logger`.

### Task 9: Proxy to llama.cpp incl. SSE streaming

**Objective:** OpenAI-compatible pass-through for `/v1/chat/completions` and `/v1/embeddings`; `stream: true` proxies SSE chunk-by-chunk; client disconnect cancels upstream.

**Files:**
- Create: `gateway/internal/proxy/openai.go`, `gateway/internal/proxy/sse.go`
- Test: `gateway/internal/proxy/sse_test.go` (httptest upstream)

Non-stream: read body → policy chain (Task 4–8) → forward → relay response → commit actual usage from response `usage`. Stream: flush per SSE event (`http.Flusher` after each write), parse `usage` from final chunk if present else estimate; `request.Context().Done()` cancels upstream request (tested with httptest upstream asserting handler sees cancellation).

**Verify (TDD):** end-to-end httptest: stream relays N events in order with flush between; upstream cancellation on client disconnect; usage accounting commits. Commit: `feat: openai-compatible proxy with sse streaming and cancellation`.

### Task 10: retrieval-svc stub (ADR-0006)

**Objective:** Tiny Go service on the chat path: receives {query, tenant}, sleeps ~50 ms (simulated vector search), returns canned context docs; emits its own OTel span.

**Files:**
- Create: `retrieval/go.mod`, `retrieval/cmd/main.go`, `retrieval/Dockerfile`
- Test: `retrieval/main_test.go`

**Verify (TDD):** returns 3 docs, propagates traceparent. Commit: `feat: retrieval stub service`.

### Task 11: OTel tracing wiring (llmgw + retrieval → Jaeger)

**Objective:** Spans: `http.route` at gateway → policy decisions as events → `retrieval.search` child → `llm.upstream.chat` client span. W3C traceparent propagated end-to-end; client-supplied traceparent honored.

**Files:**
- Modify: `gateway/cmd/llmgw/main.go`, `gateway/internal/tracing/tracing.go`, `retrieval/cmd/main.go`
- Create: `deploy/jaeger/deployment.yaml`, `deploy/jaeger/svc.yaml` (all-in-one, OTLP :4317/:4318, memory 512Mi, span limits tuned, sampling: parent-based always-on for lab)

**Verify:** integration smoke (deferred full assert to suite): after a chat request, Jaeger API `GET /api/traces?service=llmgw` returns a trace with 3+ services. Commit: `feat: opentelemetry tracing to jaeger`.

### Task 12: NGINX Gateway Fabric + Gateway API resources

**Objective:** NGF installed; Gateway + HTTPRoutes route `/v1/*` and `/admin/*` to llmgw; SSE pass-through verified config.

**Files:**
- Create: `deploy/ngf/gateway.yaml`, `deploy/ngf/httproute-v1.yaml`, `deploy/ngf/httproute-admin.yaml`, `deploy/ngf/kustomization.yaml` (NGF CRDs+helm template pinned version)

HTTPRoute for `/v1/*` sets NGF-specific `proxy_buffering off` via BackendTrafficPolicy/annotation per pinned NGF version docs; timeouts raised for long streams (e.g. 300 s); request body size limit aligned with prompt budget.

**Verify:** `kubectl get gateway` programmed; curl through NodePort/LB: `/healthz` 200; a small streamed completion arrives with visible incremental chunks (curl -N). Commit: `feat: nginx gateway fabric routes with sse pass-through`.

### Task 13: Admin tenants API

**Objective:** `/admin/tenants` CRUD backed by SQLite (runtime source of truth per ADR-0005); guarded by admin key (separate env `ADMIN_API_KEY`).

**Files:**
- Create: `gateway/internal/server/admin.go`
- Test: `gateway/internal/server/admin_test.go`

POST create (returns generated key once), GET list (keys masked), PATCH quota/allowlist, DELETE revoke. Changes take effect immediately (store is the same one auth reads).

**Verify (TDD):** create → use new key through authn → rotate quota → exceeded behavior updates. Commit: `feat: admin tenants crud api`.

### Task 14: Rust conformance suite

**Objective:** Rust-native test crate, one module per validation bullet, `make test-integration` runs it against the live cluster (base URL + test admin key from env).

**Files:**
- Create: `tests/Cargo.toml`, `tests/src/lib.rs`, `tests/src/tests/mod.rs` and modules: `routing.rs`, `auth.rs`, `quotas.rs`, `budgets.rs`, `redaction.rs`, `allowlist.rs`, `streaming.rs`, `tracing.rs`
- Create: `tests/README.md`

Tests (against gateway base URL):
- `routing.rs` — `/v1/chat/completions` and `/v1/embeddings` shaped per OpenAI (fields present, `usage` reported).
- `auth.rs` — no key 401; bad key 401; tenant header/id surfaced in response metadata.
- `quotas.rs` — tiny daily quota tenant → requests succeed until 429 `quota_exceeded`; rate limit burst → 429 with Retry-After.
- `budgets.rs` — prompt over max → 400; `max_tokens` over budget → 400.
- `redaction.rs` — send request with email/phone/key-shaped strings, then read SQLite (via `kubectl exec` sqlite3) and assert masked.
- `allowlist.rs` — allowed model 200, denied model 403.
- `streaming.rs` — SSE: collect chunk arrival times, assert ≥3 inter-chunk gaps < 1500 ms (proves no proxy buffering) and final assembled text non-empty; client disconnect mid-stream cancels upstream (assert via Jaeger span duration < threshold or llama log).
- `tracing.rs` — send request with generated traceparent, then query Jaeger API for the trace: spans present for llmgw, retrieval-svc, llama upstream; service count ≥ 3.

**Verify:** `cargo test` compiles; full run requires `make up` cluster — document expected runtime. Commit: `test: rust-native conformance suite for all validation bullets`.

### Task 15: Glue, README, end-to-end pass

**Objective:** `make up` brings everything up cold; `make test-integration` green; README documents architecture, ADRs, quota limitations, resource tuning, M4 fallback.

**Files:**
- Modify: `Makefile`, `README.md`, `docs/design/kind-llm-gateway-design.md` (link ADR-0007, plan)

**Verify (acceptance):** fresh `kind delete cluster && make up && make test-integration` — all conformance tests pass. Commit: `docs: readme and end-to-end wiring`.

---

## Known risks mapped to tasks
- SSE buffering (Task 12 + streaming.rs asserts timing, not just text)
- Tokenizer drift (Task 6; documented in README)
- 8 GB pressure (ADR-0007; fallback documented, no manifest changes)
- Quota restart loss (ADR-0005; README limitation note)
