#!/usr/bin/env bash
#
# k8s-port-forward.sh — Port-forward for local K8s thin-client mode.
#
# The host runs fracta serve in remote client mode, connecting to the in-cluster
# control plane API. Only the control plane port-forward is needed — all other
# services (postgres, falkordb, gateway, workers) are accessed in-cluster.
#
# Usage: scripts/k8s-port-forward.sh    (foreground, Ctrl-C to stop)
#
set -euo pipefail

NAMESPACE="fracta"
PIDS=()

cleanup() {
    echo "Stopping port-forwards..."
    for pid in "${PIDS[@]}"; do
        kill "$pid" 2>/dev/null || true
    done
    wait 2>/dev/null
    echo "Done."
}
trap cleanup EXIT INT TERM

echo "Starting port-forward for in-cluster orchestrator mode..."

kubectl port-forward svc/fracta-controlplane 9090:9090 -n "$NAMESPACE" &
PIDS+=($!)

echo ""
echo "Port-forwards active:"
echo "  controlplane localhost:9090"
echo ""
echo "Press Ctrl-C to stop."
wait
