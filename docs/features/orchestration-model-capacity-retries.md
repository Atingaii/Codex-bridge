# Orchestration Model-Capacity Retries

## Goals

- Recover one orchestration CLI turn when the provider explicitly reports that
  the selected model is at capacity.
- Keep the original run, turn ID, workspace, prompt, and native-session
  continuity contract intact.
- Make the waiting period, retry number, and exhausted retry budget visible in
  the authenticated orchestration timeline.
- Stop waiting promptly when the user cancels the run.

## Non-Goals

- Do not retry authentication failures, invalid model names, arbitrary command
  errors, or context cancellation.
- Do not create a new run, change the requested number of relay turns, or
  switch to a different model automatically.
- Do not add a queue, database field, HTTP endpoint, or WebSocket frame.

## Data And Protocol Impact

The existing `turn.delta` event carries Bridge-originated warning, info, and
error notices. Its existing `data` map records `category`, retry count, retry
budget, and wait seconds. Existing Hub persistence and frontend Bridge-notice
rendering preserve these events; no schema or typed payload change is needed.

## Implementation Steps

1. Recognize explicit model-capacity errors at the relay CLI boundary.
2. Retry the affected CLI invocation at most three times with 10-second,
   30-second, and 60-second waits, while observing context cancellation.
3. Reset only the affected native interactive CLI session after a capacity
   failure, then replay the same turn prompt after the wait.
4. Emit persisted timeline notices before waiting, before retrying, and when
   the retry budget is exhausted.
5. Cover recognition, success after retry, exhaustion, and cancellation.

## Exit Gates

- A capacity error retries the same turn with visible bounded backoff.
- A successful retry completes normally without opening another run.
- Exhaustion preserves the original provider error as the terminal reason.
- Cancellation during a wait prevents another CLI invocation.
- Focused Go tests, full Go tests, frontend build, document lint, and a
  deployed health check pass.

## Reviewer Q&A

**Why retry only capacity failures?**

Capacity is a temporary provider condition. Retrying invalid credentials or a
bad command would obscure a real user-actionable failure and waste execution.

**Why reset the interactive session?**

The CLI may have terminated or left its transport unusable after the provider
response. Resetting only that CLI session lets the next invocation restore the
same run context without affecting the paired agent or workspace.

**Why show two notices per retry?**

The wait notice tells the user that progress is intentionally paused and for
how long. The start notice distinguishes an active retry from a stalled timer.
