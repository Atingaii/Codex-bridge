# Orchestration Deep Collaboration And Approval

## Goal

Improve orchestration so Claude Code and Codex behave like two coordinated CLI
agents instead of a loose alternating transcript, while keeping inter-agent
messages compact. Also make review-required orchestration approval consistent
across Claude Code and Codex: Claude uses its MCP permission prompt bridge, and
Codex uses the same `codex app-server` turn execution path as browser chat.

This design was informed by
`https://github.com/SeemSeam/claude_codex_bridge` at commit
`f4e98410e876ff15c4426bbfac11e4b0c5ff1e18`. The useful patterns are named
agents, explicit sender/target messages, mailbox-style lineage, callback edges,
and an async guardrail that avoids repeated polling.

## Non-Goals

- Do not import the external project's tmux panes, mailbox database, or daemon.
- Do not add a second persistence layer or queue.
- Do not add new WebSocket frame types; reuse `approval_request` and
  `approval_response`.
- Do not change orchestration continuity. Follow-up prompts still use
  `POST /api/orchestrations/{runID}/prompts`.
- Do not make auto-execute safer than its current trusted-machine contract.
- Do not silently downgrade a review-required orchestration path to a runner
  that cannot surface browser approval.

## Current State

- `internal/bridge/orchestration.go:run` alternates Claude and Codex turns.
- `internal/bridge/orchestration.go:composeOrchestrationPrompt` uses compact
  `Msg:` and `Handoff:` lines, but the Bridge can still store those fields as
  typed turn state to avoid reparsing or carrying unnecessary transcript text.
- Chat approvals flow through `internal/protocol.TypeApprovalRequest` and
  `internal/protocol.TypeApprovalResponse`.
- `internal/hub/orchestration.go:handleOrchestrationWS` routes browser
  approval responses back to the owning Bridge when the approval is run-scoped.
- Claude Code supports `--permission-prompt-tool` for non-interactive mode. The
  official CLI reference documents it as an MCP tool used for permission
  prompts; the installed CLI accepts the flag even though `claude --help` omits
  it.

## Design

### Compact Collaboration Contract

Each turn gets an explicit identity and recipient:

```text
From: <role>/<cli>
To: <next-role>/<next-cli|user>
Mode: <collaboration|debate>
```

Visible responses should end with two short machine-scannable lines:

```text
Msg: to=<next-role|user>; intent=<implement|review|challenge|final>; need=<one request or none>
Handoff: status=<needs_next|blocked|resolved>; changed=<files or none>; verified=<commands or none>; next=<one action>; risks=<open issue or none>
```

`Msg:` borrows the external project's explicit route idea without adding a
mailbox. `Handoff:` remains for compatibility and early-stop detection. Prior
turns are compacted to one line per turn with role, CLI, `Msg`, `Handoff`, and
up to two verified commands.

Collaboration mode uses builder-reviewer checkpoints:

- Claude implementer owns the first concrete change.
- Codex reviewer independently verifies, fixes small issues, or sends the exact
  next action back.
- `status=resolved` is only valid with a user-visible final conclusion.

Debate mode uses thesis-critic checkpoints:

- Claude proposer states a testable thesis or patch.
- Codex critic attempts falsification with files or command evidence.
- The debate resolves only when the critic cannot falsify the latest thesis and
  provides final risk notes.

### Browser Approval For Orchestration

The Bridge keeps per-run pending approvals in
`internal/bridge.OrchestrationManager`. A request is emitted as
`approval_request` with `payload.runId` and `payload.turnId`; there is no chat
`sid`.

Hub routing:

- `internal/hub/ws_bridge.go:handleBridgeEnvelope` decodes approval requests.
- If `payload.runId` is present, Hub broadcasts the frame to
  `Pool.BroadcastToOrchestrationBrowsers`.
- Otherwise it keeps the existing chat-session broadcast.
- `internal/hub/orchestration.go:handleOrchestrationWS` accepts
  `approval_response`, validates the decision, loads the run for ownership and
  agent id, and sends the response to the owning Bridge agent.

Frontend:

- `frontend/src/app/App.tsx:OrchestrationWorkspace` stores run-scoped approval
  cards in memory.
- The orchestration WebSocket handles `approval_request` frames and renders the
  same approval UI used by chat inside the timeline.
- Clicking allow/deny sends `approval_response` on the run WebSocket.

Claude Code:

- Review-required Claude turns start with `--permission-mode default` and
  `--permission-prompt-tool mcp__codex_bridge__browser_approval`.
- The Bridge writes a short-lived MCP config for that turn and runs a local
  stdio MCP server command:
  `codex-bridge claude-approval-mcp --socket ...`.
- The MCP subprocess forwards Claude's permission input to the parent Bridge
  over that Unix socket; the parent calls
  `OrchestrationManager.RequestApproval`.
- Accepted browser decisions return an allow response; decline/cancel returns a
  denial response. The exact MCP response keeps the output small and includes a
  conservative compatibility shape so Claude Code can interpret it across minor
  CLI releases.

Codex:

- Review-required Codex orchestration turns call
  `internal/bridge/appserver_runner.go:Prompt` instead of
  `codex exec --json`.
