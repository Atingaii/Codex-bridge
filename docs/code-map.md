# Code Map

This is the detailed "I want to change X, where do I edit?" source. Keep
`AGENTS.md` concise and link here for detail.

## Top-Level Shape

| Area | Files |
| --- | --- |
| CLI entry and subcommands | `main.go` |
| Config structs/load | `internal/config/config.go`, `internal/config/load.go`, `internal/config/duration.go` |
| Hub routes, auth, static serving | `internal/hub/server.go` |
| Public conversation shares | `internal/hub/share.go` |
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

### Change Conversation Share Links

1. `internal/store/store.go:ConversationShare` owns share persistence and
   revocation.
2. `internal/hub/share.go` owns protected share creation, revocation, and
   public read sanitization.
3. `internal/hub/server.go:NewServer` registers `/api/*/share`,
   `/api/shares/{shareID}`, and `/api/public/shares/{shareID}`.
4. For orchestration shares,
   `internal/hub/share.go:publicOrchestrationEvents` strips Bridge-internal
   severity events, `TurnStartData.PromptText`, and private Bridge-note fields
   while preserving public lifecycle and `run.conclusion` events.
5. `frontend/src/app/App.tsx:PublicSharePage` renders `/share/{shareID}`
   before login bootstrap.
6. Update [docs/features/conversation-share-links.md](features/conversation-share-links.md).

### Change CLI Endpoint Management

1. `internal/hub/server.go:handleAgents` lists visible endpoints.
2. `internal/hub/server.go:handleDeleteAgent` soft-deletes an endpoint and
   sends `agent_shutdown` before disconnecting its active Bridge connection.
3. `internal/hub/server.go:handleCreateAgentRepairToken` generates repair
   commands for existing endpoints.
4. `internal/bridge/client.go:connectOnce` sends live `workingDirs` in Bridge
   heartbeat payloads, and `internal/hub/ws_bridge.go:handleBridgeEnvelope`
   stores them through `internal/store/store.go:TouchAgentWorkingDirs`.
5. `internal/bridge/client.go:requestShutdown` handles remote endpoint
   shutdown and local user-service cleanup.
6. `internal/store/store.go:DeleteAgent` owns agent soft deletion.
7. `frontend/src/app/App.tsx:SettingsModal` renders add/delete/detail/repair
   controls.
8. Update the relevant feature doc and tests.

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
   orchestration if the selected online Bridge cannot execute both CLIs, and
   additionally blocks review-required orchestration if browser approval support
   is missing.
9. `internal/hub/ws_browser.go:handleBrowserEnvelope` and
   `internal/hub/orchestration.go:handleOrchestrationWS` forward browser
   approval decisions back to the Bridge.
10. `frontend/src/app/App.tsx` renders capability status, approval cards, and
    approve/deny responses in chat and orchestration views.

### Change Orchestration Continuity

1. `internal/hub/orchestration.go:handleCreateOrchestration` creates a new run.
2. `internal/hub/orchestration.go:handleContinueOrchestration` appends a prompt
   to the same run, preserves or updates persisted settings such as `firstCli`,
   and compacts previous events into context.
3. `internal/hub/orchestration.go:startOrchestration` restores saved Codex
   thread id, Claude-started state, and locked run cwd into
   `internal/protocol.OrchestrationStartPayload` for resumed runs.
4. `internal/hub/orchestration.go:handleOrchestrationEvent` persists those
   native CLI continuity fields from `run.start` and `turn.end` events.
5. `internal/bridge/orchestration.go:run` executes turns using the prompt plus
   compacted context, restored CLI state, and locked cwd.
6. `frontend/src/app/App.tsx:OrchestrationWorkspace` must keep selecting the
   current run and call the continue endpoint for follow-up tasks.
7. `internal/hub/orchestration.go:startOrchestration` attaches uploaded file
   metadata to `user.message` events, and
   `frontend/src/app/App.tsx:OrchestrationEventItem` renders those files.
8. Update [docs/features/orchestration-continuity.md](features/orchestration-continuity.md).

### Change Orchestration Strategy

