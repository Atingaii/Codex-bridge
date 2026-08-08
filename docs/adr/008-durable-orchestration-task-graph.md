# ADR-008: Durable Orchestration Task Graph

## Background

The current orchestration run is durable only as a transcript. Hub stores the
run and its events, but dispatch ownership and active execution live in memory.
A Hub restart therefore marks active work failed, and a delivery timeout cannot
distinguish "Bridge never received this task" from "Bridge received it and the
acknowledgement was lost". The relay is also a single alternating schedule, so
independent investigation work cannot overlap safely.

The earlier [persistent queue design](../features/persistent-task-queue.md)
specified serial follow-ups but deliberately excluded parallel execution. That
constraint no longer meets the orchestration reliability and bounded-parallel
work requirements.

## Decision

Hub SQLite remains the only durable authority. Add task graph, dependency, and
attempt rows to the existing database; do not add a mailbox daemon, broker, or
second job store. Hub owns graph state transitions and Bridge owns private
workspace execution.

Every new orchestration run handled by a graph-capable Bridge gets one small,
fixed backend graph: two independent candidate nodes, one integration node, and
one reviewer node. At most two candidates run concurrently. Older Bridges keep
the existing serial relay. The browser continues to create and continue the
same orchestration run through the existing endpoints and does not edit graph
topology.

Each dispatch has a stable task id, a monotonically increasing attempt number,
an attempt id, and a SHA-256 payload digest. The transactional Hub transition is
`ready -> dispatching`; Bridge acknowledgement advances it to `running`, and a
terminal node event advances it once to a terminal state. Duplicate events are
idempotent by attempt id. A dispatch whose outcome cannot be proved becomes
`unknown`. Unknown work is fail-closed: Hub blocks dependants and never blindly
replays it. An explicit retry creates a child attempt with `retry_of_attempt_id`
rather than mutating historical evidence.

Parallel workers never share a writable checkout. Bridge prepares one private
copied task directory per node and excludes repository metadata and Bridge
workspace metadata. Integration is deterministic and
serial. The final reviewer operates on the integration workspace and is the
only node allowed to complete the graph. Formal-proof completion additionally
requires successful checker evidence from the reviewer turn.

On Hub restart, queued and ready nodes remain dispatchable, `dispatching` nodes
become `unknown`, and `running` nodes are reconciled from persisted run/node
terminal evidence. Without such evidence they also become `unknown`. This
preserves ambiguity instead of turning a process restart into implicit retry.

## Trade-offs

The fixed graph gives up model-selected fan-out and broad parallelism. In
return, concurrency, token use, filesystem isolation, and recovery behavior are
auditable. Two parallel workers can reduce wall-clock time for independent work
but can consume more total model tokens and CPU, so parallelism is opt-in from
backend classification and capped at two.

SQLite remains a single-connection coordination point. That is intentional for
the current single-machine Hub scale and makes claims easy to reason about. A
future distributed Hub would require a new ADR and a different claim protocol.

Cross-process recovery is durable orchestration recovery, not transparent
continuation of an arbitrary killed CLI process. A task with unprovable remote
state stops at `unknown` until reconciled or explicitly retried.

## Code Anchors

- `internal/store/store.go:Migrate`: graph, dependency, and attempt schema
- `internal/store/task_graph.go:ClaimReadyTask`: transactional dispatch claim
- `internal/store/task_graph.go:RecoverTaskGraphs`: restart reconciliation
- `internal/hub/orchestration.go:startOrchestration`: graph-linked dispatch
- `internal/protocol/envelope.go:OrchestrationStartPayload`: task/attempt identity
- `internal/bridge/orchestration.go:OrchestrationManager`: isolated execution and
  reviewer barrier
- `docs/features/durable-bounded-orchestration-task-graph.md`: behavior and exit
  gates

## Revisit When

- A deployment needs more than one Hub writer or cross-host scheduling.
- Workload evidence justifies more than two parallel workers.
- Native CLI runtimes expose a durable attach/reconcile protocol.
- Users need to author or inspect arbitrary DAG topology.
