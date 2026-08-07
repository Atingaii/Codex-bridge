# Codex Bridge Plan

## Goal

Let a single user talk from any browser to Codex CLI running on a private machine.

## Resource Budget

- One public Hub process and one private Bridge process
- SQLite with a single open DB connection
- One reverse WebSocket between Bridge and Hub
- Native browser UI embedded with `go:embed`
- No React/Vite runtime, Redis, Postgres, queue, vector store, or file projection

## Phases

| Phase | Target | Status |
| --- | --- | --- |
| P0 | Echo runner over reverse WebSocket | implemented |
| P1 | `codex exec --json` runner | implemented |
| P2 | Multiple `sid` sessions over one Bridge connection | implemented |
| P3 | SQLite users, agents, sessions, messages | implemented |
| P3.1 | Agent-scoped chat session spaces | implemented |
| P4 | Cookie JWT login and Caddy/systemd deployment files | implemented |
| P5 | Heartbeat, reconnect, cancel, close-session cleanup | implemented |
| P5.1 | Machine-bound reconnect after enrollment expiry and online Bridge metadata | implemented |
| P6 | Orchestration create/continue event stream | implemented |
| P6.1 | Low-token orchestration handoff strategies | implemented |
| P6.2 | Deep collaboration routing and orchestration browser approval | implemented |
| P6.3 | Orchestration capability matrix and pass-through Claude/Codex relay | implemented |
| P6.4 | CLI endpoint repair commands | implemented |
| P6.5 | Formal-proof lightweight workspace bootstrap | implemented |
| P6.6 | Persistent per-context task queue | designed |
| P7 | Browser permission prompts over app-server | implemented for Codex chat and Codex orchestration |
| P8 | Empty-reply hardening and accessible session deletion | implemented |
| P8.1 | Same-thread chat recovery and bounded retry evidence | implemented |
| P9 | Turnstile-protected self-registration and per-user access | implemented |
| P10 | Public screenshot guide for chat, orchestration, and formal proof | implemented |

## Engineering Workflow

- Non-trivial changes need ADR or feature design before code.
- Use [docs/change-impact.md](change-impact.md) before editing and before
  submitting.
- Commit messages must include `Doc-Impact: ...`.
- `make doc-lint` checks the lightweight documentation contract.

## Follow-up

- Chat resumes native Codex history: short-lived `codex exec` paths resume with the
  returned thread id, and review-required chat uses the `codex app-server` runner
  (`internal/bridge/appserver_runner.go`) with `initialize` / `thread/start` /
  `turn/start` / streamed deltas / `turn/interrupt`.
- Orchestration is a native-session relay: Claude + Codex runs keep one
  long-lived Codex app-server thread and one long-lived Claude Code stream-json
  session per run, while Codex + Codex runs keep independent `codex-a` and
  `codex-b` app-server threads. Native sessions are reused across turns so the
  user can `resume` them from the workspace. The Bridge only relays output and
  turn context; it does not inject verifier/remediation/assessment turns.
  Formal-proof is opt-in *prompt guidance* via
  `internal/bridge/profiles/registry` + `internal/bridge/profiles/formalproof`;
  new formal-proof runs also get a persistent lightweight proof ledger under the
  run cwd before scheduled CLI turns begin.
- Persistent task queue design is captured in
  [docs/features/persistent-task-queue.md](features/persistent-task-queue.md):
  Hub should persist per-chat-session and per-orchestration-run serial queues so
  users can submit follow-up work while a task is still running. This is not yet
  implemented.

## Maintenance Log

- 2026-08-07: Added WebSocket ping/pong resilience, bounded same-session chat
  reconnect with state/output recovery, wider Bridge liveness tolerance, and
  request-scoped strict approval for workspace `cat`, `coqc`, and batch
  `coqtop` commands.
- 2026-08-07: Added bounded ordinary-chat response evidence, direct recovery
  from streamed text, and one same-native-thread continuation for empty or
  interrupted CLI terminal responses without replaying the original prompt.
- 2026-08-06: Fixed expired machine-bound Bridge reconnects, exposed active
  Bridge version/connection time, reduced returning-worker relay history, and
  bounded the Bridge-owned formal-proof follow-up ledger.
- 2026-08-05: Added the public `/help` guide and `/hlep` alias with 17
  deterministic application screenshots, detailed collaboration/debate and
  formal-proof workflows, copyable demos, and low-context orchestration advice.
- 2026-05-30: Removed the abandoned external CCB orchestration backend and the
  superseded per-turn orchestration design (verifier / remediation / acceptance
  assessment), the deprecated `orchestration_runner` config, and dead CCB install
  code in `link.go`. `internal/bridge/orchestration.go` 7358 -> ~3200 lines;
  `profiles/formalproof` 1579 -> ~335; `profiles/registry` 109 -> ~39. Full
  `go test ./...` green. Verified unreachable code with
  `go run golang.org/x/tools/cmd/deadcode@latest ./...`.
- 2026-05-30: Completed the Go Part A monolith split from
  `docs/features/monolith-file-split.md`, moving orchestration relay, Codex,
  Claude, events, redaction, and profile helpers into same-package
  `internal/bridge/orchestration*.go` files.
- 2026-05-30: Completed the frontend Part B monolith split from
  `docs/features/monolith-file-split.md`, reducing `frontend/src/app/App.tsx`
  to root routing/bootstrap and moving pages, shared helpers, chat components,
  settings, orchestration renderers, and UI primitives under
  `frontend/src/app/{pages,components,lib}`.
