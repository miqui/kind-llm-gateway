#!/usr/bin/env bash
# Installs NGINX Gateway Fabric (NGF) v2.7.0 into the cluster, pinned to
# match the versions recorded in this file's provenance comment below.
#
# NGF version : v2.7.0
# Gateway API : v1.6.1 (standard channel CRDs)
# Source      : https://github.com/nginx/nginx-gateway-fabric/releases/tag/v2.7.0
#
# Not wired into the Makefile; invoke directly:
#   deploy/ngf/install.sh
set -euo pipefail

GATEWAY_API_VERSION="v1.6.1"
NGF_VERSION="v2.7.0"

echo "==> Installing Gateway API CRDs (${GATEWAY_API_VERSION}, standard channel)"
kubectl apply -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GATEWAY_API_VERSION}/standard-install.yaml"

echo "==> Installing NGINX Gateway Fabric CRDs (${NGF_VERSION})"
# --server-side is required: the nginxproxies CRD's embedded annotations
# exceed kubectl's client-side apply last-applied-configuration limit.
kubectl apply --server-side --force-conflicts -f "https://raw.githubusercontent.com/nginx/nginx-gateway-fabric/${NGF_VERSION}/deploy/crds.yaml"

echo "==> Installing NGINX Gateway Fabric (${NGF_VERSION})"
kubectl apply -f "https://raw.githubusercontent.com/nginx/nginx-gateway-fabric/${NGF_VERSION}/deploy/default/deploy.yaml"

echo "==> Waiting for nginx-gateway-fabric pods to be ready"
kubectl -n nginx-gateway rollout status deployment/nginx-gateway-fabric --timeout=180s
