# Monolith File Split

> Status: Part A and Part B complete. Behavior-preserving refactor of the
> two largest source files. No protocol, persistence, API, or UI behavior
> change.

## Goals

- Make the two largest files navigable: `internal/bridge/orchestration.go`
  (3,199 lines, 122 funcs before Part A) and `frontend/src/app/App.tsx`
  (5,595 lines).
- Keep every change behavior-preserving and verifiable by existing tests / build.
- Reduce merge-conflict surface in the most-churned file (orchestration.go).

## Non-Goals

- No behavior, protocol (`internal/protocol.Envelope`), SQLite, HTTP, or WS change.
- No new packages for the Go side. Split into more files **within `package bridge`**
  so no currently-unexported symbol has to be exported. (Sub-packaging would force
  a wide export surface and is explicitly out of scope.)
- No new abstractions, interfaces, or indirection. Pure relocation of declarations.
- Not a rewrite of orchestration logic. The native-relay rework is tracked
  separately (see `docs/plan.md` Follow-up); this split only moves code.

## Part A — `internal/bridge/orchestration.go` (do first; low risk)

Completed on 2026-05-30 as a same-package split. `orchestration.go` now keeps
the manager/run core, while themed files own relay, Codex, Claude, event,
redaction, and profile helpers.

Split by theme into same-package files. Each function keeps its name and receiver;
only its file location changes. Go resolves intra-package references regardless of
file, so the only failure mode is a typo, which `go build` catches immediately.

| New file | Contents |
| --- | --- |
| `orchestration.go` | `OrchestrationManager` type, `Start`/`run`/`Cancel`/`CloseAll`, native-session map, approval routing, run-scoped state |
| `orchestration_relay.go` | relay loop, `runRelayCLI`, `composeRelayPromptWithFirstCLI`, `roleForTurnWithFirstCLI`, `formatRelayPriorTurn`, `relayTerminalContent` |
| `orchestration_codex.go` | `runCodexInteractive`, `runCodexAppServerWithThread`, `ensureCodexInteractiveSessionLocked`, Codex JSONL scan |
| `orchestration_claude.go` | `runClaude`, `runClaudeInteractive`, Claude stream-input, long-command nudge |
| `orchestration_events.go` | `emit`, `emitTool`, `run.conclusion`, turn/run fallback summaries |
| `orchestration_redact.go` | `stripANSI`, `redactSensitiveText`, redaction regex vars |
| `orchestration_profile.go` | `recordCommandFingerprints`, `profileCommands`, registry bridging |

`orchestration_test.go` (~2,030 lines) may be split to mirror these files in a
follow-up, but is not required for the source split to land.

### Exit gates (Part A)

- `go build ./...`, `go vet ./...`, `gofmt -l` clean
- `go test ./...` green (esp. `internal/bridge`, `internal/integration`)
- `go run golang.org/x/tools/cmd/deadcode@latest ./...` shows no new dead funcs
- `docs/code-map.md` anchors still resolve (`make doc-lint` 0/0)

## Part B — `frontend/src/app/App.tsx`

Completed on 2026-05-30. The root `App.tsx` now keeps only bootstrap/routing
state, while pages, components, API, i18n, shared types, and event helpers live
under focused files. The split is mechanical and keeps behavior unchanged.

Layout under `frontend/src/app/`:

| New file | Components |
| --- | --- |
| `App.tsx` | root router, bootstrap, top-level state only |
| `pages/Workspace.tsx` | `Workspace` (chat) |
| `pages/OrchestrationWorkspace.tsx` | `OrchestrationWorkspace` |
| `pages/PublicSharePage.tsx` | `PublicSharePage` |
| `pages/ConversationSnapshotPage.tsx` | snapshot page + items |
| `components/Settings.tsx` | `SettingsModal`, `RenameSessionModal` |
| `components/OrchestrationComponents.tsx` | `CapabilityMatrix`, `OrchestrationEventItem`, `CommandEvent`, marks |
| `components/OrchestrationFiles.tsx` | orchestration attachment rows/lists |
| `components/SidebarContent.tsx` | chat sidebar |
| `components/AgentSelector.tsx` | endpoint selector |
| `components/chat/*` | `MessageItem`, `ToolItem`, `MessageContent`, `ApprovalCard`, `CommandBlock` |
| `components/ui.tsx` | `Button`, `Input` |
| `lib/api.ts`, `lib/types.ts`, `lib/i18n.ts`, `lib/utils.ts` | API client, shared types, `UIText`, behavior-preserving helpers/reducers |

### Exit gates (Part B)

- `cd frontend && npm run build` regenerates `internal/web/static/` with no errors
- `go test ./...` green (embedded-static tests assert expected assets)
- Browser smoke: login, chat send/resume, orchestration run, settings add/repair
  endpoint, share page, snapshot page — all render and function as before

## Sequencing

1. **Part A first**, ideally folded into the start of the native-relay rework so
   the two passes do not churn the same functions twice. Low risk; fully test-covered.
2. **Part B separately**, as a dedicated frontend pass with the browser smoke above.
   Higher risk; cannot be validated by `go test` alone.

## Reviewer Q&A

**Q: Why same-package files for Go instead of real sub-packages?**
A: Sub-packages would require exporting ~100 currently-unexported helpers, widening
the API surface and inviting accidental external use. Same-package files give the
navigability win at zero API cost.

**Q: How is "behavior-preserving" guaranteed?**
A: Go relocation cannot change semantics; the compiler resolves intra-package refs
by symbol, not file. The existing `internal/bridge` + `internal/integration` suites
(which exercise the live relay, resume, and approval paths) are the safety net.

**Q: Why not split `orchestration_test.go` at the same time?**
A: It can follow once the source files are stable; splitting tests first would just
create churn against moving targets.
