# Durable Bounded Orchestration Task Graph

## Goals

- Persist orchestration scheduling state in the existing Hub SQLite database.
- Preserve exact task/attempt lineage across Hub and Bridge reconnects.
- Represent uncertain delivery as `unknown` and prohibit automatic replay.
- Permit at most two independent worker nodes to execute in isolated writable
  workspaces.
- Serialize integration and require an independent final reviewer before the
  run may complete.
- Require reviewing-turn proof-checker evidence for formal-proof completion.
- Keep the existing collaboration/debate controls, create endpoint, run id,
  follow-up endpoint, event stream, and native-session continuity.
- Keep cross-worker packets compact by referring to persisted evidence rather
  than duplicating full transcripts.

## Non-Goals

- No mailbox daemon, external queue, Redis, or second job store.
- No arbitrary user-authored or model-authored graph topology.
- No more than two parallel workers.
- No parallel writes to the selected project checkout.
- No promise of exactly-once side effects inside arbitrary CLI commands.
  Dispatch and terminal recording are idempotent; filesystem/tool side effects
  remain subject to explicit reconciliation.
- No automatic retry of `unknown`, failed integration, failed verification, or
  canceled work.
- No new browser workflow or DAG editor in the first release.
- No parallel execution for ordinary 1:1 chat.

## Runtime Model

Hub creates one graph per new orchestration run when the selected Bridge
advertises durable task-graph support. The fixed topology is:

```text
worker-a --\
            integrate -> review
worker-b --/
```

The topology is fixed before dispatch. A node becomes `ready` only after every
dependency is `succeeded`. Failure, cancellation, or ambiguity blocks all
dependants. The graph state is derived from its nodes and final reviewer:

| Node state | Meaning |
| --- | --- |
| `pending` | Dependencies are not terminal yet. |
| `ready` | All dependencies succeeded and the node may be claimed. |
| `dispatching` | Hub committed a claim but has no Bridge acknowledgement. |
| `running` | Bridge acknowledged this exact attempt. |
| `unknown` | Delivery/execution may have happened but cannot be proved. |
| `succeeded` | Terminal evidence was stored for this exact attempt. |
| `failed` | Execution or verification failed. |
| `blocked` | A dependency failed, was canceled, or became unknown. |
| `canceled` | User canceled before successful completion. |

Hub enforces a maximum of two active `worker` nodes per graph and only one
active `integrator` or `reviewer`. The final graph cannot become `completed`
unless the reviewer node is `succeeded`.

## Durable Identity And Claims

`orchestration_task_graphs` stores graph ownership and policy. Nodes and edges
live in `orchestration_tasks` and `orchestration_task_dependencies`. Every
dispatch is recorded in `orchestration_task_attempts`.

An attempt contains:

- stable node id and attempt number;
- unique attempt id;
- optional parent attempt id for explicit retry;
- SHA-256 digest of the normalized dispatch payload;
- dispatch, acknowledgement, and terminal timestamps;
- terminal evidence JSON and error text.

`internal/store/task_graph.go:ClaimReadyTask` runs in one SQLite transaction. It
verifies dependencies and concurrency limits, creates the next attempt, and
moves the node from `ready` to `dispatching`. Bridge events must carry the graph,
task, attempt, and digest fields. A mismatched or stale event is stored as an
orchestration warning and cannot advance the node.

## Continuity And Follow-Ups

The graph augments a run; it does not replace it. The existing run id remains
the browser, transcript, approval, and native-session continuity boundary.
`internal/hub/orchestration.go:handleContinueOrchestration` continues the same
run and creates a new graph generation only after the prior generation is
terminal. It never creates a hidden replacement run.

Payloads stay compact. Worker handoffs carry the newest request, node role,
artifact/diff reference, verification evidence, risks, and next action. Native
CLI history remains the primary conversational memory.

## Workspace Isolation And Integration

Bridge creates one private copied task directory per node below Bridge-owned
metadata. Repository metadata (`.git`) and Bridge workspace metadata are
excluded, and paths are derived from Hub-generated node ids.

Workers never merge into the user's selected checkout. The integrator consumes
worker diffs and evidence in a serial integration workspace. Conflicting or
invalid patches fail integration and block review. Cleanup is conservative:
active or unknown attempts retain their workspace for inspection; successful
terminal graphs may remove temporary worktrees after evidence has been stored.

## Reviewer Barrier

