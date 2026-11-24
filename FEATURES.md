# Swag Feature Map

Executable map of Swag's core capabilities, boundaries, and verification commands.

| Feature | Backend Boundary | Frontend Entry | Automated / Runtime Check |
| --- | --- | --- | --- |
| **Multi-Machine Fleet & Tunnel** | `internal/swag/hub.go`, `node.go`, `fleet.go` | `web/app.js` (`#v-devices`) | `go test -race ./...` · `swag list` |
| **Machine Dispatch Pool & Groups** | `store.go` (`MachinePolicy`, `PolicyFor`), `super.go` (`SpawnWithOptions`) | `web/app.js` (`#mach-policies`) | `TestMachinePolicyDefaults` |
| **Role & Auth Profiles (AK)** | `store.go` (`RoleProfile`), `profiles.go` (`~/.swag/profiles.json`) | `web/app.js` (`#settings-form`) | `TestBuildEnvExportCmd` · `TestArchSingleAgentStartPath` |
| **Unified Agent Launch & Pre-Trust** | `server.go` (`startAgentWithProfile`), `trust.go` (`trustAgyFolder`, `trustClaudeBypass`) | `web/app.js` (`#t-model`, `#d-head`) | `TestArchSingleAgentStartPath` · `trust_test.go` |
| **Timers (Machine / Group / Role)** | `store.go` (`Timer`), `super.go` (`tick`), `superapi.go` (`superAddTimer`) | `web/app.js` (`#timer-form`) | `swag timer ls` |
| **Remote Dream & Memory Mirror** | `dream.go` (`StartDream`, `finishDream`), `sync.go` (`SyncMemoryMirrors`) | `web/app.js` (`data-act="sync-mirror"`, `#x-dream-pin`) | `TestDirMirrorSyncAndPrune` · `ls ~/.swag/memory-mirror/` |
| **Feishu/Lark Notifications (Proxy)** | `lark.go`, `sync.go` (`larkNotify`, `larkStatus`) | `web/app.js` (`data-act="lark-test"`) | `TestArchSingleLarkSendPath` · `lark_test.go` |
| **External Sessions (`tmux`/Desktop)** | `external.go` (`locateExternal`), `desktop.go` | `web/app.js` (`#v-ext`) | `TestLocateExternalCodexResumeSameCwd` |
| **Token & Usage Dashboard** | `usage.go` (`scanLocalUsage`, `superUsage`, `nodeUsage`) | `web/panels.js` (`renderUsage`) | `TestScanCodexAndClaudeUsage` · `GET /api/super/usage` |
| **Native Quota & Multi-Provider Pool** | `quota.go` (`superQuota`, `nodeQuota`), `pool.go` (`poolResponses`, `superPoolProbe`) | `web/panels.js` (`renderPool`) | `TestParseCodexLatestQuota` · `TestPoolFailoverAndCooldown` |
| **Recent Session Archive** | `usage.go` (`superArchive`), `store.go` (`ArchivedSessions`) | `web/app.js` (`#x-archive`, `.side-arch`) | `POST /api/super/archive` |
| **Codebase Architecture Guardrails** | `internal/swag/arch_test.go` | `scripts/verify.sh` | `./scripts/verify.sh --live` |
