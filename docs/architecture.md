# Architecture

This is the single overview for runtime architecture. Detailed rationale lives
in ADRs; implementation paths live in [docs/code-map.md](code-map.md).

```text
Browser UI (one selected run stream)
  | WSS /ws/chat?sid=<session>
Hub (Go + embedded UI + SQLite)
  | reverse WSS /api/agents/connect?token=<enroll>
Bridge (Go)
  | spawn per prompt
codex exec --json
```

All three WebSocket paths use standard ping/pong control frames in addition to
the existing JSON heartbeat envelopes. Hub serializes JSON and ping writes in
`internal/hub/pool.go:WriteLoop`; Bridge does the same in
`internal/bridge/client.go:connectOnce`. Read deadlines retain at least six
heartbeat windows (and at least 90 seconds). Chat reconnect in
`frontend/src/app/pages/Workspace.tsx:connectWS` keeps the same `sid`, reloads
persisted messages/runs, and receives any in-flight assistant buffer from
`internal/hub/ws_browser.go:handleBrowserWS`.

For orchestration, a transient reverse Bridge WebSocket loss is transport state,
not task cancellation. `internal/bridge/client.go:connectOnce` detaches the
dead output channel while `internal/bridge/orchestration_events.go:send` buffers
events until reconnect. `internal/hub/ws_bridge.go:bridgeReconnectGrace` holds
the Hub-side active run through the Bridge's maximum retry delay, jitter, and a
heartbeat window before declaring an offline machine failed. Process restart
and explicit cancellation remain terminal. After a registered transport drops,
`internal/bridge/client.go:bridgeReconnectDelay` permits one immediate retry;
failed connection attempts retain bounded exponential backoff with jitter. See
[orchestration transport recovery](features/orchestration-transport-recovery.md).
The event-history endpoint returns an authoritative earlier-page indicator, and
a terminal offline error records the bounded transport close reason. See
[orchestration history and terminal reasons](features/orchestration-history-and-terminal-reasons.md).

CLI endpoints created with the review-required profile use
`internal/bridge/appserver_runner.go` instead of `codex exec --json` for Codex
chat. That runner keeps a `codex app-server --listen stdio://` JSON-RPC session
open for the turn so Codex approval requests can be relayed to the browser.

When the endpoint runs with `bridge.runner: acp`, chat uses
`internal/bridge/acp_runner.go:ACPRunner` instead of a per-turn process. It keeps
one resident Agent Client Protocol adapter per chat session
(`internal/bridge/acp_client.go:acpClient` over stdio JSON-RPC) so prompts stream
into a live conversation without restarting the process (target A). It also
resolves the underlying CLI's own session id so the same conversation can be
continued from the workspace with `claude --resume <id>` / `codex resume <id>`
(target B). The dual ids round-trip to the browser through the optional
`SessionOpenedPayload`/`PromptCompletePayload` `nativeResumeId` and
`nativeResumeCommand` fields; the ACP session id is stored as the existing
`remote_thread_id` so continuity plumbing is unchanged. The full design is in
[docs/features/acp-runner.md](features/acp-runner.md). The `echo`, `codex-exec`,
and `codex-app-server` runners are unchanged; only `ACPRunner` implements the
`internal/bridge/runner.go:SessionRunner` interface and the session layer falls
back to one-shot `Runner.Prompt` for every other runner. The browser surfaces
the takeover command via `frontend/src/app/components/chat/TakeoverHint.tsx` and
an ACP badge in `frontend/src/app/pages/Workspace.tsx`, shown only when the
Bridge actually resolved a command (otherwise a neutral "unavailable" note);
see [docs/features/acp-runner-pr2.md](features/acp-runner-pr2.md). Orchestration
resident-session reuse is intentionally deferred (it would touch the
review-required approval pipeline).

The orchestration UI uses HTTP for create/continue/cancel plus a run-scoped
WebSocket for event streaming:

```text
Browser UI
  | POST /api/orchestrations
  | POST /api/orchestrations/<run>/prompts
  | WSS /ws/orchestrations?runId=<run>
Hub (SQLite runs/events + durable task graph/attempts)
  | reverse WSS orchestration_start / orchestration_event
Bridge (independent execution handles per run)
  | run-scoped native CLI sessions
Codex CLI / Claude CLI
```

