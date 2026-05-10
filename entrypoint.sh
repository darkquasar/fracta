#!/bin/sh
set -e

# Auth helpers — workspace-local first (per-agent overrides), then image-baked.
# Left-most dir on PATH wins; workspace shadows image (spec-42 §6, Rule 3).
export PATH="${PWD}/.fracta/auth-helpers:/opt/fracta/auth-helpers:${PATH}"

# Install host-specific user settings. The orchestrator is the SOLE source of
# settings.json — no in-pod fallbacks (spec-42 Rule 2). Pods without an
# orchestrator-prepared file run unauthenticated; the agent's first auth-bearing
# call fails authoritatively.
FRACTA_USER_SETTINGS="${PWD}/.fracta/user-settings.json"
if [ -f "$FRACTA_USER_SETTINGS" ]; then
  mkdir -p ~/.claude
  cp "$FRACTA_USER_SETTINGS" ~/.claude/settings.json
fi

# Start the Python strategy sidecar if the runner exists AND we're not in
# external socket mode (K8s sidecar container manages the runner process).
STRATEGY_DIR="${FRACTA_STRATEGY_DIR:-/opt/fracta/strategies}"
RUNNER="${STRATEGY_DIR}/runner.py"
SIDECAR_LOG="${FRACTA_SIDECAR_LOG:-/var/log/fracta-strategy.log}"
if [ -f "$RUNNER" ] && [ -z "$FRACTA_STRATEGY_EXTERNAL" ]; then
  uv run --project "$STRATEGY_DIR" python "$RUNNER" \
    --socket /tmp/fracta-strategy.sock --strategy-dir "$STRATEGY_DIR" \
    >"$SIDECAR_LOG" 2>&1 &
  for i in $(seq 1 10); do
    [ -S /tmp/fracta-strategy.sock ] && break
    sleep 0.5
  done
fi

# Diagnostic line: tells the operator at a glance whether the orchestrator
# handed off settings and which auth helpers are visible (R1 mitigation).
present=false
[ -f "$FRACTA_USER_SETTINGS" ] && present=true
helpers=""
[ -d "${PWD}/.fracta/auth-helpers" ] && helpers=$(ls "${PWD}/.fracta/auth-helpers" 2>/dev/null)
[ -d /opt/fracta/auth-helpers ] && helpers="${helpers}${helpers:+ }$(ls /opt/fracta/auth-helpers 2>/dev/null)"
helpers=$(printf '%s\n' "$helpers" | tr ' ' '\n' | sort -u | paste -sd, -)
echo "fracta: user-settings.json present=${present}; auth-helpers=[${helpers:-none}]" >&2

exec "$@"
