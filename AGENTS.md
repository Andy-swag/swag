# Swag Agent Guide

Keep this file short. Put constraints in code, types, and `internal/swag/arch_test.go` first—not in prose.

## Mandatory Verification
Before finishing any change, run:
```bash
./scripts/verify.sh
```
For changes deployed to live machines, also run `./scripts/verify.sh --live`.

## Architectural Rules (Enforced Mechanically by `arch_test.go`)
1. **Single agent launch path**: Launch herdr agents only via `Server.startAgentWithProfile` (`server.go`). Never call `herdr.Call(..., "agent.start", ...)` elsewhere.
2. **Single Lark send path**: Send Feishu notifications only via `Server.larkNotify` (`sync.go`) so remote `LarkMachine` proxying is preserved.
3. **No workaround-excuse comments**: Never write `TODO`/`FIXME`/`HACK`/`XXX`/`workaround`/`for now` comments. Fix the design or encode the rule in `arch_test.go`.
4. **No new raw `time.Sleep`**: Use `sleepCtx(ctx, d)` or readiness signals.
5. **No monolith growth**: Put new cohesive features in dedicated files (like `profiles.go`, `sync.go`, `trust.go`) instead of expanding `super.go` or `web/app.js`.
6. **Zero legacy branding**: Keep all identifiers, UI copy, and docs strictly branded `Swag` (`swag`).
7. **Never log secrets**: Never print tokens, cookies, or `profiles.json` values.

See `FEATURES.md` for the feature-to-code-and-check map.