The Bridge keeps orchestration deterministic while preserving native CLI
continuity. Each run persists a narrow `worker_pair`: `claude-codex` keeps the
default Claude Code + Codex relay, while `codex-codex` runs two Codex
participants in separate worker slots. For Claude + Codex runs, Bridge keeps
one long-lived Codex app-server thread and one long-lived Claude Code
stream-json session. For Codex + Codex runs, Bridge keeps independent
`codex-a` and `codex-b` app-server threads so each participant has distinct
native context. Direct orchestration is a pass-through relay: the run's
persisted `worker_pair` and `first_cli` settings decide which worker receives
the browser task first, Bridge streams CLI deltas, typed command lifecycle and
progress events, coalescing only high-frequency command output updates before
transport while preserving their text and terminal ordering, and
terminal status to the browser, and the next worker receives a parsed
Agent-to-Agent packet containing the newest request, changed files,
verification, next action, risks, and useful command context. Full prose is a
bounded fallback only when the structured packet is absent or malformed.
Direct serial relay reviewer/critic turns may end below their configured ceiling
only after an explicit resolved handoff with independent evidence. For
graph-capable Bridges, `max_turns` instead budgets whole collaboration rounds:
each round runs two bounded candidates, serial integration, and an independent
reviewer, and Hub automatically creates every configured generation under the
same run id. Intermediate valid `resolved`, `needs_next`, and `blocked`
handoffs advance the graph; only the final generation applies the resolved
completion barrier. Formal-proof review additionally requires a successful
reviewing-turn checker/audit command. Hub SQLite is the task authority; node
attempts carry stable identities and payload digests, ambiguous restart state
becomes `unknown`, and Bridge executes each node serially in the selected
project workspace. Older Bridges retain the serial relay behavior. See
[ADR-008](adr/008-durable-orchestration-task-graph.md).
Bridge persists the legacy
Codex thread id, the `codex_thread_ids_json` slot map, and the stable Claude
session id so follow-up prompts can resume native history after a Bridge
restart where the CLI supports it. Run-end data also carries direct native
resume metadata for the participating CLIs: Codex exposes
`codex resume <thread-id>` for each persisted Codex slot, and Claude exposes
`claude --resume <session-id>` plus the project transcript path under
`~/.claude/projects/<encoded-cwd>/`. After successful Claude turns,
Bridge updates only the current cwd entry in `~/.claude.json` so native Claude
project metadata points at the Bridge session without touching unrelated
projects, and materializes the same Claude-written transcript so Claude Code's
interactive `/resume` picker can show it from the run cwd. It does not add
hidden proof strategy gates,
automatic verifier turns, or remediation turns. Formal-proof guidance is opt-in
through the persisted `profile=formal-proof` run setting selected in the
orchestration UI; the default profile does not activate proof guidance based on
prompt keywords. Under that profile, collaboration alternates a proof author
and independent proof auditor, while debate alternates a falsifiable proof
claim and adversarial critic. Both contracts freeze the named statement, carry
proof-state/checker evidence forward, audit trust dependencies, and require a
final adjudication that cannot call placeholder, weakened, axiom-tainted, or
unfinished output a completed proof. Existing UI modes, role identifiers,
turn counts, and protocol payloads are unchanged. See
[docs/features/formal-proof-orchestration-contracts.md](features/formal-proof-orchestration-contracts.md).
Relay prompts reuse each worker's native session as the primary memory: a
returning worker receives only the newest cross-worker handoff plus bounded
command evidence instead of copies of its own earlier output. A worker entering
the run for the first time still receives the bounded prior history it needs.
For new formal-proof runs, Bridge keeps the selected project directory as the
CLI cwd and writes one `proof-notes/<run-id>.md` evidence ledger beneath its
`.codex-bridge` metadata directory before scheduled CLI turns begin. Uploaded
projects are materialized directly into that selected project directory. This
bootstrap is not a hidden verifier turn and does not consume the user's turn
budget. Workers run project-appropriate proof assistant commands directly and
update the ledger only with material targets,
obligations, command evidence, blockers, or decisions; Bridge does not generate
a checker or run per-turn metadata synchronization. Bridge maintains follow-up
requests inside one delimited block, retaining the newest eight bounded requests
and a count of compacted predecessors without rewriting worker-owned evidence.
See
[docs/features/formal-proof-lightweight-workspace.md](features/formal-proof-lightweight-workspace.md).
Follow-up prompts reuse the locked selected project cwd through the existing `RunCWD`
continuity path. The native-session design is documented in
[docs/features/native-interactive-orchestration.md](features/native-interactive-orchestration.md),
and the relay contract is documented in
[docs/features/orchestration-pass-through-cli.md](features/orchestration-pass-through-cli.md).
The structured handoff and convergence rules are documented in
[docs/features/structured-agent-dialogue-relay.md](features/structured-agent-dialogue-relay.md).
Runs may opt in to `native_context_compaction=after-turn`; Bridge then sends
native compaction maintenance after each successful business turn where the CLI
surface exposes a verified control channel. Codex uses app-server
`thread/compact/start`; Claude Code stream-json is skipped with an info Bridge
note until it exposes an equivalent control channel. Bridge keeps maintenance
output out of handoffs and treats compaction failures as warnings.
Profile-specific prompt fragments, assessments, manual-build carry-over, and
command fingerprint policy live behind `internal/bridge/profiles/registry` and
`internal/bridge/profiles/formalproof/`; `internal/bridge/orchestration.go`
only calls the neutral registry boundary.

