#!/usr/bin/env bash
#
# k8s-smoke-test.sh — End-to-end smoke test for fracta local K8s mode.
#
# Prerequisites:
#   - Docker Desktop Kubernetes enabled and running
#   - make k8s-setup completed (all pods deployed + secrets created)
#   - op CLI authenticated (for env vars via .op-env)
#   - kubectl, curl, psql, make on PATH
#
# What it does:
#   1. Verifies all pods are running (incl. fracta-controlplane)
#   2. Starts port-forwards to cluster services (incl. CP API on 9090)
#   3. Waits for gateway and CP API /healthz
#   4. Spawns a test agent via the in-cluster control-plane API
#   5. Waits for agent Job completion
#   6. Fetches agent logs
#   7. Queries postgres for agent_events
#   8. Cleans up
#
set -euo pipefail

NAMESPACE="fracta"
TASK_NAME="smoke-test-$(date +%s)"
PORT_FORWARD_PIDS=()

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}[smoke]${NC} $*"; }
warn()  { echo -e "${YELLOW}[smoke]${NC} $*"; }
fail()  { echo -e "${RED}[smoke]${NC} $*"; exit 1; }

cleanup() {
    info "Cleaning up..."
    # Kill port-forwards
    for pid in "${PORT_FORWARD_PIDS[@]}"; do
        kill "$pid" 2>/dev/null || true
    done
    # Delete test Job
    kubectl delete job "fracta-agent-${TASK_NAME}" -n "$NAMESPACE" --ignore-not-found 2>/dev/null || true
    # Clean up configmaps and secrets created for the agent
    kubectl delete configmap "fracta-config-${TASK_NAME}" -n "$NAMESPACE" --ignore-not-found 2>/dev/null || true
    kubectl delete secret "fracta-auth-${TASK_NAME}" -n "$NAMESPACE" --ignore-not-found 2>/dev/null || true
    info "Cleanup complete."
}
trap cleanup EXIT

# --- Step 1: Verify pods are running ---
info "Step 1: Checking pod status..."
PODS=$(kubectl get pods -n "$NAMESPACE" --no-headers 2>/dev/null || true)
if [[ -z "$PODS" ]]; then
    fail "No pods found in namespace $NAMESPACE. Run 'make k8s-setup' first."
fi

for component in postgres falkordb elastic-mcp vendor-mcp fracta-gateway fracta-controlplane; do
    if ! echo "$PODS" | grep -q "$component"; then
        fail "Missing pod: $component. Run 'make k8s-setup' first."
    fi
done
info "All expected pods found."

# Wait for pods to be ready
info "Waiting for pods to be ready..."
kubectl wait --for=condition=ready pod -l app=fracta -n "$NAMESPACE" --timeout=120s || \
    fail "Pods did not become ready within 120s."
info "All pods ready."

# --- Step 2: Start port-forwards ---
info "Step 2: Starting port-forwards..."

kubectl port-forward svc/postgres 5432:5432 -n "$NAMESPACE" &>/dev/null &
PORT_FORWARD_PIDS+=($!)

kubectl port-forward svc/falkordb 6379:6379 -n "$NAMESPACE" &>/dev/null &
PORT_FORWARD_PIDS+=($!)

kubectl port-forward svc/fracta-gateway 8080:8080 -n "$NAMESPACE" &>/dev/null &
PORT_FORWARD_PIDS+=($!)

kubectl port-forward svc/fracta-controlplane 9090:9090 -n "$NAMESPACE" &>/dev/null &
PORT_FORWARD_PIDS+=($!)

# --- Step 3: Verify port-forwards are reachable ---
info "Step 3: Verifying port-forwards..."

# Postgres
for i in $(seq 1 15); do
    if pg_isready -h localhost -p 5432 -U fracta >/dev/null 2>&1; then
        info "Postgres reachable on localhost:5432."
        break
    fi
    if [[ $i -eq 15 ]]; then
        fail "Postgres port-forward not reachable within 15s."
    fi
    sleep 1
done

# FalkorDB (Redis protocol — PING/PONG)
for i in $(seq 1 15); do
    if echo "PING" | nc -w1 localhost 6379 2>/dev/null | grep -q "PONG"; then
        info "FalkorDB reachable on localhost:6379."
        break
    fi
    if [[ $i -eq 15 ]]; then
        fail "FalkorDB port-forward not reachable within 15s."
    fi
    sleep 1
done

# Gateway /healthz
for i in $(seq 1 30); do
    if curl -sf http://localhost:8080/healthz >/dev/null 2>&1; then
        info "Gateway healthy on localhost:8080."
        break
    fi
    if [[ $i -eq 30 ]]; then
        fail "Gateway /healthz did not respond within 30s."
    fi
    sleep 1
