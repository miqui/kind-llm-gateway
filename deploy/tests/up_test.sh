#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."

# Exported shell functions keep these cases independent of Docker and Kubernetes.
command() {
  if [[ "${MODE:-}" == missing && "$*" == "-v kind" ]]; then
    return 1
  fi
  builtin command "$@"
}
docker() {
  [[ "${MODE:-}" != docker-down ]]
}
kind() { :; }
kubectl() {
  if [[ "$1" != --context || "$2" != kind-test || "$3" != --request-timeout=10s ]]; then
    echo "UNSAFE_CONTEXT"
    return 99
  fi
  shift 3
  if [[ "${MODE:-}" == unreachable ]]; then
    return 1
  fi
  if [[ "${MODE:-}" == diagnostic-failure && "$*" != "get nodes -o wide" ]]; then
    return 1
  fi
  case "$*" in
    *"get pods --field-selector="*) echo pod/example ;;
    *"get pod/example -o jsonpath="*) echo chat ;;
    *) echo "KUBECTL: $*" ;;
  esac
}
export -f command docker kind kubectl
export KUBE_CONTEXT=kind-test

run_case() {
  export MODE="$1"
  local expected="$2"
  shift 2
  status=0
  output=$(bash deploy/up.sh "$@" 2>&1) || status=$?
  if [[ "$status" != "$expected" || "$output" == *UNSAFE_CONTEXT* ]]; then
    printf 'FAIL %s: expected exit %s, got %s\n%s\n' "$MODE" "$expected" "$status" "$output" >&2
    exit 1
  fi
}
contains() {
  if [[ "$output" != *"$1"* ]]; then
    printf 'FAIL %s: missing %s\n%s\n' "$MODE" "$1" "$output" >&2
    exit 1
  fi
}
excludes() {
  if [[ "$output" == *"$1"* ]]; then
    printf 'FAIL %s: unexpected %s\n%s\n' "$MODE" "$1" "$output" >&2
    exit 1
  fi
}

run_case missing 1 bash -c 'echo STARTED'
contains "Missing required tool: kind"
excludes STARTED
excludes "Startup diagnostics"

run_case docker-down 1 bash -c 'echo STARTED'
contains "Docker is unavailable"
excludes STARTED
excludes "Startup diagnostics"

run_case success 0 bash -c 'echo STARTED'
contains STARTED
excludes "Startup diagnostics"

run_case startup-failure 42 bash -c 'exit 42'
contains "Startup failed (exit 42)"
contains "context: kind-test"
contains "get deployments,pods"
contains "get events --sort-by=.metadata.creationTimestamp"
contains "get gateways,httproutes"
contains "--tail=40 --limit-bytes=8192"
contains "-c chat --previous"

run_case unreachable 23 bash -c 'exit 23'
contains "Cannot reach the target cluster"

run_case diagnostic-failure 17 bash -c 'exit 17'
contains "Warning: diagnostic command failed"
contains "cannot list pods in llmgw"

echo "All 6 startup wrapper cases passed."