- The orchestration manager passes `RunnerRequest.RunID`, the per-turn id in
  `RunnerRequest.PromptID`, the selected working directory, and an
  `orchestrationApprovalRequester`.
- `CodexAppServerRunner` maps app-server approval callbacks into the existing
  `approval_request` frame. Because the request contains `runId`, Hub and the
  browser treat it as run-scoped, not chat-session scoped.
- Auto-execute remains on `codex exec --json` with the existing dangerous
  bypass flags.

Capability policy:

- Bridge registration advertises runtime capabilities on
  `protocol.RegisterPayload.Capabilities`. Hub stores the latest online
  capabilities in memory with the agent connection and returns them from
  `GET /api/agents`.
- The orchestration UI renders a small capability matrix for the selected
  endpoint, including whether Claude and Codex browser approval are available
  for orchestration.
- `internal/hub/orchestration.go:validateOrchestrationCapabilities` rejects
  review-required orchestration if the selected online Bridge does not advertise
  the browser approval capability required by both participating CLIs. This
  prevents silent fallback to `codex exec --json`.

### Structured Handoff State

Each turn record keeps parsed handoff data:

- `Msg` and `Handoff` preserve the visible compatibility lines.
- `HandoffFields` stores `status`, `changed`, `verified`, `next`, and `risks`
  parsed from the `Handoff:` line.
- Prompt construction uses these fields first, plus short command summaries, so
  the next CLI receives only the necessary continuity payload. It falls back to
  compact visible text only when no structured line is available.

### Conditional Final Verifier

The orchestration loop may add one lightweight verifier turn after the scheduled
turns. It is skipped when there are no file changes and no failed tests or tool
errors, or when an explicit `status=resolved` handoff already includes
verification.

The verifier uses the opposite CLI from the last scheduled turn where possible,
receives the compact structured handoffs, and is instructed to inspect only the
reported changes, failed commands, and unresolved risks. It must not make broad
new changes unless a small fix is required to unblock verification.

## Data And Protocol Impact

- No SQLite schema change.
- No new `internal/protocol.Envelope` type.
- Existing `ApprovalRequestPayload` already has `runId` and `turnId`.
- `ApprovalResponsePayload` remains keyed by `requestId`.
- Orchestration approval cards are transient browser state. They are not
  persisted in `orchestration_events` because the approval frame itself is the
  actionable state, and command events still persist after a tool runs.
- `RegisterPayload.Capabilities` is an additive JSON field. Older Bridges omit
  it and are treated as lacking review-required orchestration browser approval.

## Implementation Steps

1. Add run-scoped approval bookkeeping to
   `internal/bridge/orchestration.go:OrchestrationManager`.
2. Add a `claude-approval-mcp` subcommand in `main.go`.
3. Make Claude review-required turns load a temporary MCP config and
   `--permission-prompt-tool`.
4. Route orchestration approval requests and responses in Hub.
5. Render and answer orchestration approval cards in the frontend.
6. Tighten collaboration and debate prompts around `Msg:` plus compact
   `Handoff:`.
7. Add Codex app-server orchestration turns for review-required mode.
8. Add Bridge capability advertisement, Hub capability guards, and frontend
   capability matrix rendering.
9. Store parsed handoff fields and use them in compact prior-turn prompts.
10. Add a conditional final verifier turn.
11. Update permission, architecture, code-map, and plan docs.

## Exit Gates

- `frontend && npm run build`
- `/usr/local/go/bin/go test ./...`
- `CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .`
- `make doc-lint`
- Browser smoke: orchestration WS renders Claude and Codex approval cards,
  clicking a decision sends `approval_response`, the selected endpoint shows a
  capability matrix, and endpoint switching still keeps isolated orchestration
  spaces.

## Reviewer Q&A

**Q: Why not implement the external project's mailbox kernel directly?**

The current product already has Hub persistence, run-scoped WebSockets, and a
single-user workflow. A mailbox would duplicate storage and increase token
surface. The borrowed part is the useful contract: named agents, targetable
messages, and lineage.

**Q: Does browser approval make Claude as granular as Codex chat?**

It uses Claude Code's own permission prompt callback. Granularity is therefore
bounded by the tool input Claude Code sends, but the user's browser decision now
controls the turn instead of a hidden local prompt.

Claude Code expects the MCP permission-prompt tool result to be a single text
block containing a JSON decision such as `{"behavior":"allow","updatedInput":{}}`
or `{"behavior":"deny","message":"..."}`. Returning prose in that text block is
invalid and prevents the browser approval card from unblocking the turn.

Codex app-server approval responses use the upstream session-scoped acceptance
values (`acceptForSession`, `approved_for_session`, and `scope=session`) when a
browser user approves. This still requires the explicit first browser click, but
lets Codex reuse its same-session approval cache for matching later prompts
instead of asking for the same directory or command repeatedly.

**Q: Why block review-required orchestration when capabilities are missing?**

Review-required is a user-visible safety promise. If Codex would fall back to
`codex exec --json`, the browser could show a safer profile while the private
Bridge cannot actually ask for approval. Blocking is clearer than a silent
downgrade.

**Q: What happens if no browser is connected?**

The Bridge waits on the approval request until the turn context is canceled or
Claude Code times out the MCP tool call. The run remains visible as active, and
the browser can answer after reconnecting if the request is still pending.
