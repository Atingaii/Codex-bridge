# Durable Task-Graph Startup Reconnect Grace

## Goals

- Let a Bridge reconnect after a brief Hub restart without changing an active
  orchestration task or its attempt lineage to `unknown` or `failed`.
- Keep the Hub available while waiting for reverse-WebSocket reconnection.
- Retain the conservative ambiguity rule after the bounded grace period: work
  whose owning endpoint did not return is not replayed automatically.
- Preserve existing disconnect recovery, cancellation, deletion, and
  non-durable orchestration behavior.

## Non-Goals

- No automatic replay, retry, or synthetic completion of an active CLI task.
- No Bridge protocol, database schema, or browser API change.
- No requirement to restart or update Bridge services for a Hub-only release.

## Runtime Model

`internal/hub/server.go:Server.Run` starts listening first, then starts one
bounded boot-recovery timer using the existing effective Bridge reconnect grace
from `internal/hub/ws_bridge.go:bridgeReconnectGrace`. A reconnecting Bridge
registers normally and keeps its in-memory manager running, so buffered task
events can be delivered to the new Hub connection.

When the timer expires, recovery considers only endpoint ids that owned work
that could not be resumed by ordinary ready-task dispatch:

1. active chat runs;
2. active non-graph orchestration runs; and
3. durable graph tasks in `dispatching` or `running`.

An endpoint online at expiry is left unchanged. For an endpoint still offline,
durable active attempts become `unknown` and their dependants become blocked;
then the existing per-agent terminal recovery records the owning run failure.
No task is replayed. A durable graph containing only `ready` work is retained:
when its Bridge eventually reconnects, the existing reconnect dispatch path
can send that unambiguous work exactly once.

## Data And Protocol Impact

- Add store queries scoped by endpoint identity for boot recovery selection and
  durable active-attempt ambiguity recovery.
- Reuse existing task, attempt, run, event, and WebSocket data. No migration
  and no protocol fields are required.

## Implementation Steps

1. Move Hub boot recovery behind the existing reconnect grace and start the
   HTTP listener before that timer begins.
2. Select pending recovery endpoints from persisted active work.
3. Skip endpoints currently registered in the Hub pool; conservatively recover
   only the remaining endpoints.
4. Add focused tests for reconnect preservation and offline expiry, then run
   the full Go and documentation checks.

## Exit Gates

- A task that reconnects inside the grace stays `running` with its same attempt
  id and its top-level run stays active.
- An offline active durable task becomes `unknown`, blocks dependants, and its
  run receives the existing terminal failure evidence.
- Ready-but-never-dispatched graph work is not converted to `unknown`.
- No automatic duplicate task dispatch occurs during recovery.
- Existing tests, `/usr/local/go/bin/go test ./...`, `make doc-lint`, and the
  release build pass.

## Reviewer Q&A

**Why not recover all work immediately and let a reconnect fix it?**

An immediate terminal transition rejects late terminal evidence and destroys
the user-visible continuity before the Bridge has had a chance to reconnect.
The Bridge already reconnects with bounded backoff and buffers events, so the
Hub must give that mechanism time first.

**What happens if the private endpoint never returns?**

After the bounded grace, active delivery is treated as ambiguous and is never
replayed automatically. This keeps side-effect safety while ensuring a stale
run does not falsely look healthy.
