# ProofBridge Plan

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
| P6.7 | Structured Agent dialogue relay and evidence-based convergence | implemented |
| P6.8 | Durable bounded task graph with shared-CWD serialized workers and reviewer barrier | implemented |
| P7 | Browser permission prompts over app-server | implemented for Codex chat and Codex orchestration |
| P8 | Empty-reply hardening and accessible session deletion | implemented |
| P8.1 | Same-thread chat recovery and bounded retry evidence | implemented |
| P9 | Turnstile-protected self-registration and per-user access | implemented |
| P10 | Public screenshot guide for chat, orchestration, and formal proof | implemented |
| P10.1 | Orchestration runtime, token usage, cost estimates, one-turn mode, and prominent final conclusion | implemented |
| P10.2 | Administrator activity and usage dashboard with user aggregates and read-only conversation detail | implemented |
| P10.3 | Isolated Codex/Claude worker profiles, dual-provider pairs, and deterministic quorum early-stop | implemented |

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
  session per run, Codex + Codex runs keep independent `codex-a` and `codex-b`
  app-server threads, and Claude + Claude runs keep independent `claude-a` and
  `claude-b` sessions. Native sessions are reused across turns so the
  user can `resume` them from the workspace. The Bridge only relays worker
  output and turn context; it does not inject remediation into worker sessions.
  Local hard gates nominate completion candidates; two fresh Agent Verifiers
  use the role presets for those candidates, and a run ends early only when both models and local evidence gates accept the
  structured, independently evidenced final handoff.
  Saved CLI presets can be pinned independently to `claude`, `codex-a`,
  `codex-b`, `claude-a`, and `claude-b` worker slots without changing
  machine-wide configuration.
  Formal-proof is opt-in *prompt guidance* via
  `internal/bridge/profiles/registry` + `internal/bridge/profiles/formalproof`;
  new formal-proof runs also get a persistent lightweight proof ledger under the
  run cwd before scheduled CLI turns begin.
- The earlier serial persistent-queue proposal is deprecated. New graph-capable
  orchestration runs use the Hub SQLite durable bounded task graph described in
  [docs/features/durable-bounded-orchestration-task-graph.md](features/durable-bounded-orchestration-task-graph.md).
- Administrators can gray-test the optional planning progress workspace: two
  planning/review nodes precede the existing graph, while a default-closed
  overlay presents prompt history, a React Flow node map, and checklist state
  without resizing the live transcript. See
  [docs/features/orchestration-plan-progress-workspace.md](features/orchestration-plan-progress-workspace.md).

## Maintenance Log

- 2026-08-15: Delayed durable task recovery until the existing bounded Bridge
  reconnect window after Hub startup, so a brief Hub-only reload preserves
  active attempt identity and buffered terminal evidence; only endpoints still
  offline after that window are conservatively recovered.
- 2026-08-14: Made Bridge installation endpoint-neutral. `/install.sh` now
  atomically replaces only the local binary; the following directory-scoped
  `link` starts or restarts only the selected endpoint, so linking another
  workspace cannot interrupt active same-machine orchestration runs.
- 2026-08-11: Archived the Android wrapper and removed APK builds from the
  automatic release path. Android is no longer maintained or considered a
  release exit gate; existing wrapper sources remain for historical reference.
- 2026-08-09: Coalesced high-frequency native command progress at the Bridge
  before transport and SQLite persistence, preserving full output and terminal
  ordering without adding a database service or user configuration. Durable
  orchestration prompts now use workspace-neutral wording and do not prescribe
  a Git workflow.
- 2026-08-08: Added a Hub-persisted task graph with stable attempt identity,
  ambiguity-safe restart recovery, user-selected shared CWD execution,
  serialized candidate writes, and a final reviewer/formal-checker barrier.
- 2026-08-08: Removed bootstrap-admin cross-user and legacy-unowned CLI
  endpoint visibility, and protected existing machine ownership from
  reassignment during reconnect.
- 2026-08-08: Added structured Agent-to-Agent relay packets, compact incremental
  cross-worker context, and conservative reviewer/critic-confirmed early
  convergence without changing orchestration APIs, UI, or native sessions.
- 2026-08-07: Added WebSocket ping/pong resilience, bounded same-session chat
  reconnect with state/output recovery, wider Bridge liveness tolerance, and
  request-scoped strict approval for workspace `cat`, `coqc`, and batch
  `coqtop` commands.
- 2026-08-07: Prevented transient Bridge outbound-queue saturation from
  forcing a reconnect, added heartbeat interval defaults, and reset backoff
  after stable connections.
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