1. `internal/hub/orchestration.go:normalizeOrchestrationFirstCLI`,
   `internal/store/store.go:OrchestrationRun`, and
   `internal/protocol.OrchestrationStartPayload` carry the persisted first-turn
   CLI selection.
2. `internal/hub/orchestration.go:normalizeOrchestrationProfile`,
   `internal/store/store.go:normalizeOrchestrationProfile`,
   `internal/protocol.OrchestrationStartPayload`, and
   `frontend/src/app/App.tsx:OrchestrationWorkspace` carry the persisted
   orchestration profile (`default` or `formal-proof`).
3. `internal/bridge/orchestration.go:roleForTurnWithFirstCLI` controls which
   CLI owns each turn.
4. `internal/bridge/orchestration.go:composeRelayPromptWithFirstCLI` controls
   the pass-through prompt sent to Claude/Codex. Only
   `profile=formal-proof` enables formal-proof prompt guidance; the default
   profile does not silently activate it from prompt keywords.
5. `internal/bridge/profiles/registry` is the neutral boundary for
   profile-specific orchestration behavior. Formal-proof prompt fragments,
   assessments, manual-build carry-over, command fingerprint decisions, and
   benchmark-specific detectors live under
   `internal/bridge/profiles/formalproof/`.
6. `internal/bridge/orchestration.go:formatRelayPriorTurn` controls how much
   prior visible output and command context is sent to the next CLI.
7. `internal/bridge/orchestration.go:runRelayCLI` preserves the per-run Claude
   session id and Codex thread id when launching the next CLI turn.
8. `internal/bridge/orchestration.go:cwd` locks resumed runs to the absolute
   run cwd reported by Bridge, and
   `internal/bridge/orchestration.go:PrepareOrchestrationPromptFiles` writes
   uploaded files under that cwd.
9. `internal/bridge/orchestration.go:relayTerminalContent` controls terminal
   run content without adding a hidden verifier or remediation turn.
10. Keep event kinds compatible with `frontend/src/app/App.tsx:visibleOrchestrationEvents`,
   including terminal run summary rendering for `run.end` / `run.error`.
11. Update
   [docs/features/orchestration-pass-through-cli.md](features/orchestration-pass-through-cli.md).

### Change Orchestration Event Protocol

1. `internal/protocol/envelope.go:OrchestrationEventPayload` defines the event
   contract, including `Source`, `Severity`, typed sub-payloads, and
   `RunConclusion`.
2. `internal/bridge/orchestration.go:emit` normalizes source/severity and
   emits exactly one `run.conclusion` before terminal run events.
3. `internal/bridge/orchestration.go:emitTool` maps `RunnerToolEvent` into
   typed `CommandData`; frontend command cards must use `commandData`, not
   free-form `data` keys.
4. `internal/store/store.go:AddOrchestrationEvent` persists typed event
   payloads and keeps legacy `Data` compatibility for older rows.
5. `internal/hub/orchestration.go:handleOrchestrationEvent` persists the
   Bridge event and updates run continuity fields from typed payloads.
6. `frontend/src/app/App.tsx:visibleOrchestrationEvents` renders events using
   `source`, `severity`, `commandData`, and `runConclusion`.
7. `internal/hub/share.go:publicOrchestrationEvents` is the public transcript
   sanitizer for typed orchestration events.
8. Update [docs/features/orchestration-event-protocol-hardening(1).md](features/orchestration-event-protocol-hardening(1).md).

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
4. For `bridge.long_command_observer`, keep YAML fields, env overrides, and
   `internal/bridge/orchestration.go:longCommandObserverConfig` defaults in
   sync.
5. Update `docs/dev-workflow.md`.

## Tests To Prefer

| Change | Useful tests |
| --- | --- |
| Store schema/CRUD | `/usr/local/go/bin/go test ./internal/store` |
| Hub auth/API | `/usr/local/go/bin/go test ./internal/hub` |
| Bridge runner/session | `/usr/local/go/bin/go test ./internal/bridge` |
| End-to-end Hub/Bridge flow | `/usr/local/go/bin/go test ./internal/integration` |
| Any shared change | `/usr/local/go/bin/go test ./...` |
