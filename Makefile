.PHONY: up up-stack down images load test-integration clean

CLUSTER_NAME := llmgw
KUBECONFIG ?= $(HOME)/.kube/config
export KUBECONFIG
KUBECTL := kubectl --context "kind-$(CLUSTER_NAME)"
ROLLOUT_TIMEOUT ?= 600s

## up: create cluster (if absent), build+load images, deploy everything in order
up:
	+@KUBE_CONTEXT="kind-$(CLUSTER_NAME)" bash deploy/up.sh $(MAKE) up-stack

## up-stack: internal bring-up steps; use up for preflight and failure diagnostics
up-stack:
	@clusters=$$(kind get clusters) && { printf '%s\n' "$$clusters" | grep -qx "$(CLUSTER_NAME)" || kind create cluster --config kind/kind-config.yaml; }
	$(MAKE) images load
	$(KUBECTL) apply -f deploy/namespace.yaml
	$(KUBECTL) apply -f deploy/jaeger/
	$(KUBECTL) apply -f deploy/llama/
	KUBE_CONTEXT="kind-$(CLUSTER_NAME)" bash deploy/ngf/install.sh
	$(KUBECTL) apply -f deploy/retrieval/
	$(KUBECTL) apply -f deploy/llmgw/
	$(KUBECTL) apply -f deploy/tenants/
	$(KUBECTL) apply -f deploy/ngf/
	@echo "Waiting for all stack deployments and Gateway readiness..."
	$(KUBECTL) -n llmgw wait --for=condition=Accepted gateway/llmgw-gateway --timeout=$(ROLLOUT_TIMEOUT)
	$(KUBECTL) -n llmgw wait --for=condition=Programmed gateway/llmgw-gateway --timeout=$(ROLLOUT_TIMEOUT)
	$(KUBECTL) -n llmgw rollout status deployment/jaeger deployment/llama-server deployment/retrieval-svc deployment/llmgw deployment/llmgw-gateway-nginx --timeout=$(ROLLOUT_TIMEOUT)
	@echo "Stack is ready in context kind-$(CLUSTER_NAME). See README.md for local access options."

## images: build local images
images:
	docker build -t llmgw:local gateway/
	docker build -t retrieval-svc:local retrieval/

## load: push local images into kind
load:
	kind load docker-image llmgw:local --name $(CLUSTER_NAME)
	kind load docker-image retrieval-svc:local --name $(CLUSTER_NAME)

## test-integration: Rust conformance suite against the live cluster
test-integration:
	cd tests && cargo test -- --nocapture

## down: delete cluster
down:
	kind delete cluster --name $(CLUSTER_NAME) || true

clean: down