Orchestration events use a typed contract in
`internal/protocol/envelope.go:OrchestrationEventPayload`. `source`
distinguishes `cli`, `bridge`, and `user` events; `severity` carries
Bridge-internal log levels without overloading lifecycle `status`; command
start/update/end,
run-start, turn-start, run-end, Bridge-note, and final-conclusion details live
in typed sub-payloads. `turn.start.content` is a one-line status; the full
local prompt is kept in `TurnStartData.PromptText` for authenticated local
diagnostics and is stripped from public shares. Every terminal run emits one
structured `run.conclusion` event before `run.end`, `run.error`, or
`run.cancelled`. Each `turn.end` also carries Bridge-captured start, completion,
and elapsed wall-clock time, so follow-up turns append independent timing to the
same run.

Legacy Bridges emit a persisted `turn.usage` snapshot after each relay turn.
Current Bridges additionally handle `orchestration_usage_sync_request` by
scanning only the named native CLI session logs read-only and returning
normalized, content-free usage events. The Hub validates run ownership and
stores those events idempotently in a separate ledger. Terminal runs trigger a
sync automatically, while the owner-scoped statistics endpoint and retry route
support historical backfill. Ledger statistics distinguish non-cached input,
cache read/write, output, and reasoning; legacy snapshots remain visible but
are explicitly incomplete. See
[docs/features/embedded-cli-usage-ledger.md](features/embedded-cli-usage-ledger.md).

Bridge long-command observation is controlled by
`bridge.long_command_observer`. Matching Claude commands can receive a tagged
stream-input note, and matching Codex commands emit a visible Bridge-note row
when no stdin side-channel exists. Both paths use
`BridgeNoteData.InjectedText` so the browser timeline records exactly what
Bridge said.

Transient orchestration failures use separate bounded recovery classes. An
explicit model-capacity rejection keeps the same relay turn and replays the
same prompt after 10/30/60-second backoffs. Codex app-server
`Reconnecting N/M` progress first keeps the current native turn alive for a
bounded grace period; if that native reconnect fails, Bridge recreates only
the damaged app-server process and resumes the same Codex thread after
5/15/30-second backoffs with accumulated output and command evidence instead
of replaying the original task. When a restarted Bridge resumes a Codex thread
whose prior native turn still owns the writer lease, it keeps that thread and
retries the still-unaccepted original prompt after 5/15/30/60-second backoffs.
Durable task attempts close their short-lived app-server process and unsubscribe
the persisted thread before emitting any terminal event, including failures, so
the next graph node or an external `codex resume` cannot inherit a stale writer.
Waiting, retry start, native progress, and retry exhaustion are persisted as
Bridge-originated `turn.delta` notices, and cancellation interrupts every
wait. Authentication, validation, command, and proof failures remain terminal. See
[docs/features/orchestration-transient-cli-recovery.md](features/orchestration-transient-cli-recovery.md).

