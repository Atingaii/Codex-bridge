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
| Orchestration HTTP/WS | `internal/hub/orchestration.go`, `internal/bridge/orchestration*.go` |
| Runner abstraction | `internal/bridge/runner.go`, `internal/bridge/appserver_runner.go`, `internal/bridge/acp_runner.go`, `internal/bridge/acp_client.go`, `internal/bridge/session.go` |
| SQLite schema and CRUD | `internal/store/store.go`, `internal/store/id.go` |
| Wire protocol | `internal/protocol/envelope.go` |
| Frontend source | `frontend/src/app/App.tsx`, `frontend/src/app/pages/`, `frontend/src/app/components/`, `frontend/src/app/lib/`, `frontend/src/styles/` |
| Embedded frontend output | `internal/web/static/`, `internal/web/embed.go` |
| Android wrapper | `android/`, `frontend/capacitor.config.ts` |
| Deployment | `deploy/Caddyfile`, `deploy/systemd-*.service`, `Dockerfile`, `Makefile`, `docs/deployment.md` |

## Common Tasks

### Add A Hub HTTP Endpoint

1. Add the route in `internal/hub/server.go:NewServer`.
2. Implement the handler in the relevant `internal/hub/*.go` file.
3. Add store methods if persistence is needed.
4. Add frontend caller in the relevant `frontend/src/app/pages/` or
   `frontend/src/app/components/` file when UI-visible.
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
5. `frontend/src/app/App.tsx:App` routes `/share/{shareID}` before login
   bootstrap, and `frontend/src/app/pages/PublicSharePage.tsx:PublicSharePage`
   renders it.
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
7. `frontend/src/app/components/Settings.tsx:SettingsModal` renders
   add/delete/detail/repair controls.
8. Update the relevant feature doc and tests.

### Add A WebSocket Frame

1. Define constants/payloads in `internal/protocol/envelope.go`.
2. Handle browser-originated frames in `internal/hub/ws_browser.go` or
   orchestration WS code.
3. Handle Bridge-originated frames in `internal/hub/ws_bridge.go`.
4. Handle Hub-to-Bridge frames in `internal/bridge/client.go`.
5. Update frontend parsing in the relevant page under
   `frontend/src/app/pages/` or helper under `frontend/src/app/lib/`.
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
6. `internal/hub/browser_lease.go:startBrowserLease` keeps a disconnected chat
   `sid` in `leaseIdleLeased` until `hub.browser_lease_ttl` expires, and
   `internal/hub/browser_lease.go:tryReattach` cancels that timer when the same
   `sid` reconnects.
7. `internal/bridge/session.go:Open` is the Bridge-side reattach
   point: if the `sid` already exists, it updates the output channel and reuses
   the existing runner/session instead of spawning a new process.

### Change Agent-Scoped Chat Sessions

1. `internal/store/store.go:Session` owns `AgentID`.
2. `internal/hub/server.go:handleCreateSession` creates sessions for the
   selected agent.
3. `frontend/src/app/pages/Workspace.tsx:Workspace` filters sessions by
   selected agent and stores per-agent active session ids in browser local
   storage.
4. Update
   [docs/features/agent-scoped-chat-sessions.md](features/agent-scoped-chat-sessions.md).

### Change ACP Runner / Resident Session Chat

1. `internal/bridge/runner.go:SessionRunner` extends the one-shot `Runner`
   with `OpenSession`/`Resume`/`PromptSession`/`CloseSession` for resident
   adapter processes, and `internal/bridge/runner.go:NewRunner` wires the
   `"acp"` runner.
2. `internal/bridge/acp_client.go:startACPClient` is a bidirectional stdio
   JSON-RPC client (responses, agent→client requests, notifications).
3. `internal/bridge/acp_runner.go:OpenSession` starts/reuses the resident ACP
   adapter, runs `initialize` + `session/new`/`session/load`, and resolves the
   dual-ID model via `internal/bridge/acp_runner.go:resolveNativeResumeID`
   (Claude: ACP sessionId equals the native `.jsonl` UUID; Codex: prefer ACP
   id, else scan `~/.codex/sessions/`).
4. `internal/bridge/session.go:Prompt` dispatches to `SessionRunner` when the
   runner implements it (else falls back to one-shot `Runner.Prompt`) and emits
   `NativeResumeID`/`NativeResumeCommand` in `prompt_complete`.
5. `internal/protocol/envelope.go:ACPCapability` advertises adapter
   availability and native-resume support to the Hub/browser.
6. `main.go:preflightRunner` validates the selected ACP adapter command at
   `connect` time.
7. Honesty rule: report degradation truthfully when the adapter is missing, the
   native id cannot be resolved, or cwd mismatches — never fabricate a takeover
   command.
