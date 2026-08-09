# Durable Bounded Orchestration Task Graph

## Goals

- Persist orchestration scheduling state in the existing Hub SQLite database.
- Preserve exact task/attempt lineage across Hub and Bridge reconnects.
- Represent uncertain delivery as `unknown` and prohibit automatic replay.
- Preserve durable candidate, integration, and review scheduling in the
  user-selected writable project directory.
- Serialize integration and require an independent final reviewer before the
  run may complete.
- Treat the user-selected `max_turns` value as a whole-run collaboration-round
  budget. Each graph generation consumes one round and the Hub automatically
  schedules the next generation until that budget is exhausted.
- Require reviewing-turn proof-checker evidence for formal-proof completion.
- Keep the existing collaboration/debate controls, create endpoint, run id,
  follow-up endpoint, event stream, and native-session continuity.
- Keep cross-worker packets compact by referring to persisted evidence rather
  than duplicating full transcripts.
- Give every node the full agency of a real engineering handoff: inspect,
  change, test, and continue through viable approaches instead of stopping
  after one small slice or one failed probe.
- Allow a node to hand off an unresolved blocker only after the materially same
  obstacle repeats without new evidence, and require the handoff to preserve
  the exact failure, attempted approaches, commands, and next executable entry
  point.

## Non-Goals

- No mailbox daemon, external queue, Redis, or second job store.
- No arbitrary user-authored or model-authored graph topology.
- No more than two parallel workers.
- No parallel writes to the selected project checkout; graph nodes are serial
  when using its shared directory.
- No promise of exactly-once side effects inside arbitrary CLI commands.
  Dispatch and terminal recording are idempotent; filesystem/tool side effects
  remain subject to explicit reconciliation.
- No automatic retry of `unknown`, failed integration execution, canceled
  work, or malformed reviewer output. A valid reviewer verdict that more work
  remains advances the collaboration round; it is not an attempt retry.
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

Hub persists a bounded graph and dispatches candidate A, candidate B,
integration, and review in order. This deliberately trades candidate parallelism
for direct, visible work in the user's selected checkout. The final graph cannot
become `completed` unless the reviewer node is `succeeded`.

One complete graph generation is one user-visible collaboration round. The
configured `max_turns` is the number of generations, not the number of internal
nodes and not a per-node Bridge turn limit. Every node remains a single bounded
CLI assignment, while the Hub retains the whole-run round budget. A run
configured for four rounds therefore executes up to four generations of the
fixed graph and carries each review handoff into the next generation.

The internal `maxTurns=1` assignment is only a runner envelope for one graph
node. Prompt composition and browser rendering must use task-graph
`round`/`maxRounds` for user-visible round semantics. A node must never be told
that it is the first or final collaboration turn merely because its internal
runner envelope is `turn 1/1`. Task-graph `generation` is durable lineage and is
also not a user-visible round number because recovery or migration generations
may exist.

The Hub does not stop early merely because an intermediate reviewer reports the
current workspace resolved. Later configured rounds independently challenge,
improve, and re-review that result. Only the reviewer in the final configured
generation may complete the run.

Within every round, nodes maximize useful progress before handing off:

- candidate A selects and pursues the strongest viable implementation path;
- candidate B starts from the actual workspace and candidate A evidence, then
  improves it or pursues a materially different path instead of repeating the
  baseline scan;
- the integrator resolves the candidates into the checkout and continues
  closing remaining gaps instead of only summarizing them;
- the reviewer independently falsifies the result and may directly make safe
  in-scope fixes before reporting the remaining state.

A single failed command, a long checker invocation, or an incomplete proof
slice is not by itself a handoff condition. A node hands off an unresolved
obstacle only when the same normalized blocker has recurred and available
alternative paths no longer produce new evidence. That handoff names the exact
goal and failure location, reproducing command and result, attempted approaches
and why they failed, changes already made, ruled-out assumptions, and one
concrete next entry point. This is advisory progress state, not a run failure.

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

Automatic configured-round advancement also keeps the same run id. After a
reviewer returns a valid structured verdict, Hub appends its compact conclusion
and evidence to the next generation context, refreshes persisted native resume
metadata, and creates the next generation without requiring a browser prompt.
Successor creation atomically requires the completed graph to remain the latest
generation, so duplicate reviewer terminal delivery cannot consume two rounds.
An explicit follow-up after the run is terminal starts a new configured round
budget under the same run id as before.

Payloads stay compact. Worker handoffs carry the newest request, node role,
artifact/diff reference, verification evidence, risks, and next action. Native
CLI history remains the primary conversational memory.

Prompt context excludes browser-only bootstrap notices, proof-note creation
messages, repeated role templates, and full prior prose when structured evidence
exists. The next node receives the newest goal state, material changes,
successful and failed command evidence, repeated blocker description, and next
entry point. This keeps continuity without encouraging a fresh project scan.