Review-required Claude orchestration uses Claude Code's
`--permission-prompt-tool` support.
`internal/bridge/orchestration_claude.go:runClaude` and
`internal/bridge/orchestration_claude.go:runClaudeInteractive` write a
temporary MCP config, run `codex-bridge claude-approval-mcp` as a stdio MCP
server, and forward MCP permission prompts back to the parent Bridge over a
Unix socket. Hub then reuses existing `approval_request` and
`approval_response` frames with `payload.runId` for browser approval on the
orchestration timeline.

Codex orchestration uses `codex app-server --listen stdio://` through
`internal/bridge/orchestration_codex.go:runCodexInteractive`. App-server approval
callbacks are mapped to run-scoped `approval_request` frames with
`payload.runId`, and browser decisions return as `approval_response` frames to
the owning Bridge. The standalone `internal/bridge/appserver_runner.go:Prompt`
path remains the Codex app-server runner for chat and non-orchestration runner
uses.

In review-required mode, command execution callbacks pass through
`internal/bridge/approval_allowlist.go:isProofCommandAutoApprovable` before
browser relay. A single workspace-contained `cat`, `coqc`, or batch `coqtop`
request can receive a request-scoped approval; file changes, permission grants,
compound shell syntax, unknown options, and paths escaping the workspace keep
the normal browser approval path. The same validator is applied at the Claude
permission socket boundary.

The standalone app-server path retries process initialization and thread
preparation once, before `turn/start`; it never replays a submitted turn because
tool and file side effects are not idempotent. It recovers terminal-only agent
text, ignores empty unscoped protocol noise until an attributable terminal
event arrives, and preserves a bounded diagnostic tail when the child process
exits. `internal/bridge/session_recovery.go:runChatPromptAttempt` also retains a
bounded assistant/tool summary for ordinary chat: streamed text can complete a
missing terminal payload, while an empty or interrupted turn can issue one
short continuation in the same native thread without replaying the original
prompt. `internal/hub/ws_bridge.go:handlePromptComplete` remains the final empty
completion guard. See
[docs/features/cli-thread-recovery-and-empty-response-retry.md](features/cli-thread-recovery-and-empty-response-retry.md).

Orchestration is a native-session relay only. Each run drives one long-lived
Codex app-server thread and one long-lived Claude Code stream-json session on the
selected Bridge, and follow-up turns reuse those native conversations so the user
can `resume` them from the workspace. The Bridge streams CLI output and typed
command events to the browser and carries the previous turn's visible result
forward; it does not inject verifier, remediation, or assessment turns. The
earlier per-turn design (and the external CCB backend) was removed on 2026-05-30.

Bridge registration includes `protocol.RegisterPayload.Capabilities`. Hub keeps
the latest online capabilities in `internal/hub/pool.go` and returns them from
`GET /api/agents`, allowing the frontend to show whether Codex and Claude
orchestration execution and browser approval are available. Hub blocks
orchestration when the selected endpoint cannot execute both CLIs, and blocks
review-required orchestration when the endpoint cannot provide the required
approvals instead of falling back to `codex exec --json`.

Conversation share links are Hub-only public reads. Authenticated users create
share records for chat sessions or orchestration runs; anonymous viewers fetch
sanitized persisted transcripts through `GET /api/public/shares/<share>`. The
Bridge is not contacted for public reads, and the frontend `/share/<share>`
route renders before login bootstrap. Orchestration share sanitization in
`internal/hub/share.go:publicOrchestrationEvents` drops severity events (except
`turn.end`, whose lifecycle status stays public so failed turns close visibly),
internal Bridge notes, `TurnStartData.PromptText`, `RunStartData.CWD`, and all
of `RunEndData` except the worker pair — native resume commands, thread and
session ids, transcript paths, and the run cwd never reach anonymous viewers —
while preserving public run lifecycle and structured conclusion events.