Workers and the integrator cannot self-certify completion. The reviewer receives
the integrated result plus bounded evidence references and runs relevant tests.
Its terminal event includes command evidence and a verdict.

For `profile=formal-proof`, `succeeded` additionally requires a successful
reviewing-turn checker command recognized by the existing formal-proof checker
policy (`coqc`, `coqtop`, Rocq, Lean/lake, Isabelle, or configured equivalent).
Missing or failed checker evidence changes the reviewer node to `failed` and the
run cannot report completion.

## Recovery And Retry

At Hub startup:

1. `pending`, `ready`, and never-dispatched queued work is retained.
2. `dispatching` becomes `unknown` because delivery may have succeeded.
3. `running` is reconciled against persisted terminal orchestration evidence.
4. A running attempt without matching terminal evidence becomes `unknown`.
5. Dependants of `unknown`, `failed`, or `canceled` become `blocked`.
6. Only unambiguous `ready` work may be dispatched after Bridges reconnect.

An explicit retry never edits the old attempt. It creates a new attempt with a
new id and digest plus `retry_of_attempt_id`. Retrying an unknown task is a user
decision because the previous attempt may already have changed external state.

## Data And Protocol Impact

- Add three graph tables and store CRUD/claim/recovery methods.
- Extend orchestration start and event data with optional graph/task/attempt
  identity and payload digest. Older Bridges ignore absent graph metadata and
  Hub uses the conservative serial topology for them.
- Persist task identity in orchestration events for terminal reconciliation.
- Do not expose graph payloads, queued prompts, private paths, or attempt
  evidence through public share responses.
- Add the opt-out `bridge.durable_task_graph` configuration (and
  `BRIDGE_DURABLE_TASK_GRAPH` environment variable) for rolling upgrades with
  older Bridge binaries. It defaults to `true`; disabling it selects the
  existing conservative serial relay path and does not alter browser/API
  continuity.
- Existing HTTP routes and frontend request shapes remain valid.

## Implementation Steps

1. Add schema, typed store records, transactional claims, idempotent ack/finish,
   dependency propagation, retry lineage, and restart recovery.
2. Create a conservative graph when an orchestration run is created or
   continued and link dispatch metadata to the existing start payload.
3. Persist and reconcile graph-aware Bridge events without changing browser
   event rendering.
4. Add Bridge workspace preparation and bounded worker execution, followed by
   serial integration and reviewer execution.
5. Reuse formal-proof checker recognition to enforce the reviewer barrier.
6. Add store race/idempotency tests, Hub recovery tests, Bridge isolation tests,
   and end-to-end orchestration tests.
7. Sweep architecture, code map, change-impact rules, and roadmap status.

## Exit Gates

- A task claim is atomic and concurrent claims cannot create two attempts for
  the same node.
- Duplicate acknowledgement and terminal events are idempotent.
- A mismatched attempt id or payload digest cannot complete a node.
- Restart recovery preserves ready work and converts ambiguous work to
  `unknown` without redispatch.
- Retry preserves the original attempt and records parent lineage.
- No graph has more than two running worker nodes.
- Parallel workers receive distinct writable workspace paths.
- Integration and review never overlap and review starts only after successful
  integration.
- The run cannot complete without reviewer success.
- Formal-proof review cannot succeed without successful checker evidence.
- Follow-ups reuse the same run id, native thread metadata, and locked run cwd.
- Public share payloads contain no private graph/attempt data.
- `/usr/local/go/bin/go test ./...`
- `make doc-lint`
- `CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .`

## Reviewer Q&A

**Why not add a mailbox daemon?**

Hub already owns authentication, routing, run state, events, and the only
SQLite database. A daemon would create two authorities and require a delivery
protocol between them before it adds user value. The graph tables provide the
needed durability without another process.

**Does exactly-once mean a shell command runs exactly once?**

No. The system provides one durable identity and idempotent state transitions
per attempt. Arbitrary external side effects cannot be made exactly-once without
tool-specific transactions, which is why ambiguous attempts stop at `unknown`.

**Why a fixed graph?**

It bounds CPU, token use, conflict risk, and recovery states. Dynamic topology
can be considered after fixed-graph evidence shows it is needed.

**Why keep the same run id for follow-ups?**

The run id owns browser continuity, persisted events, native CLI thread ids,
approvals, and formal-proof workspace state. Replacing it would silently break
the product's continuity invariant.