8. `frontend/src/app/pages/Workspace.tsx:Workspace` reads the optional
   `nativeResumeId`/`nativeResumeCommand` from `session_opened`/`prompt_complete`,
   persists the id on the session record, and renders
   `frontend/src/app/components/chat/TakeoverHint.tsx:TakeoverHint` (reusing
   `frontend/src/app/components/chat/CommandBlock.tsx:CommandBlock`) plus an ACP
   badge. `frontend/src/app/lib/types.ts:ACPCapability` mirrors the protocol
   capability.
9. After any frontend change, run `npm run build` in `frontend/` to regenerate
   the embedded `internal/web/static` output and commit it.
10. See [docs/features/acp-runner.md](features/acp-runner.md) and
    [docs/features/acp-runner-pr2.md](features/acp-runner-pr2.md).

### Change Browser Approval Flow

1. `internal/protocol/envelope.go` defines `approval_request` and
   `approval_response`.
2. `internal/bridge/appserver_runner.go` maps Codex app-server approval requests
   to Bridge protocol frames.
3. `internal/bridge/orchestration_codex.go:runCodexAppServerWithThread` reuses the app-server
   runner for review-required Codex orchestration and emits run-scoped approval
   frames.
4. `internal/bridge/orchestration_claude.go:runClaude` maps Claude Code permission
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
10. `frontend/src/app/components/OrchestrationComponents.tsx:CapabilityMatrix`,
    `frontend/src/app/components/chat/ApprovalCard.tsx:ApprovalCard`,
    `frontend/src/app/pages/Workspace.tsx:Workspace`, and
    `frontend/src/app/pages/OrchestrationWorkspace.tsx:OrchestrationWorkspace`
    render capability status, approval cards, and approve/deny responses in
    chat and orchestration views.

### Change Orchestration Continuity

1. `internal/hub/orchestration.go:handleCreateOrchestration` creates a new run.
2. `internal/hub/orchestration.go:handleContinueOrchestration` appends a prompt
   to the same run, preserves or updates persisted settings such as
   `workerPair` and `firstCli`, and compacts previous events into context.
3. `internal/hub/orchestration.go:startOrchestration` restores saved Codex
   thread id(s), Claude-started state, and locked run cwd into
   `internal/protocol.OrchestrationStartPayload` for resumed runs.
4. `internal/hub/orchestration.go:handleOrchestrationEvent` persists those
   native CLI continuity fields from `run.start`, `turn.end`, and `run.end`
   events.
5. `internal/bridge/orchestration.go:run` executes turns using the prompt plus
   compacted context, restored CLI state, and locked cwd.
6. `frontend/src/app/pages/OrchestrationWorkspace.tsx:OrchestrationWorkspace`
   must keep selecting the current run and call the continue endpoint for
   follow-up tasks.
7. `internal/hub/orchestration.go:startOrchestration` attaches uploaded file
   metadata to `user.message` events, and
   `frontend/src/app/components/OrchestrationComponents.tsx:OrchestrationEventItem`
   renders those files.
8. Update [docs/features/orchestration-continuity.md](features/orchestration-continuity.md).

### Change Orchestration Strategy

1. `internal/protocol/envelope.go:NormalizeOrchestrationWorkerPair`,
   `internal/hub/orchestration.go:normalizeOrchestrationStart`,
   `internal/store/store.go:OrchestrationRun`, and
   `frontend/src/app/pages/OrchestrationWorkspace.tsx:OrchestrationWorkspace`
   carry the persisted worker pair (`claude-codex` or `codex-codex`).
2. `internal/hub/orchestration.go:normalizeOrchestrationFirstCLI`,
   `internal/store/store.go:OrchestrationRun`, and
   `internal/protocol.OrchestrationStartPayload` carry the persisted first-turn
   CLI selection. Codex + Codex normalizes this to Codex.
3. `internal/hub/orchestration.go:normalizeOrchestrationProfile`,
   `internal/store/store.go:normalizeOrchestrationProfile`,
   `internal/protocol.OrchestrationStartPayload`, and
   `frontend/src/app/pages/OrchestrationWorkspace.tsx:OrchestrationWorkspace`
   carry the persisted orchestration profile (`default` or `formal-proof`).
4. `internal/protocol/envelope.go:NormalizeNativeContextCompaction`,
   `internal/store/store.go:OrchestrationRun`,
   `internal/hub/orchestration.go:normalizeOrchestrationStart`, and
   `frontend/src/app/pages/OrchestrationWorkspace.tsx:OrchestrationWorkspace`
   carry the persisted native context compaction preference (`off` or
   `after-turn`).
