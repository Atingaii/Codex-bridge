# Code Map

This is the detailed "I want to change X, where do I edit?" source. Keep
`AGENTS.md` concise and link here for detail.

## Top-Level Shape

| Area | Files |
| --- | --- |
| CLI entry and subcommands | `main.go` |
| Config structs/load | `internal/config/config.go`, `internal/config/load.go`, `internal/config/duration.go` |
| Hub routes, auth, static serving | `internal/hub/server.go` |
| Browser chat WebSocket | `internal/hub/ws_browser.go` |
| Bridge reverse WebSocket | `internal/hub/ws_bridge.go`, `internal/bridge/client.go` |
| Browser/Bridge connection pools | `internal/hub/pool.go` |
| Orchestration HTTP/WS | `internal/hub/orchestration.go`, `internal/bridge/orchestration.go` |
| Runner abstraction | `internal/bridge/runner.go`, `internal/bridge/appserver_runner.go`, `internal/bridge/session.go` |
| SQLite schema and CRUD | `internal/store/store.go`, `internal/store/id.go` |
| Wire protocol | `internal/protocol/envelope.go` |
| Frontend source | `frontend/src/app/App.tsx`, `frontend/src/styles/` |
| Embedded frontend output | `internal/web/static/`, `internal/web/embed.go` |
| Android wrapper | `android/`, `frontend/capacitor.config.ts` |
| Deployment | `deploy/Caddyfile`, `deploy/systemd-*.service` |

## Common Tasks

### Add A Hub HTTP Endpoint

1. Add the route in `internal/hub/server.go:NewServer`.
2. Implement the handler in the relevant `internal/hub/*.go` file.
3. Add store methods if persistence is needed.
4. Add frontend caller in `frontend/src/app/App.tsx` when UI-visible.
5. Add or update a feature doc and tests.

### Change CLI Endpoint Management

1. `internal/hub/server.go:handleAgents` lists visible endpoints.
2. `internal/hub/server.go:handleDeleteAgent` soft-deletes an endpoint and
   disconnects its active Bridge connection.
3. `internal/hub/server.go:handleCreateAgentRepairToken` generates repair
   commands for existing endpoints.
4. `internal/store/store.go:DeleteAgent` owns agent soft deletion.
5. `frontend/src/app/App.tsx:SettingsModal` renders add/delete/detail/repair
   controls.
6. Update the relevant feature doc and tests.

### Add A WebSocket Frame

1. Define constants/payloads in `internal/protocol/envelope.go`.
2. Handle browser-originated frames in `internal/hub/ws_browser.go` or
   orchestration WS code.
3. Handle Bridge-originated frames in `internal/hub/ws_bridge.go`.
4. Handle Hub-to-Bridge frames in `internal/bridge/client.go`.
5. Update frontend parsing in `frontend/src/app/App.tsx`.
6. Add integration tests under `internal/integration/`.

### Change Chat Continuity

1. `internal/store.Session` and `UpdateSessionRemoteThreadByID` own the persisted
   Codex thread id.
2. `internal/hub/ws_browser.go:handleBrowserWS` sends that thread id in
   `open_session`.
3. `internal/bridge/session.go:Open` stores it per live session.
4. `internal/bridge/runner.go:args` switches to
   `codex exec resume <thread> -`.
5. `internal/hub/ws_bridge.go:handlePromptComplete` writes the new thread id
   after a prompt completes.

### Change Agent-Scoped Chat Sessions

1. `internal/store/store.go:Session` owns `AgentID`.
2. `internal/hub/server.go:handleCreateSession` creates sessions for the
   selected agent.
3. `frontend/src/app/App.tsx:Workspace` filters sessions by selected agent and
   stores per-agent active session ids in browser local storage.
4. Update
   [docs/features/agent-scoped-chat-sessions.md](features/agent-scoped-chat-sessions.md).

### Change Browser Approval Flow

1. `internal/protocol/envelope.go` defines `approval_request` and
   `approval_response`.