## Decisions

| ID | Decision | Reason |
| --- | --- | --- |
| ADR-001 | Bridge reverse-connects to Hub | Works behind NAT without opening inbound ports |
| ADR-002 | Single Bridge WebSocket with `sid` envelopes | Multiple browser tabs share one long connection |
| ADR-003 | Short-lived Codex runner for v1 | Lower resident memory and simpler crash cleanup |
| ADR-004 | SQLite only on Hub | Single-user persistence without extra services |
| ADR-005 | Embedded native frontend | No Node build, smaller deployment surface |
| ADR-006 | Orchestration continue reuses `runID` | Follow-up tasks keep context through event compaction |
| ADR-007 | Public conversation share links | Anonymous readers can view sanitized transcripts without workspace access |
| ADR-009 | Remote CLI configuration switcher | Hub stores encrypted presets; Bridge probes providers and updates native CLI configuration |

## Protocol

Every Hub-Bridge and browser-Hub frame uses:

```go
type Envelope struct {
    Type    string          `json:"type"`
    Sid     string          `json:"sid,omitempty"`
    Payload json.RawMessage `json:"payload,omitempty"`
}
```

Implemented frame types:

- `register`, `registered`, `heartbeat`
- `open_session`, `session_opened`, `close_session`
- `prompt`, `session_update`, `prompt_complete`
- `approval_request`, `approval_response`
- `cancel`, `error`, `status`
- `orchestration_start`, `orchestration_event`, `orchestration_cancel`
- `agent_shutdown`

Bridge-originated `heartbeat` payloads may include `workingDirs`. Hub treats
that as live endpoint metadata, updates `agents.working_dirs_json`, and still
accepts older heartbeat payloads that only carry a timestamp.

Enrollment-token expiry limits first use. After a token is bound through
`internal/store/store.go:ConsumeEnrollTokenInfo`, only that machine id may reuse
it for reconnects, including after the bootstrap expiry; endpoint deletion
revokes the binding. Hub retains `RegisterPayload.Version` and connection time
only in `internal/hub/pool.go:BridgeConn`, and exposes them for online endpoints
through the authenticated, ownership-filtered agent list.

## Continuity

Chat continuity:

1. Hub loads `sessions.remote_thread_id`.
2. Hub sends it in `open_session`.
3. Bridge stores it in the live session.
4. The saved `remote_thread_id` is passed to the configured runner for
   follow-up prompts. Codex app-server runner paths use `thread/resume`; Codex
   exec runner paths use `codex exec resume <thread-id> -`.
5. Hub persists the latest returned thread id on `prompt_complete`.
6. When the last browser WebSocket for a chat `sid` disconnects, Hub starts an
   in-memory `leaseIdleLeased` timer in
   `internal/hub/browser_lease.go:startBrowserLease`. Reopening the same `sid`
   before `hub.browser_lease_ttl` expires calls
   `internal/hub/browser_lease.go:tryReattach`, sends the existing
   `open_session` frame again, and lets
   `internal/bridge/session.go:Open` rebind the browser output
   channel to the existing Bridge-side session. TTL expiry sends
   `close_session`; explicit session deletion still closes immediately.
   Setting `hub.browser_close_session: true` opts back into the legacy
   close-after-`browser_close_grace` behavior.

Orchestration continuity:

1. New tasks create an `orchestration_runs` row.
2. Follow-up tasks call `/api/orchestrations/{runID}/prompts` and stay on the
   run's original `agentId`; switching CLI endpoint requires an explicit new
   run. The same persisted `worker_pair` and `first_cli` values are reused
   unless the request explicitly changes them.
3. Hub compacts prior `orchestration_events` into context.
4. Hub also restores native CLI state from `orchestration_runs`: the latest
   legacy Codex thread id, Codex thread ids by worker slot, whether Claude
   reached a successful turn, and the locked absolute run cwd reported by
   Bridge. The persisted
   `native_context_compaction` setting is restored with the same run.
5. Bridge receives the same `runID` with `Resume=true`, reuses any live
   run-scoped native sessions, can resume Codex and Claude by persisted native
   ids after restart where supported, and materializes new uploads under the
   locked run cwd.
