# AGENTS.md - Codex Bridge Session Brief

This is the entry brief for AI coding sessions. Read this file first, then load
the detailed docs linked below only when the task needs them.

## Positioning

Single-user remote access, from any browser, to Codex and Claude Code CLIs
running on a private machine. Two surfaces: 1:1 chat with a single CLI, and
multi-CLI orchestration that relays turns between one long-lived native Codex
session and one long-lived native Claude Code session (with opt-in formal-proof
prompt guidance). The public Hub is only a transport, auth, static UI, and SQLite
persistence layer. The private Bridge owns workspace access and model
credentials.

## Core Decisions

1. **One Go module, one binary.** `codex-bridge hub` runs the public Hub and
   `codex-bridge bridge` runs the private reverse client. Keep this shape unless
   an ADR explicitly changes it.
2. **Reverse WebSocket Bridge.** Hub never opens an inbound connection to the
   private machine. Bridge dials Hub with an enroll token and multiplexes work
   over `internal/protocol.Envelope`.
3. **SQLite on Hub only.** Users, agents, sessions, messages, runs, enroll
   tokens, and orchestration events live in SQLite with one open connection.
4. **Runner boundary stays small.** Chat uses `internal/bridge.Runner`; `echo`
   is deterministic, `codex` uses `codex exec --json`, and a future app-server
   runner must stay behind that interface unless the wire protocol changes by
   design.
5. **Embedded UI is deployable output.** The checked-in static UI under
   `internal/web/static/` is what the Go binary serves. The `frontend/` Vite app
   is source tooling for regenerating that directory.
6. **Conversation continuity is a product invariant.** Browser chat must reuse
   the selected `sessions.id` and persisted `remote_thread_id`; orchestration
   follow-up prompts must use `POST /api/orchestrations/{runID}/prompts`, not a
   new run. See [docs/features/orchestration-continuity.md](docs/features/orchestration-continuity.md).

## Hard Rules

### R1. Design Before Code

Non-trivial changes need a design artifact before code:

| Change type | Required artifact |
| --- | --- |
| Architecture, protocol, persistence, runner boundary, auth/session model | `docs/adr/NNN-<slug>.md` |
| User-visible feature, HTTP endpoint, WebSocket frame, UI workflow | `docs/features/<slug>.md` |
| Small bug fix, copy tweak, test-only change | May skip design, but commit message must explain scope |

Feature docs must include goals, explicit non-goals, data/protocol impact,
implementation steps, exit gates, and reviewer Q&A.

### R2. Change-Impact Table

Before editing and before submitting, check
[docs/change-impact.md](docs/change-impact.md). Add new coupling rules only
there; keep this file as a concise brief.

Top frequent rules:

| If you change | Also update |
| --- | --- |
| HTTP or WebSocket API | `internal/hub/server.go`, protocol payloads if needed, frontend caller, docs |
| `internal/protocol.Envelope` frame or payload | Hub handler, Bridge handler, frontend parser, integration tests, architecture docs |
| SQLite schema in `internal/store.Store.Migrate` | Store structs/CRUD, tests, `docs/code-map.md`, `docs/architecture.md` |
| Config or env var | `internal/config/`, `configs/*.yaml.example`, `docs/dev-workflow.md`, README |
| Frontend source in `frontend/` | `npm run build` to refresh `internal/web/static/`, then Go tests/build |

### R3. Anchored References

Docs that reference code should use `path:symbol` or `path:line`, for example
`internal/hub/orchestration.go:handleContinueOrchestration`.

### R4. Single Source of Truth

- Environment variables: [docs/dev-workflow.md](docs/dev-workflow.md)
- Detailed code map: [docs/code-map.md](docs/code-map.md)
- Full change-impact rules: [docs/change-impact.md](docs/change-impact.md)
- Architecture overview: [docs/architecture.md](docs/architecture.md)
- Roadmap/status: [docs/plan.md](docs/plan.md)

Other docs should link to these sources instead of duplicating full tables.

### R5. Deprecate, Do Not Delete

When a design is replaced, keep the old doc and add this at the top:

```markdown
> **DEPRECATED - <short reason>**
>
> Current design: [link]. Historical only; do not implement from this doc.
```

### R6. Commit Message Contract

Use this format for commits that include code or behavior changes:

```text
<type>(<scope>): <short summary>

Change summary:
- <path>: <what changed>

Exit gate:
- [x] <test or manual verification>

Doc-Impact: none
```

If docs were changed or should have been changed, replace `none` with a
comma-separated relative path list:

```text
Doc-Impact: AGENTS.md, docs/change-impact.md
```

Allowed types: `feat`, `fix`, `docs`, `refactor`, `perf`, `test`, `chore`.
Common scopes: `hub`, `bridge`, `store`, `protocol`, `auth`, `ui`, `android`,
`config`, `deploy`, `docs`.

### R7. Post-Code Sweep

After implementation, sweep in this order:

1. `AGENTS.md`
2. `docs/architecture.md`
3. `docs/code-map.md`
4. `docs/dev-workflow.md`
5. Relevant ADR/feature docs
6. `docs/change-impact.md`
7. README / README.zh-CN if user-facing setup changed

Run `make doc-lint` when document paths, anchors, env vars, or commit rules were
touched.

### R8. Continuity Guard

Do not introduce a path where sending a follow-up task silently opens a fresh
session/run. If a new task is meant to continue context:

- Chat: keep the same `sid` and pass the stored `remote_thread_id` back into
  `codex exec resume`.
- Orchestration: keep the same `runID` and call
  `/api/orchestrations/{runID}/prompts`.

Only explicit New Session / New Run actions may create a new context.

## Feature Dev Loop

1. Confirm the goal, boundaries, and affected surfaces.
2. Add or update the design doc when R1 applies.
3. Implement against existing package boundaries.
4. Verify with focused tests, then broader tests when shared behavior changed.
5. Sweep docs and produce a commit message with `Doc-Impact`.

## Main Code Map

| Area | Path |
| --- | --- |
| Config | `internal/config/` |
| Hub HTTP/API/WS | `internal/hub/` |
| Bridge reverse client | `internal/bridge/` |
| SQLite store | `internal/store/` |
| Auth/JWT | `internal/auth/` |
| Wire protocol | `internal/protocol/` |
| Embedded UI output | `internal/web/static/` |
| Frontend source | `frontend/src/app/` |
| Android wrapper | `android/` |

Detailed "change X -> edit Y" guidance is in [docs/code-map.md](docs/code-map.md).

## Commands

```bash
/usr/local/go/bin/go test ./...
CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .
make doc-lint
```

Local smoke flow:

```bash
cp configs/dev.yaml.example configs/dev.yaml
/usr/local/go/bin/go run . user --username admin --password 'change-me'
/usr/local/go/bin/go run . enroll
/usr/local/go/bin/go run . hub
/usr/local/go/bin/go run . bridge
```
