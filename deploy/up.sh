#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="${KUBE_CONTEXT:-kind-llmgw}"
KUBECTL=(kubectl --context "$KUBE_CONTEXT" --request-timeout=10s)

preflight() {
  local tool missing=0
  for tool in bash make docker kind kubectl grep tail; do
    if ! command -v "$tool" >/dev/null 2>&1; then
      echo "Missing required tool: $tool. Install it and rerun make up." >&2
      missing=1
    fi
  done
  if (( missing )); then
    return 1
  fi
  if ! docker info >/dev/null; then
    echo "Docker is unavailable. Start Docker Desktop (or your Docker daemon) and check your Docker context before rerunning make up." >&2
    return 1
  fi
}

diagnostic() {
  if "${KUBECTL[@]}" "$@"; then
    return 0
  else
    echo "Warning: diagnostic command failed: kubectl $*" >&2
  fi
}

diagnostics() {
  local namespace pods pod
  echo "==> Startup diagnostics (context: $KUBE_CONTEXT)" >&2
  if ! "${KUBECTL[@]}" get nodes -o wide; then
    echo "Cannot reach the target cluster; skipping Kubernetes diagnostics. See the startup error above." >&2
    return
  fi
  for namespace in nginx-gateway llmgw; do
    echo "==> $namespace: deployments, pods, and recent events" >&2
    diagnostic -n "$namespace" get deployments,pods -o wide
    diagnostic -n "$namespace" get events --sort-by=.metadata.creationTimestamp | tail -n 31
    if [[ "$namespace" == llmgw ]]; then
      diagnostic -n "$namespace" get gateways,httproutes -o wide
    fi
    if ! pods=$("${KUBECTL[@]}" -n "$namespace" get pods \
      --field-selector=status.phase!=Succeeded -o name); then
      echo "Warning: cannot list pods in $namespace; skipping their logs." >&2
      continue
    fi
    while IFS= read -r pod; do
      [[ -n "$pod" ]] || continue
      echo "==> $namespace/$pod: recent container logs" >&2
      diagnostic -n "$namespace" logs "$pod" --all-containers=true \
        --prefix --timestamps --tail=40 --limit-bytes=8192 --pod-running-timeout=5s
      local restarted
      if restarted=$("${KUBECTL[@]}" -n "$namespace" get "$pod" \
        -o 'jsonpath={range .status.initContainerStatuses[?(@.restartCount>0)]}{.name}{"\n"}{end}{range .status.containerStatuses[?(@.restartCount>0)]}{.name}{"\n"}{end}'); then
        local container
        while IFS= read -r container; do
          [[ -n "$container" ]] || continue
          echo "==> $namespace/$pod/$container: previous container logs" >&2
          diagnostic -n "$namespace" logs "$pod" -c "$container" --previous \
            --prefix --timestamps --tail=40 --limit-bytes=8192 --pod-running-timeout=5s
        done <<< "$restarted"
      else
        echo "Warning: cannot inspect restarts for $namespace/$pod." >&2
      fi
    done <<< "$pods"
  done
}

on_exit() {
  local status=$?
  trap - EXIT
  if (( status != 0 )); then
    echo "Startup failed (exit $status). Collecting diagnostics..." >&2
    # Diagnostics are best-effort and must not replace the startup exit status.
    set +e
    diagnostics
  fi
  exit "$status"
}

if (( $# == 0 )); then
  echo "Usage: bash deploy/up.sh <startup command> [arguments...]" >&2
  exit 2
fi
preflight
trap on_exit EXIT
"$@"