6. The frontend stores the last selected run id locally and restores it on
   `/orchestrate`.

Interrupted-run recovery: a Hub restart loses every in-memory bridge
connection, so `internal/hub/server.go:recoverInterruptedRuns` runs once at
startup and settles rows the previous process left behind — non-terminal chat
`runs` and queued/running `orchestration_runs` become `failed` with a
restart reason and a `run.error` conclusion event, and `canceling` runs settle
as `canceled`. Follow-up prompts claim a terminal run back to `running`
atomically (`store.ClaimOrchestrationRunForContinue`), so two concurrent
continues cannot double-start the same run on the Bridge.

Chat session isolation:

1. Each `sessions` row stores its owning CLI endpoint in `sessions.agent_id`.
2. The frontend filters the chat sidebar by the selected agent.
3. Switching agents closes the active chat WebSocket and restores that agent's
   remembered session from `codexBridge.activeSessionByAgent`.
4. Sending from an empty agent space creates a new session for that agent.

## Authentication And User Isolation

The bootstrap administrator is created by CLI/config. Operators may explicitly
enable self-service registration with a Cloudflare Turnstile Managed widget.
The browser receives only the public site key from `GET /api/auth/config`; the
Hub validates the single-use token, `action=register`, optional production
hostname, password policy, duplicate username, and a separate registration rate
limit before creating a user and issuing the existing HttpOnly JWT cookie.
Registration is fail-closed and remains disabled unless enabled with both keys.

Agents, sessions, orchestration runs, and conversation shares carry a user id.
Every authenticated HTTP and WebSocket entry loads its parent record using the
JWT subject before child messages, chat runs, or orchestration events can be
read or changed. Every user, including the bootstrap administrator, can use
both chat and orchestration but sees and controls only explicitly owned
endpoints and records. Legacy unowned agents require an explicit ownership
migration before use. Public shares remain an explicit, revocable exception
containing sanitized read-only data.

The bootstrap administrator additionally has one explicit, read-only
cross-user analytics boundary: `GET /api/admin/usage` and
`GET /api/admin/users/{userID}/usage`, plus the on-demand read endpoint
`GET /api/admin/users/{userID}/conversations/{kind}/{conversationID}`. The
overview exposes aggregate activity, endpoint, workload, Token, and
price-estimate fields. User detail adds conversation metadata and usage without
content. The final endpoint checks ownership and returns only message bodies or
the orchestration prompt and visible text events. Workspace paths and native
session identifiers are excluded, and no administrator endpoint grants access
to user-scoped mutation APIs. See
[docs/features/admin-usage-dashboard.md](features/admin-usage-dashboard.md).

## Storage

SQLite tables:

- `users`
- `agents` (`deleted_at` soft-deletes CLI endpoints while preserving history)
- `sessions`
- `messages`
- `runs`
- `enroll_tokens`
- `orchestration_runs` (including persisted mode, `worker_pair`, `first_cli`,
  `profile`, cwd, max turns, status, native CLI continuity state,
  `codex_thread_ids_json`, native context compaction preference, locked runtime
  cwd, and uploaded file metadata)
- `orchestration_events` (including `source`, `severity`, lifecycle status,
  and typed event payload JSON)
- `orchestration_usage_syncs` and `orchestration_usage_events` (private native
  session scan state and normalized per-call counters; no transcript content)
- `orchestration_task_graphs`, `orchestration_tasks`,
  `orchestration_task_dependencies`, and `orchestration_task_attempts`
  (bounded scheduling, dependency state, identity, retry lineage, and evidence)
- `conversation_shares`
- `cli_config_presets` (machine- and CLI-scoped preset metadata plus an API-key
  ciphertext encrypted directly to the target Bridge public key)

Hub stores browser auth, chat history, and only Bridge-targeted API-key
ciphertext. Bridge stores its generated `machine_id`, persistent configuration
switcher decryption key/state, and writes native Codex/Claude credentials on the
private machine. Configuration test/apply/reset frames are capability-gated and
request-correlated; older Bridges continue without exposing this workflow.
