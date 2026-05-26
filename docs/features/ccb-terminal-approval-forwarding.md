> **DEPRECATED - CCB is no longer an active orchestration backend**
>
> Current design: [manual-orchestration-rounds.md](manual-orchestration-rounds.md). Historical only; do not implement from this doc.

# CCB Terminal Approval Forwarding

## Goals

- Surface interactive local CCB agent terminal prompts in the browser as the
  existing approval card workflow.
- Handle Codex CLI workspace trust prompts shown in CCB-managed tmux panes, such
  as "Do you trust the contents of this directory?" and "Press enter to
  continue".
- Continue the same orchestration run after approval by sending the accepted
  input to the same CCB agent pane.

## Non-Goals

- Do not replace Claude Code's MCP permission prompt bridge.
- Do not introduce a new WebSocket frame type; reuse
  `internal/protocol:ApprovalRequestPayload` and
  `internal/protocol:ApprovalResponsePayload`.
- Do not create a separate persistence model for approvals.

## Data And Protocol Impact

The Bridge emits `approval_request` for CCB terminal prompts with:

- `kind`: `ccb.terminal_prompt`
- `runId`: the active orchestration run
- `turnId`: the CCB orchestration turn
- `command`: a compact description of the required terminal input
- `cwd`: the orchestration working directory
- `reason`: sanitized console prompt text
- `params`: JSON metadata including `agent`, `paneId`, and `input`

The Hub already routes orchestration approval requests to browsers and
approval responses back to the same agent through
`internal/hub/orchestration.go:handleOrchestrationWS`.

## Implementation Steps

1. Poll CCB agent console panes during structured watch.
2. Detect known terminal confirmation prompts from sanitized console text.
3. De-duplicate prompts by agent, pane, prompt type, and console tail.
4. Send a browser approval request through
   `internal/bridge/orchestration.go:orchestrationApprovalRequester`.
5. On `accept`, send the required key sequence to the same tmux pane.
6. Emit an orchestration event recording that the browser approved and input was
   forwarded.
7. Add an integration test that simulates the Codex trust-directory prompt,
   approves it through the orchestration WebSocket, and verifies the same run
   completes.

## Exit Gates

- Unit tests cover prompt detection and tmux send-key argument construction.
- Integration test covers browser approval request/response routing for a CCB
  terminal prompt.
- Frontend static assets are rebuilt after source changes.
- Go tests pass for the affected packages.

## Reviewer Q&A

Q: Why reuse approval cards instead of adding a CCB-specific prompt frame?

A: The user-visible action is still an approval decision, and existing Hub and
browser code already preserve run routing for `approval_request`.

Q: Why send only `Enter` for the Codex trust prompt?

A: The prompt's default selected option is "Yes, continue" and the local console
explicitly says "Press enter to continue". The Bridge only forwards that key
after browser approval.
