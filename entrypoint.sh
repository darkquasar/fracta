#!/bin/sh
set -e

# Install host-specific user settings.
# Priority: orchestrator-prepared file > auto-generated from fetch-bedrock-token
FRACTA_USER_SETTINGS="${PWD}/.fracta/user-settings.json"
if [ -f "$FRACTA_USER_SETTINGS" ]; then
  mkdir -p ~/.claude
  cp "$FRACTA_USER_SETTINGS" ~/.claude/settings.json
elif command -v fetch-bedrock-token >/dev/null 2>&1; then
  # No orchestrator-prepared settings (standalone spike / direct Job mode).
  # Generate minimal auth settings using the in-pod token helper.
  mkdir -p ~/.claude
  cat > ~/.claude/settings.json <<SETTINGS
{
  "apiKeyHelper": "/usr/local/bin/fetch-bedrock-token",
  "env": {
    "CLAUDE_CODE_USE_BEDROCK": "1",
    "CLAUDE_CODE_SKIP_BEDROCK_AUTH": "1",
    "CLAUDE_CODE_API_KEY_HELPER_TTL_MS": "60000",
    "AWS_REGION": "${AWS_REGION:-ap-southeast-2}",
    "ANTHROPIC_MODEL": "${ANTHROPIC_MODEL:-global.anthropic.claude-sonnet-4-6}"
  }
}
SETTINGS
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

exec "$@"
