# Orchestration Transport Recovery

## Goal

Keep an active orchestration running through a transient Bridge-to-Hub WebSocket
disconnect. Progress emitted while detached is buffered by the Bridge and sent
after reconnect; the browser's existing run WebSocket reconnect then reloads
persisted events.

## Non-Goals

- Do not resume a process after the Bridge itself has restarted.
- Do not make an explicit user cancellation recoverable.
- Do not add a new protocol frame or persistence schema.

## Runtime Contract

1. Client.connectOnce detaches the old output channel on transport failure
   without calling OrchestrationManager.CloseAll.
2. Bridge keeps the run context and native CLI process alive, buffering
   orchestration events until a replacement reverse WebSocket is attached.
3. Hub waits for the maximum Bridge reconnect backoff, possible jitter, and a
   heartbeat window before declaring an offline active run failed.
4. Actual process shutdown, a changed Bridge instance, and explicit
   cancellation retain their terminal behavior.

## Implementation

- Preserve active orchestration managers when a reverse WebSocket closes.
- Size Hub's offline grace window to cover the Bridge retry schedule.
- Verify bridge disconnect lifecycle and Hub grace calculations with focused
  tests, then run the full Go test suite.

## Exit Gates

- A short Bridge transport loss does not emit run.cancelled.
- Reconnected Bridge output is persisted and available to the browser's normal
  event reload.
- A permanently offline Bridge eventually causes a visible failed run.

## Reviewer Q&A

**Why not leave runs active indefinitely?**

An indefinitely unavailable private machine cannot return a reliable result.
The grace period only covers the configured reconnect policy; after that Hub
records a terminal failure.

**Why is Bridge process restart still terminal?**

The prior native CLI processes and in-memory relay state are no longer owned by
the new process. Treating that state as resumable would risk duplicate or
interleaved execution.
