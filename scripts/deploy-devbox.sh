#!/usr/bin/env bash
# Cross-compile linux/amd64 and roll out Swag Hub over SSH without interrupting running herdr panes.
set -euo pipefail

export PATH="/usr/local/go/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

HOST="${SWAG_HUB_SSH:?Set SWAG_HUB_SSH to your Hub SSH host (e.g. SWAG_HUB_SSH=my-hub ./scripts/deploy-devbox.sh)}"
VERSION="${SWAG_VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
OUT="$ROOT_DIR/dist/swag-linux-amd64"

echo "==> [1/3] Building linux/amd64 ($VERSION)..."
mkdir -p "$ROOT_DIR/dist"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$OUT" .

echo "==> [2/3] Uploading binary to $HOST:~/.local/bin/swag ..."
gzip -c "$OUT" | ssh "$HOST" 'mkdir -p ~/.local/bin && gunzip > ~/.local/bin/swag.new && chmod 755 ~/.local/bin/swag.new && mv -f ~/.local/bin/swag.new ~/.local/bin/swag'

echo "==> [3/3] Restarting Swag Hub on $HOST (herdr panes stay alive)..."
ssh "$HOST" 'if systemctl --user is-enabled swag-hub.service >/dev/null 2>&1; then systemctl --user restart swag-hub.service && sleep 1 && systemctl --user is-active swag-hub.service; elif [ -d /etc/service/swag-hub ]; then sudo sv restart /etc/service/swag-hub && sleep 1 && sudo sv status /etc/service/swag-hub; else echo "swag-hub service not found"; fi'
echo "Done."
