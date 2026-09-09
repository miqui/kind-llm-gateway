.PHONY: up down images load test-integration clean

CLUSTER_NAME := llmgw
KUBECONFIG ?= $(HOME)/.kube/config

## up: create cluster (if absent), build+load images, deploy everything in order
up:
	@kind get clusters | grep -qx $(CLUSTER_NAME) || kind create cluster --config kind/kind-config.yaml
	$(MAKE) images load
	kubectl apply -f deploy/namespace.yaml
	kubectl apply -f deploy/jaeger/
	kubectl apply -f deploy/llama/
	kubectl apply -f deploy/ngf/
	kubectl apply -f deploy/retrieval/
	kubectl apply -f deploy/llmgw/
	kubectl apply -f deploy/tenants/
	@echo "Waiting for gateway to become ready..."
	kubectl -n llmgw rollout status deploy/llmgw --timeout=300s
	@echo "Stack is up. Gateway available via NGINX Gateway Fabric (see deploy/ngf)."

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
