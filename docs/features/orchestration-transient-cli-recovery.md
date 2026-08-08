# Orchestration Transient CLI Recovery

## Goals

- Keep an orchestration turn alive while Codex reports native
  `Reconnecting N/M` progress instead of treating the first progress event as
  a terminal error.
- Recover boundedly from provider capacity failures and Codex stream transport
  failures without opening a new run, task attempt, workspace, or native
  conversation.
- Preserve command side effects and partial assistant output by continuing from
  the current native thread instead of blindly replaying work after a stream
  interruption.
- Persist wait, retry-start, native reconnect, success, and exhausted-budget
  notices so users can distinguish active recovery from a stalled run.
- Keep capacity, transport, missing-final, cancellation, and permanent CLI
  failures as separate classes with independent handling.
- Treat Codex `thread ... already has an active writer` as a temporary native
  thread lease conflict after process/Bridge restart, without losing or
  rewriting the prompt that Codex has not accepted yet.

## Non-Goals

- Do not retry authentication, authorization, invalid-model, validation,
  approval-denial, command, or proof failures.
- Do not switch models or CLIs automatically.
- Do not create another orchestration run or durable task attempt.
- Do not promise recovery after the configured bounded budgets are exhausted.
- Do not add a Hub API, WebSocket frame, database column, or configuration
  surface.

## Recovery Model

`internal/bridge/appserver_runner.go:readEvents` treats a
scoped `Reconnecting N/M` error as native progress. It emits an internal runner
notice and keeps reading the same app-server process for a bounded grace
window. A subsequent turn event clears the reconnect timer. Process exit or
grace expiry returns the concrete transport reason to the orchestration layer.

`internal/bridge/orchestration.go:runRelayTurnWithContinuations` classifies the
returned result:

| Class | Recovery |
| --- | --- |
| model capacity | reset the affected CLI process, wait 10/30/60 seconds, then replay the same prompt because the provider rejected it before useful work |
| Codex active writer | keep the same native thread and original prompt, wait 5/15/30/60 seconds for the prior turn lease to clear, then submit it again |
| Codex stream transport | preserve cumulative text and command evidence, reset only the affected app-server process, wait 5/15/30 seconds, resume the same native thread with a compact current-state recovery prompt |
| missing final conclusion | continue the same turn with the existing bounded conclusion prompt |
| cancellation/deadline | stop immediately |
| permanent CLI or command failure | report the concrete error without retry |

Active-writer, transport, and missing-final retries have separate counters. An
active-writer response cannot be merged with stream recovery because Codex
rejected `turn/start` before accepting the prompt; that class must retry the
unchanged prompt. A transport failure during a conclusion continuation does
not consume the remaining conclusion budget. Recovery decisions use the entire
accumulated turn record, not only output from the most recent invocation.

## Data And Protocol Impact

No wire or persistence shape changes are required. Bridge-originated
`turn.delta` events use the existing `severity`, `error`, and `data` fields.
The `data.category` values are:

- `codex-native-reconnect-progress`
- `cli-transport-retry-wait`
- `cli-transport-retry-start`
- `cli-transport-retry-exhausted`
- `codex-thread-busy-retry-wait`
- `codex-thread-busy-retry-start`
- `codex-thread-busy-retry-exhausted`
- existing `model-capacity-retry-*` and `turn-continuation-*` values

All notices retain the same run ID, turn ID, durable attempt reference, worker
slot, workspace, and native thread ID.

## Implementation Steps

1. Recognize only explicit Codex reconnect/stream-close messages as recoverable
   transport failures.
2. Keep reading app-server events during native reconnect progress with a
   bounded timer and surface each progress message to orchestration.
3. Base interrupted-turn recovery on the accumulated turn record.
4. Add a separate 5/15/30/60-second active-writer budget that preserves the
   original unaccepted prompt and native thread.
5. Join the rejected submission's event reader before retrying so it cannot
   consume events emitted for the next accepted turn.
6. Add a separate 5/15/30-second transport retry budget using a compact
   same-thread recovery prompt.
7. Reset only the failed native CLI process before resuming its persisted
   thread.
8. Cover recognition, native reconnect success, active-writer recovery,
   exhaustion, permanent failures, and cancellation.

## Exit Gates

- `Reconnecting 1/5` alone cannot immediately fail an orchestration.
- Native reconnect success completes without starting another CLI invocation.
- A terminal stream loss visibly retries on the same run, turn, task attempt,
  workspace, and Codex thread.
- Already observed commands are not blindly replayed.
- Capacity and transport budgets do not consume missing-final continuations.
- Active-writer retry preserves the original prompt because `turn/start` did
  not accept it, and its budget is independent from transport recovery.
- Exhaustion retains the last concrete provider/transport reason.
- Focused Bridge tests, full Go tests, build, document lint, and diff checks
  pass.

## Reviewer Q&A

### Why not replay the original prompt after a disconnect?

The model may already have edited files or run commands before the stream was
lost. Resuming the native thread with bounded evidence lets it inspect current
state and finish without duplicating side effects.

### Why may capacity replay the prompt?

An explicit capacity rejection occurs before model work begins. It is the one
class where replaying the same request is both useful and materially safer.

### Why wait for Codex native reconnect before Bridge recovery?

Codex already owns the provider stream and may repair it without losing the
active turn. Bridge-level recovery is a fallback after that native mechanism
actually fails, not a competing retry loop.