## Shared Project Directory

Every node runs in the exact project directory selected for the orchestration.
This preserves normal command-line behavior: starting `codex` or `claude` in
that directory exposes the native conversation through `/resume`. Candidate B
depends on candidate A, and the integrator/reviewer remain serial, so arbitrary
filesystem writes never overlap. Hub task records retain durable handoffs and
attempt evidence; they do not own copies of the checkout.

## Reviewer Barrier

Workers and the integrator cannot self-certify completion. The reviewer receives
the integrated result plus bounded evidence references and runs relevant tests.
Its terminal event includes command evidence and a verdict.

Before the final configured generation, a structurally valid reviewer handoff
with `resolved`, `needs_next`, or `blocked` is a successful review task. It
advances to the next generation because the reviewer completed its assignment;
the handoff status describes goal progress, not runner health. On the final
generation, only `resolved`, `to=user`, `intent=final`, `next=none`, and
`risks=none` may complete the run. A final `needs_next` or `blocked` verdict is
reported as an unmet-goal conclusion rather than as missing command evidence.

Only the final configured generation's reviewer receives final-run synthesis
guidance. Earlier candidates, integrators, and reviewers receive the real outer
round number, are told that later nodes or rounds will run, and finish with a
peer handoff rather than a user-final conclusion.

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
6. The owning top-level run receives terminal `turn.end` and `run.error`
   events after its graph becomes `unknown`; it must not remain visually
   `running` after the execution authority was lost.
7. Only unambiguous `ready` work may be dispatched after Bridges reconnect.

An explicit retry never edits the old attempt. It creates a new attempt with a
new id and digest plus `retry_of_attempt_id`. Retrying an unknown task is a user
decision because the previous attempt may already have changed external state.

## Data And Protocol Impact

- Add three graph tables and store CRUD/claim/recovery methods.
- Extend orchestration start and event data with optional graph/task/attempt
  identity, payload digest, and explicit `round`/`maxRounds` metadata. Internal
  node dispatch keeps `maxTurns=1`; the separate round fields preserve the
  whole-run budget. Older Bridges ignore absent graph metadata and Hub uses the
  conservative serial topology for them.
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
   continued, link dispatch metadata to the existing start payload, and create
   the next generation automatically after each valid intermediate review.
3. Persist and reconcile graph-aware Bridge events without changing browser
   event rendering.
4. Preserve the selected CWD for every Bridge task and serialize writable
   worker, integration, and reviewer execution.
5. Reuse formal-proof checker recognition to enforce the reviewer barrier.
6. Release each durable task's native app-server writer before publishing its
   terminal event while preserving the persisted thread id for the next node.
7. Add store race/idempotency tests, Hub recovery tests, Bridge isolation tests,
   and end-to-end orchestration tests.
8. Sweep architecture, code map, change-impact rules, and roadmap status.
9. Compose graph-node prompts from outer round metadata, role-specific agency,
   and compact blocker evidence rather than the internal one-turn envelope.
10. Render task-graph round and role identity consistently and hide internal
    bootstrap/protocol labels from the user timeline.

## Exit Gates

- A task claim is atomic and concurrent claims cannot create two attempts for
  the same node.
- Duplicate acknowledgement and terminal events are idempotent.
- A mismatched attempt id or payload digest cannot complete a node.
- Restart recovery preserves ready work and converts ambiguous work to
  `unknown` without redispatch.
- Restart recovery settles the owning run instead of leaving its timer and
  command lifecycle visually `running` forever.
- Retry preserves the original attempt and records parent lineage.
- No graph has overlapping writable nodes in the selected project directory.
- Worker, integration, and reviewer events report the selected project CWD.
- Integration and review never overlap and review starts only after successful
  integration.
- A configured N-round run creates and executes N graph generations unless the
  user cancels it or a genuine execution/protocol failure stops it.
- Every node is prompted to continue through viable implementation and
  validation paths; a single failed approach does not force a handoff.
- An unresolved handoff follows repeated materially identical blockers and
  contains the exact problem, attempts, command evidence, and executable next
  entry point.
- Only the final round reviewer receives final-turn/final-user guidance; all
  other graph nodes receive accurate outer round and successor context.
- Header, timeline group, and task card round labels all derive from
  `round`/`maxRounds`, never internal `turn` or graph `generation`.
- Browser timelines omit proof harness bootstrap notes and raw protocol kind
  labels while retaining actionable recovery, warning, approval, and error
  events.
- Intermediate valid reviewer verdicts automatically advance the same run to
  the next generation with compact evidence and native-session continuity.
- The run cannot complete without a resolved final-generation reviewer.
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