5. `internal/bridge/orchestration_relay.go:roleForTurnWithWorkerPair` controls
   which CLI and worker slot owns each turn.
6. `internal/bridge/orchestration_relay.go:composeRelayPromptWithWorkerSlot`
   controls the pass-through prompt sent to Claude/Codex. Only
   `profile=formal-proof` enables formal-proof prompt guidance; the default
   profile does not silently activate it from prompt keywords.
7. `internal/bridge/profiles/registry` is the neutral boundary for
   profile-specific orchestration behavior. Formal-proof prompt fragments,
   assessments, manual-build carry-over, command fingerprint decisions, and
   benchmark-specific detectors live under
   `internal/bridge/profiles/formalproof/`.
8. `internal/bridge/orchestration_relay.go:formatRelayPriorTurn` controls how much
   prior visible output and command context is sent to the next CLI.
9. `internal/bridge/orchestration_relay.go:runRelayCLI`,
   `internal/bridge/orchestration_codex.go:runCodexInteractive`, and
   `internal/bridge/orchestration_claude.go:runClaudeInteractive` preserve the
   run-scoped native Codex app-server thread(s), Claude stream-json process,
   stable Claude session id, and Codex thread id(s) when launching the next CLI
   turn. Codex + Codex uses separate `codex-a` and `codex-b` sessions.
10. `internal/bridge/orchestration_native.go:runNativeContextCompaction` runs
   native compaction only through verified CLI control channels, records skip
   Bridge notes for unsupported surfaces, and emits warning-only Bridge notes on
   failure.
11. `internal/bridge/orchestration_native.go:runEndDataWithNativeResume` and
   `internal/bridge/orchestration_relay.go:relayRunEndData` attach Codex and
   Claude native resume metadata to run-end payloads.
12. `internal/bridge/orchestration.go:cwd` locks resumed runs to the absolute
   run cwd reported by Bridge, and
   `internal/bridge/orchestration.go:PrepareOrchestrationPromptFiles` writes
   uploaded files under that cwd.
13. `internal/bridge/orchestration_relay.go:relayTerminalContent` controls terminal
   run content without adding a hidden verifier or remediation turn.
14. Keep event kinds compatible with
   `frontend/src/app/lib/utils.ts:visibleOrchestrationEvents`, including
   terminal run summary rendering for `run.end` / `run.error`.
15. Update
   [docs/features/orchestration-pass-through-cli.md](features/orchestration-pass-through-cli.md).

### Change Orchestration Event Protocol

1. `internal/protocol/envelope.go:OrchestrationEventPayload` defines the event
   contract, including `Source`, `Severity`, typed sub-payloads, and
   `RunConclusion`.
2. `internal/bridge/orchestration_events.go:emit` normalizes source/severity and
   emits exactly one `run.conclusion` before terminal run events.
3. `internal/bridge/orchestration_events.go:emitTool` maps `RunnerToolEvent` into
   typed `CommandData`; frontend command cards must use `commandData`, not
   free-form `data` keys.
4. `internal/store/store.go:AddOrchestrationEvent` persists typed event
   payloads and keeps legacy `Data` compatibility for older rows.
5. `internal/hub/orchestration.go:handleOrchestrationEvent` persists the
   Bridge event and updates run continuity fields from typed payloads.
6. `frontend/src/app/lib/utils.ts:visibleOrchestrationEvents` reduces events
   using `source`, `severity`, `commandData`, and `runConclusion`, and
   `frontend/src/app/components/OrchestrationComponents.tsx:OrchestrationEventItem`
   renders the result.
7. `internal/hub/share.go:publicOrchestrationEvents` is the public transcript
   sanitizer for typed orchestration events.
8. Update [docs/features/orchestration-event-protocol-hardening(1).md](features/orchestration-event-protocol-hardening(1).md).

### Change SQLite Schema

1. Edit `internal/store.Store.Migrate`.
2. Update structs, scanners, and CRUD methods in `internal/store/store.go`.
3. Add store tests.
4. Update `docs/architecture.md` storage bullets and `docs/change-impact.md`.
4. Update docs that describe persisted tables.

### Change Frontend UI

1. Edit the relevant file under `frontend/src/app/` or styles.
2. Run `cd frontend && npm run build` so `internal/web/static/` is refreshed.
3. Run Go tests because embedded static tests cover expected assets.

### Change Config

1. Edit `internal/config/config.go`.
2. Bind env overrides in `internal/config/load.go`.
3. Update `configs/dev.yaml.example`.
4. For `bridge.long_command_observer`, keep YAML fields, env overrides, and
   `internal/bridge/orchestration_claude.go:longCommandObserverConfig` defaults in
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
