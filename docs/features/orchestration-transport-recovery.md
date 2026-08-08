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
2. OrchestrationManager owns process-lifetime run contexts. A connection reader
   may dispatch a start frame, but the individual reverse WebSocket context is
   never the parent of the native CLI process.
3. Bridge keeps the run context and native CLI process alive, buffering
   orchestration events until a replacement reverse WebSocket is attached.
   The replacement connection starts its writer before managers attach, and
   ordered backlog flushing keeps newly emitted events behind the backlog so a
   long offline period cannot deadlock or reorder terminal state.
4. Hub waits for the maximum Bridge reconnect backoff, possible jitter, and a
   heartbeat window before declaring an offline active run failed.
5. Actual process shutdown, a changed Bridge instance, and explicit
   cancellation retain their terminal behavior. When reconnect grace expires,
   the terminal failure preserves the bounded reverse WebSocket close reason.
6. Hub refuses to start or continue orchestration on a semantically versioned
   Bridge older than `v0.3.1`, the first release with transport-detached run
   ownership. Chat remains available so an outdated endpoint is not presented
   as completely offline, and the rejection tells the user to update Bridge.

## Implementation

- Preserve active orchestration managers when a reverse WebSocket closes.
- Parent each run from manager-owned process lifetime rather than the socket
  reader that delivered its start frame.
- Start the replacement WebSocket writer before flushing buffered events and
  preserve event order while that bounded flush is in progress.
- Gate orchestration dispatch when the connected endpoint predates transport
  recovery, while accepting development/test builds whose versions are not
  release semver strings.
- Size Hub's offline grace window to cover the Bridge retry schedule.
- Verify the socket disconnect path preserves active run handles, run contexts
  are owned by the manager rather than the socket reader, detached events are
  buffered for the replacement connection, and explicit cancellation still
  stops work. Also verify Hub grace calculations, then run the full Go test
  suite.

## Exit Gates

- A short Bridge transport loss does not emit run.cancelled.
- Reconnected Bridge output is persisted and available to the browser's normal
  event reload.
- A pre-`v0.3.1` endpoint receives an actionable orchestration rejection instead
  of accepting a task it will cancel on the next transient transport loss.
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

**Why reject old Bridge versions instead of relying on the current Hub?**

The native CLI is a child of the private Bridge, not the Hub. A current Hub can
wait for reconnect, but it cannot prevent a pre-`v0.3.1` Bridge from canceling
its own process as soon as that old client's WebSocket reader exits.
