# Swag (`swag`)

> Project page: **https://andy-swag.github.io/swag/**

A self-hosted, multi-machine Super Agent control deck and task scheduler built as a single Go binary (SQLite WAL + embedded zero-build web UI `web/` + `herdr` terminal integration + `yamux` outbound node tunnels).

## Architecture

- **Hub (always-on server)**: Runs `swag` (managed via `systemd` or `runit`), holds the authoritative `~/.swag/swag.db`, persistent memory (`~/.swag/router/memory/`), unified LLM provider failover pool (`/v1`), and event/todo control plane.
- **Nodes (laptops / remote compute boxes)**: Connect outbound to the Hub over `yamux` via `swag node` (no inbound port required on nodes) and optionally expose a local Web UI proxy (`http://127.0.0.1:17788`, installable as a desktop PWA).
- **Mobile & ChatOps**: Scan the Hub pairing QR code (`swag pair`) to open the responsive PWA, or connect Feishu/Lark interactive cards with WebSocket callback confirmation (`card.action.trigger`).

## Highlights

- **Multi-Machine Agent Fleet**: Spawn, steer, interrupt, and resume Claude Code, Codex, and Antigravity workers across multiple machines with machine groups, role profiles, and isolated auth profiles (`~/.swag/profiles.json`, `0600`).
- **Unified Inbox & Todo Center**: Server-computed Inbox triage (`需要你` / `已处理` / `全部`), one-click Inbox-to-Todo promotion, structured `swag todo` CLI, and live SSE badge sync.
- **Model Pool & Native Subscription Quota**: OpenAI-compatible `/v1/responses` & `/v1/chat/completions` failover proxy with tiered routing (`tier-fast`, `tier-standard`, `tier-strong`), cooldown recovery, native CLI subscription fallback, and cross-machine token usage analytics.
- **Persistent Memory & Dream**: Nightly memory compaction ("Dream") with cross-node read-only mirror sync (`~/.swag/memory-mirror/`).

## Repository Layout

```text
main.go              CLI and server entrypoint
internal/swag/       Hub, Node, Router/Worker orchestration, SQLite, memory, Dream, model pool, Lark, CLI
web/                 Embedded web UI (vanilla HTML/CSS/JS, zero build step)
templates/memory/    Starter memory templates (instructions, machine matrix, automation playbooks)
scripts/             Verification (verify.sh) and SSH hub deployment helper (deploy-devbox.sh)
```

## Build & Verify

```bash
# Run vet, race tests, architectural guardrails, and JS syntax checks
./scripts/verify.sh

# Build locally
CGO_ENABLED=0 go build -trimpath -o ~/.local/bin/swag .

# Cross-compile linux/amd64 and roll out to your Hub over SSH
SWAG_HUB_SSH=your-hub-host ./scripts/deploy-devbox.sh
```

## Common CLI Commands

```bash
swag pair                                 # Print a one-time phone pairing QR code
swag pair --computer                      # Print a one-time pairing command for another computer
swag list                                 # List connected machines, Router, and active workers
swag timer ls                             # List scheduled recurring/one-shot timers
swag timer add "weekdays 09:00" "..."     # Schedule a timer
swag todo add --title "..." --due "..."   # Add an item to the Todo center
swag todo ls                              # List open and completed todos
```

## License

[MIT](LICENSE)
