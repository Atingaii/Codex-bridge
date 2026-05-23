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
| Runner abstraction | `internal/bridge/runner.go`, `internal/bridge/session.go` |
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

### Change Orchestration Continuity

1. `internal/hub/orchestration.go:handleCreateOrchestration` creates a new run.
2. `internal/hub/orchestration.go:handleContinueOrchestration` appends a prompt
   to the same run and compacts previous events into context.
3. `internal/bridge/orchestration.go:run` executes turns
   using the prompt plus compacted context.
4. `frontend/src/app/App.tsx:OrchestrationWorkspace` must keep selecting the
   current run and call the continue endpoint for follow-up tasks.
5. Update [docs/features/orchestration-continuity.md](features/orchestration-continuity.md).

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