2. `internal/bridge/appserver_runner.go` maps Codex app-server approval requests
   to Bridge protocol frames.
3. `internal/bridge/orchestration.go:runCodexAppServer` reuses the app-server
   runner for review-required Codex orchestration and emits run-scoped approval
   frames.
4. `internal/bridge/orchestration.go:runClaude` maps Claude Code permission
   prompts through `codex-bridge claude-approval-mcp` and run-scoped approval
   frames.
5. `internal/bridge/session.go:ApprovalResponse` routes browser decisions back
   to the waiting runner.
6. `internal/bridge/orchestration.go:ApprovalResponse` routes run-scoped
   browser decisions back to the waiting Claude MCP or Codex app-server turn.
7. `internal/hub/ws_bridge.go:handleBridgeEnvelope` forwards Bridge approval
   requests to chat browsers by `sid` or orchestration browsers by
   `payload.runId`.
8. `internal/hub/orchestration.go:validateOrchestrationCapabilities` blocks
   review-required orchestration if the selected online Bridge does not report
   browser approval support for both CLIs.
9. `internal/hub/ws_browser.go:handleBrowserEnvelope` and
   `internal/hub/orchestration.go:handleOrchestrationWS` forward browser
   approval decisions back to the Bridge.
10. `frontend/src/app/App.tsx` renders capability status, approval cards, and
    approve/deny responses in chat and orchestration views.

### Change Orchestration Continuity

1. `internal/hub/orchestration.go:handleCreateOrchestration` creates a new run.
2. `internal/hub/orchestration.go:handleContinueOrchestration` appends a prompt
   to the same run and compacts previous events into context.
3. `internal/bridge/orchestration.go:run` executes turns
   using the prompt plus compacted context.
4. `frontend/src/app/App.tsx:OrchestrationWorkspace` must keep selecting the
   current run and call the continue endpoint for follow-up tasks.
5. Update [docs/features/orchestration-continuity.md](features/orchestration-continuity.md).

### Change Orchestration Strategy

1. `internal/bridge/orchestration.go:roleForTurn` controls which CLI owns each
   turn.
2. `internal/bridge/orchestration.go:composeOrchestrationPrompt` controls
   strategy instructions and the compact `Msg:` / `Handoff:` contracts.
3. `internal/bridge/orchestration.go:formatCompactPriorTurn` controls how much
   prior output is sent to the next CLI.
4. `internal/bridge/orchestration.go:parseHandoffFields` and
   `composeFinalVerifierPrompt` control structured handoff context and the
   conditional final verifier turn.
5. Keep event kinds compatible with `frontend/src/app/App.tsx:visibleOrchestrationEvents`.
6. Update
   [docs/features/orchestration-strategy-optimization.md](features/orchestration-strategy-optimization.md).

### Change SQLite Schema

1. Edit `internal/store.Store.Migrate`.
2. Update structs, scanners, and CRUD methods in `internal/store/store.go`.
3. Add store tests.
4. Update docs that describe persisted tables.

### Change Frontend UI

1. Edit `frontend/src/app/App.tsx` or styles.
2. Run `cd frontend && npm run build` so `internal/web/static/` is refreshed.
3. Run Go tests because embedded static tests cover expected assets.

### Change Config

1. Edit `internal/config/config.go`.
2. Bind env overrides in `internal/config/load.go`.
3. Update `configs/dev.yaml.example`.
4. Update `docs/dev-workflow.md`.

## Tests To Prefer

| Change | Useful tests |
| --- | --- |
| Store schema/CRUD | `/usr/local/go/bin/go test ./internal/store` |
| Hub auth/API | `/usr/local/go/bin/go test ./internal/hub` |
| Bridge runner/session | `/usr/local/go/bin/go test ./internal/bridge` |
| End-to-end Hub/Bridge flow | `/usr/local/go/bin/go test ./internal/integration` |
| Any shared change | `/usr/local/go/bin/go test ./...` |
