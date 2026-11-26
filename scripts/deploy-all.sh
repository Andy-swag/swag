#!/usr/bin/env bash
# Build and roll out Swag to local macOS node (optional), Hub, and remote Linux nodes in parallel
# without interrupting running herdr panes.
#
# Example:
#   SWAG_VERSION=v0.8.0 SWAG_HUB_SSH=my-hub SWAG_NODE_SSHS="node1 node2" ./scripts/deploy-all.sh
set -euo pipefail

export PATH="/usr/local/go/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

VERSION="${SWAG_VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
HUB_HOST="${SWAG_HUB_SSH:-}"
NODE_HOSTS="${SWAG_NODE_SSHS:-}"
DO_MAC="${SWAG_LOCAL_MAC:-1}"

mkdir -p "$ROOT_DIR/dist"
LINUX_BIN="$ROOT_DIR/dist/swag-linux-amd64"
DARWIN_BIN="$ROOT_DIR/dist/swag-darwin-arm64"

echo "==> Building binaries ($VERSION)..."
if [ -n "$HUB_HOST" ] || [ -n "$NODE_HOSTS" ]; then
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$LINUX_BIN" . &
  PID_LINUX=$!
fi
if [ "$DO_MAC" = "1" ] && [ "$(uname -s)" = "Darwin" ]; then
  CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$DARWIN_BIN" . &
  PID_DARWIN=$!
fi
[ -n "${PID_LINUX:-}" ] && wait "$PID_LINUX"
[ -n "${PID_DARWIN:-}" ] && wait "$PID_DARWIN"

PIDS=()
if [ "$DO_MAC" = "1" ] && [ "$(uname -s)" = "Darwin" ]; then
  (
    echo "==> [mac] Installing $VERSION..."
    mkdir -p "$HOME/.local/bin"
    rm -f "$HOME/.local/bin/swag"
    cp "$DARWIN_BIN" "$HOME/.local/bin/swag"
    chmod 755 "$HOME/.local/bin/swag"
    codesign --force --sign - "$HOME/.local/bin/swag" >/dev/null 2>&1 || true
    launchctl kickstart -k "gui/$(id -u)/com.swag.node" >/dev/null 2>&1 || true
    echo "==> [mac] $("$HOME/.local/bin/swag" version)"
  ) &
  PIDS+=($!)
fi

if [ -n "$HUB_HOST" ]; then
  (
    echo "==> [hub:$HUB_HOST] Uploading $VERSION..."
    gzip -c "$LINUX_BIN" | ssh -o ConnectTimeout=15 "$HUB_HOST" 'mkdir -p ~/.local/bin && gunzip > ~/.local/bin/swag.new && chmod 755 ~/.local/bin/swag.new && mv -f ~/.local/bin/swag.new ~/.local/bin/swag'
    ssh -o ConnectTimeout=15 "$HUB_HOST" 'if systemctl --user is-enabled swag-hub.service >/dev/null 2>&1; then systemctl --user restart swag-hub.service && sleep 1 && systemctl --user is-active swag-hub.service; elif [ -d /etc/service/swag-hub ]; then sudo sv restart /etc/service/swag-hub && sleep 1 && sudo sv status /etc/service/swag-hub; fi; ~/.local/bin/swag version' | sed "s/^/==> [hub:$HUB_HOST] /"
  ) &
  PIDS+=($!)
fi

for nh in $NODE_HOSTS; do
  (
    echo "==> [node:$nh] Uploading $VERSION..."
    gzip -c "$LINUX_BIN" | ssh -o ConnectTimeout=15 "$nh" 'mkdir -p ~/.local/bin && gunzip > ~/.local/bin/swag.new && chmod 755 ~/.local/bin/swag.new && mv -f ~/.local/bin/swag.new ~/.local/bin/swag'
    ssh -o ConnectTimeout=15 "$nh" 'if [ -d /etc/service/swag-node ]; then sudo sv restart /etc/service/swag-node && sleep 1 && sudo sv status /etc/service/swag-node; elif systemctl --user is-enabled swag-node.service >/dev/null 2>&1; then systemctl --user restart swag-node.service && sleep 1 && systemctl --user is-active swag-node.service; fi; ~/.local/bin/swag version' | sed "s/^/==> [node:$nh] /"
  ) &
  PIDS+=($!)
done

FAIL=0
for p in "${PIDS[@]}"; do
  wait "$p" || FAIL=1
done
[ "$FAIL" = "0" ] || exit 1
echo "==> All targets deployed ($VERSION)."
