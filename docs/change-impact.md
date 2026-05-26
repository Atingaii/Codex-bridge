# Change Impact

This is the detailed "if X changes, Y must also change" table. `AGENTS.md`
keeps only the top frequent subset.

## General

| Change | Must update |
| --- | --- |
| Add or change config field | `internal/config/` structs/loaders, `configs/*.yaml.example`, `docs/dev-workflow.md`, README if user-facing |
| Add or change env var | `internal/config/load.go`, `configs/dev.yaml.example`, `docs/dev-workflow.md` env table, deploy docs |
| Add or change HTTP endpoint | `internal/hub/server.go` route, handler tests, frontend API caller, feature/API docs |
| Add or change WebSocket frame | `internal/protocol/envelope.go`, Hub handler, Bridge handler, frontend parser, integration tests, `docs/architecture.md` |
| Add or change SQLite table/column | `internal/store.Store.Migrate`, store structs/scanners/CRUD, store tests, `docs/code-map.md`, architecture storage table |
| Add or change auth/cookie behavior | `internal/auth/`, `internal/hub/server.go`, frontend login/logout handling, security docs |
| Add or change runner behavior | `internal/bridge/runner.go` or `internal/bridge/orchestration.go`, protocol payload if needed, tests, runner notes in docs |
| Add or change deployment unit | `deploy/`, README deployment section, `docs/dev-workflow.md` |
| Add or change install/download flow | `internal/hub/server.go`, `configs/*.yaml.example`, README/README.zh-CN, integration tests |
| Add or change Android wrapper behavior | `android/`, `frontend/capacitor.config.ts`, README Android section, GitHub workflow if release output changes |
| Add or change embedded static assets | `frontend/` source, run `npm run build`, verify generated `internal/web/static/` |

## Conversation And Orchestration

| Change | Must update |
| --- | --- |
| Chat session creation or selection | `internal/hub/server.go`, `internal/store/` session methods, `frontend/src/app/App.tsx`, continuity feature doc |
| Chat prompt flow | `internal/hub/ws_browser.go`, `internal/bridge/session.go`, `internal/bridge/runner.go`, run/message store methods, integration tests |
| Codex thread resume behavior | `internal/bridge/runner.go:args`, `internal/hub/ws_bridge.go:handlePromptComplete`, `internal/store/store.go:UpdateSessionRemoteThreadByID`, tests |
| Orchestration create/continue flow | `internal/hub/orchestration.go`, `internal/bridge/orchestration.go`, `frontend/src/app/App.tsx`, `docs/features/orchestration-continuity.md` |
| Orchestration endpoint continuity | `internal/hub/orchestration.go:handleContinueOrchestration`, `frontend/src/app/App.tsx`, `docs/features/orchestration-continuity.md`, `docs/features/manual-orchestration-rounds.md` |
| Bridge capability reporting | `internal/protocol.RegisterPayload`, `internal/bridge/client.go`, `internal/hub/pool.go`, `internal/hub/server.go:handleAgents`, frontend agent types/UI, policy tests |
| Orchestration event shape | `internal/protocol.OrchestrationEventPayload`, `internal/store.OrchestrationEvent`, visible event reducer in `frontend/src/app/App.tsx`, integration tests |
| Cancellation semantics | Hub cancel handler, Bridge cancel manager, status constants, frontend stop button, tests |
| Attachment handling | Hub size/type validation, Bridge file materialization, frontend upload limits, README if limits are user-facing |

## Security

| Change | Must update |
| --- | --- |
| Secret field or token source | Example configs with empty placeholders, env var docs, logging redaction review |
| Login/register validation | Auth tests, frontend validation text, rate limit notes |
| CORS/origin/cookie policy | `internal/hub/server.go`, deploy/Caddy docs, README deployment guidance |
| Logging fields | Check no password/token/API key appears in logs, update dev workflow logging notes |
| Private operational notes | Put in `docs/private/`; do not commit tickets, scan reports, real tokens, or private hostnames |

## Documentation

| Change | Must update |
| --- | --- |
| New ADR | Add `docs/adr/NNN-<slug>.md`, link from architecture/AGENTS if top-level |
| New feature design | Add `docs/features/<slug>.md`, update `docs/plan.md` if roadmap/status changes |
| New package or major file | `docs/code-map.md` and AGENTS brief table if top-level |
| Changed setup command | README, README.zh-CN if Chinese quick start changes, `docs/dev-workflow.md` |
| Deprecated design | Mark old doc as deprecated and link the replacement |

Last updated: 2026-05-23
