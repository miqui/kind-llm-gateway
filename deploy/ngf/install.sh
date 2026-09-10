#!/usr/bin/env bash
# Installs NGINX Gateway Fabric (NGF) v2.7.0 into the cluster, pinned to
# match the versions recorded in this file's provenance comment below.
#
# NGF version : v2.7.0
# Gateway API : v1.6.1 (standard channel CRDs)
# Source      : https://github.com/nginx/nginx-gateway-fabric/releases/tag/v2.7.0
#
# Called by make up; can also be invoked directly:
#   deploy/ngf/install.sh
set -euo pipefail

GATEWAY_API_VERSION="v1.6.1"
NGF_VERSION="v2.7.0"
KUBE_CONTEXT="${KUBE_CONTEXT:-kind-llmgw}"
KUBECTL=(kubectl --context "$KUBE_CONTEXT")

install_crds() {
  "${KUBECTL[@]}" apply "$@" -o name |
    while IFS= read -r resource; do
      case "$resource" in
        customresourcedefinition.apiextensions.k8s.io/*)
          "${KUBECTL[@]}" wait --for=condition=Established "$resource" --timeout=120s
          ;;
      esac
    done
}

echo "==> Installing Gateway API CRDs (${GATEWAY_API_VERSION}, standard channel)"
install_crds -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GATEWAY_API_VERSION}/standard-install.yaml"

echo "==> Installing NGINX Gateway Fabric CRDs (${NGF_VERSION})"
# --server-side is required: the nginxproxies CRD's embedded annotations
# exceed kubectl's client-side apply last-applied-configuration limit.
install_crds --server-side --force-conflicts -f "https://raw.githubusercontent.com/nginx/nginx-gateway-fabric/${NGF_VERSION}/deploy/crds.yaml"

echo "==> Installing NGINX Gateway Fabric (${NGF_VERSION})"
"${KUBECTL[@]}" apply -f "https://raw.githubusercontent.com/nginx/nginx-gateway-fabric/${NGF_VERSION}/deploy/default/deploy.yaml"

echo "==> Waiting for nginx-gateway pods to be ready"
"${KUBECTL[@]}" -n nginx-gateway rollout status deployment/nginx-gateway --timeout=180s
