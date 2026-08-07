# CLI Thread Recovery And Empty-Response Retry

## Goals

- Complete ordinary chat turns when a CLI streamed assistant text but omitted
  the terminal response field.
- Recover once from an empty or interrupted terminal response by continuing the
  same native CLI thread instead of replaying the original user prompt.
- Preserve command side effects, approvals, Hub `sid`, run identifiers, and
  native thread continuity.
- Bound recovery memory and latency so reliability does not add persistent
  workers, goroutines, or unbounded context.

## Non-Goals

- No automatic replay of the original prompt after a turn may have started.
- No retries for cancellation, deadline expiry, attachment errors, approval
  decisions, or validation errors.
- No WebSocket, HTTP, SQLite, frontend, or configuration changes.
- No change to orchestration scheduling. Orchestration already applies its own
  bounded same-turn continuation in
  `internal/bridge/orchestration.go:runRelayTurnWithContinuations`.

## Design

`internal/bridge/session.go:Prompt` records bounded evidence while forwarding
updates immediately. It keeps assistant text only up to the Hub's existing
message limit and at most six compact command events. It does not retain full
command output streams or raw runner event objects.

Resident ACP session open/load preparation may retry once before any prompt is
submitted. The first prompt attempt then follows the existing runner dispatch:

- A non-empty `RunnerResult.Content` completes normally.
- An empty terminal field with streamed assistant text completes from that
  text. This repairs protocol-tail loss without another CLI invocation.
- Cancellation and deadline errors terminate immediately.
- An empty response or runner error may trigger one continuation only when a
  native `remote_thread_id` is available.

The continuation is a new short instruction in the same CLI thread. It states
that the preceding turn may already have changed the workspace, includes only
bounded visible-text and command summaries, and tells the CLI to inspect the
current state, avoid repeating completed commands, and return only the missing
final response. The original prompt is not replayed. If continuation also
returns no assistant text, Bridge emits the existing `EMPTY_RESPONSE` or
`RUNNER_ERROR` frame.

Resident ACP sessions use the same already-open session. One-shot Codex runners
receive the persisted native thread id. A continuation that returns a newer
thread id updates the same Bridge session; it never creates a new Hub session or
run.

## Data And Protocol Impact

- No payload shape changes. Existing `session_update`, `prompt_complete`, and
  `error` frames are reused.
- The final `prompt_complete.content` contains the accumulated assistant text
  from both invocations, while streamed updates remain incremental.
- `remote_thread_id`, `run_id`, and `prompt_id` remain attached to the same
  request lifecycle.

## Implementation Steps

1. Add a bounded attempt recorder around ordinary chat runner updates.
2. Retry resident session preparation once before prompt submission.
3. Recover final content from streamed assistant updates.
4. Add one same-thread continuation for recoverable terminal failures.
5. Cover success, retry bounds, cancellation, command evidence, and continuity
   with focused Bridge tests.

## Exit Gates

- Streamed text with an empty terminal result completes without another prompt.
- Empty first attempts continue exactly once on the same native thread.
- Continuation prompts do not contain or replay the original user prompt.
- Tool evidence produces a recovery instruction that forbids duplicate work.
- Cancellation is never retried.
- `/usr/local/go/bin/go test ./...`, `make doc-lint`, and `git diff --check`
  pass.

## Reviewer Q&A

### Why not retry the original prompt when there was no text?

A CLI may execute tools before losing its final response, and some transports
cannot prove that no side effect occurred. Continuing in the same thread lets
the model inspect current state and avoids blind duplicate commands.

### Why keep the policy fixed instead of configurable?

One bounded continuation addresses transient empty tails without creating a
retry policy surface or surprising latency. Repeated failure remains visible to
the user through the existing error frames.

### Why complete directly from streamed text?

The browser and Hub have already received that assistant text. Treating a
missing duplicate terminal field as failure discards useful output and causes a
false empty-response error.
