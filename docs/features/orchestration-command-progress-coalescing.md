# Orchestration Command Progress Coalescing

## Goals

- Keep orchestration responsive when a native command, such as a verbose Coq
  check, emits thousands of output deltas in a short period.
- Preserve complete command output and lifecycle order while reducing the
  number of `command.update` envelopes sent to the Hub.
- Reuse the existing Bridge reconnect and pending-event path for the resulting
  larger progress chunks.

## Non-Goals

- Do not change the orchestration protocol, Hub persistence schema, frontend
  reducer, CLI runner contract, or user configuration.
- Do not coalesce `turn.delta`, command start/end events, approvals, errors, or
  run lifecycle events.
- Do not truncate, summarize, or otherwise discard native command output.

## Runtime Contract

`internal/bridge/orchestration_events.go:emitTool` groups adjacent
`command.update` events by execution, turn, CLI role, and command id. A group is
flushed after a short bounded interval or when its accumulated output reaches a
bounded byte threshold.

Before the matching `command.end` is emitted, the Bridge synchronously flushes
that command's pending progress. Before a turn or run terminal event is emitted,
the Bridge flushes all pending command progress for that execution. Start,
terminal, approval, and error events remain immediate and independent.

The coalescer concatenates output bytes in arrival order. The emitted
`CommandData.Output` and legacy `data.output` fields remain identical, so the
Hub and browser consume the same event shape as before.

## Data And Protocol Impact

- No new envelope or event kind.
- No field or SQLite schema changes.
- A verbose command produces fewer persisted `command.update` rows, each with
  a larger output fragment. Its complete output and terminal state are
  unchanged.

## Implementation Steps

1. Add execution-scoped pending progress state to
   `internal/bridge/orchestration.go:OrchestrationManager`.
2. Buffer and merge only `command.update` payloads in
   `internal/bridge/orchestration_events.go:emitTool`.
3. Flush pending progress before command, turn, and run terminal events and
   when the manager closes.
4. Cover coalescing, command isolation, timer flushing, and lifecycle ordering
   with small unit tests.

## Exit Gates

- Several deltas for one command become one ordered progress event without
  output loss.
- A terminal command event never overtakes buffered progress.
- Different commands do not share buffers.
- Start and terminal events are not delayed or merged.
- Focused Bridge unit tests and documentation lint pass without a load test.

## Reviewer Q&A

**Why coalesce in Bridge instead of the Hub or browser?**

This removes excess work before reverse-WebSocket transport, SQLite writes,
Hub broadcasts, and browser rendering while preserving their existing
contracts.

**Can a Bridge disconnect lose the buffered text?**

The short-lived in-memory chunk is flushed on its timer or lifecycle boundary.
Once flushed it uses the existing pending-envelope reconnect path. Process
termination during the small coalescing window has the same unavoidable
in-memory loss boundary as any event not yet handed to the transport.

**Why is this not configurable?**

The window and byte limit are internal transport safeguards rather than user
policy. Keeping them fixed avoids new setup burden and configuration coupling.

**Why keep SQLite instead of moving the Hub to MySQL?**

The expensive path is event amplification: every progress envelope otherwise
causes its own sequence lookup, insert, run update, and transaction. Coalescing
removes that work before it reaches the already WAL-enabled SQLite store. A
second database service would add memory, migration, and operational cost on
small servers without addressing the upstream transport and rendering load.
