#!/usr/bin/env bash
set -euo pipefail

export PATH="/usr/local/go/bin:/opt/homebrew/bin:/usr/local/bin:$HOME/.local/bin:$PATH"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

echo "==> [1/3] go vet ./..."
go vet ./...

echo "==> [2/3] go test -race ./... (includes AST architectural guardrails)"
go test -race ./...

echo "==> [3/3] node --check web/app.js web/panels.js"
node --check web/app.js
node --check web/panels.js

if [[ "${1:-}" == "--live" && -n "${SWAG_HUB_URL:-}" ]]; then
  echo "==> [live] verifying Hub & local UI + memory mirror"
  curl -fsS -o /dev/null -w "hub       /         -> HTTP %{http_code}\n" "${SWAG_HUB_URL%/}/"
  curl -fsS -o /dev/null -w "hub       /icon.svg -> HTTP %{http_code}\n" "${SWAG_HUB_URL%/}/icon.svg"
  curl -fsS -o /dev/null -w "local     /icon.svg -> HTTP %{http_code}\n" http://127.0.0.1:17788/icon.svg
  test -f "$HOME/.swag/memory-mirror/instructions.md" && echo "local memory-mirror -> OK"
fi

echo "ALL CHECKS PASSED"