done

# Control-plane API /healthz
for i in $(seq 1 30); do
    if curl -sf http://localhost:9090/healthz >/dev/null 2>&1; then
        info "Control-plane API healthy on localhost:9090."
        break
    fi
    if [[ $i -eq 30 ]]; then
        fail "Control-plane API /healthz did not respond within 30s."
    fi
    sleep 1
done

# --- Step 4: Spawn test agent via CP API ---
info "Step 4: Spawning test agent '${TASK_NAME}' via control-plane API..."
PROMPT="You are a smoke test agent. Do exactly these three things and nothing else:
1. Respond with the word 'pong'
2. List every MCP server you can reach (use mcp_search or list your available tools and group by server)
3. State whether the fracta gateway is available and responding
Keep your response concise."

SPAWN_RESP=$(curl -sf -X POST http://localhost:9090/api/v1/agents \
    -H "Content-Type: application/json" \
    -d "{
        \"task\": \"${TASK_NAME}\",
        \"contract\": $(echo "$PROMPT" | jq -Rs .),
        \"tier\": \"light\",
        \"dispatch\": \"queued\"
    }" 2>&1) || fail "Spawn via CP API failed: ${SPAWN_RESP}"

info "Spawn response: ${SPAWN_RESP}"
info "Agent '$TASK_NAME' spawned via CP API. Waiting for K8s Job..."

# --- Step 5: Wait for Job completion ---
JOB_NAME="fracta-agent-${TASK_NAME}"
info "Step 5: Waiting for Job '${JOB_NAME}' (timeout 180s)..."

# Wait for the Job to appear first
for i in $(seq 1 30); do
    if kubectl get job "$JOB_NAME" -n "$NAMESPACE" &>/dev/null; then
        break
    fi
    if [[ $i -eq 30 ]]; then
        fail "Job '$JOB_NAME' was not created within 30s."
    fi
    sleep 1
done

kubectl wait --for=condition=complete job/"$JOB_NAME" -n "$NAMESPACE" --timeout=180s || {
    warn "Job did not complete. Checking status..."
    kubectl describe job "$JOB_NAME" -n "$NAMESPACE"
    kubectl logs job/"$JOB_NAME" -n "$NAMESPACE" --tail=50 || true
    fail "Job '$JOB_NAME' did not complete within 180s."
}

info "Job completed successfully."

# --- Step 6: Fetch and validate agent output ---
info "Step 6: Fetching agent output..."
AGENT_LOGS=$(kubectl logs job/"$JOB_NAME" -n "$NAMESPACE" 2>/dev/null || true)
if [[ -z "$AGENT_LOGS" ]]; then
    fail "Could not fetch agent logs."
fi
echo "---"
echo "$AGENT_LOGS"
echo "---"

# Validate the agent responded with "pong" as requested
if echo "$AGENT_LOGS" | grep -qi "pong"; then
    info "Agent output contains 'pong' — response validated."
else
    fail "Agent output does not contain 'pong'. The agent may not have executed correctly."
fi

# --- Step 7: Check postgres for durable events ---
info "Step 7: Querying agent_events in postgres..."
EVENTS=$(PGPASSWORD=fracta-dev-password psql -h localhost -U fracta -d fracta -t -c \
    "SELECT event_id, component, action, outcome, detail FROM agent_events WHERE task = '${TASK_NAME}' ORDER BY timestamp;" 2>/dev/null || true)

if [[ -n "$EVENTS" ]]; then
    info "Events found for task '${TASK_NAME}':"
    echo "$EVENTS"
else
    fail "No events found in postgres for task '${TASK_NAME}'. StoreSink or Postgres persistence is broken."
fi

# --- Step 8: Check Kubernetes Events for fracta-originated entries ---
info "Step 8: Checking Kubernetes Events for fracta-originated entries..."
K8S_EVENTS=$(kubectl get events -n "$NAMESPACE" --field-selector reason=JobCreated,source=fracta \
    --no-headers 2>/dev/null || true)

if [[ -z "$K8S_EVENTS" ]]; then
    # Fallback: search all recent events for fracta source
    K8S_EVENTS=$(kubectl get events -n "$NAMESPACE" --no-headers 2>/dev/null | grep -i "fracta" || true)
fi

if [[ -n "$K8S_EVENTS" ]]; then
    info "Kubernetes Events with fracta source found:"
    echo "$K8S_EVENTS"
else
    fail "No fracta-originated Kubernetes Events found. K8sEventSink may not be wired correctly."
fi

# --- Done ---
echo ""
info "========================================="
info "  Smoke test PASSED for '${TASK_NAME}'"
info "========================================="
