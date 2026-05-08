#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$REPO_ROOT/bin/fracta"
OUT="$REPO_ROOT/docs/reference/cli"

if [[ ! -x "$BIN" ]]; then
  echo "fracta binary not found at $BIN — run 'go build -o bin/fracta .' first" >&2
  exit 1
fi

mkdir -p "$OUT"

COMMANDS="auth controlplane graph host-mcp init kill list mcp merge peek registry say serve spawn watch worker"

for cmd in $COMMANDS; do
  short="$("$BIN" "$cmd" --help 2>&1 | sed -n '1p' | sed 's/^[[:space:]]*//')"
  cat > "$OUT/$cmd.md" <<EOF
---
title: fracta $cmd
description: ${short:-Command reference for fracta $cmd}
---

# fracta $cmd

\`\`\`
$("$BIN" "$cmd" --help 2>&1)
\`\`\`
EOF
done

echo "wrote $(echo $COMMANDS | wc -w | tr -d ' ') CLI reference pages to $OUT"
