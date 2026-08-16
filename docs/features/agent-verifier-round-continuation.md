# Agent-Verifier Round Continuation

## Goals

- Make early orchestration completion depend on two independent Agent Verifiers
  after the durable graph's final reviewer, rather than a Bridge-local semantic
  gate.
- Preserve local checker, command, handoff, and formal-proof observations as
  auditable facts supplied to those agents.
- Continue through the configured collaboration rounds when the agents do not
  unanimously confirm completion; only report an unresolved result after the
  final configured round.
- Present planning and plan review as pre-collaboration stages, not as
  collaboration round numbers.
- Remove the `strict-workspace` (automatic execution limited to the workspace)
  option from the browser's new-endpoint selector without changing server
  support or existing endpoint behavior.

## Non-Goals

- Do not add a third worker, change the `candidate-a -> candidate-b ->
  integrate -> review` topology, or alter worker-slot ownership.
- Do not change protocol frames, persisted schema, main-site deployment, or
  existing endpoint compatibility.
- Do not let an Agent Verifier claim completion without the recorded evidence
  it needs to substantiate that claim.

## Data And Protocol Impact

- Existing verifier events retain their shape. Their local checker entries are
  recorded facts, prefixed `recorded/`, rather than an execution gate.
- The Hub still advances a completed durable reviewer node by its existing
  task-graph transition. `verified-early` remains the existing terminal reason.
- No database migration or wire-field change is required.

## Implementation Steps

1. Collect local handoff, command, proof-checker, and reviewer-boundary facts
   for the verifier prompt without short-circuiting the Agent Verifier calls.
2. Run the two role-bound verifier agents only after the durable `review`
   node. Require both to pass for `verified-early`.
3. On a non-final round, emit ordinary task completion after a continuing
   verifier verdict so the Hub dispatches the next graph. On the final round,
   emit an unresolved terminal result only after the verifier quorum continues.
4. Hide collaboration round metadata on `planner` and `plan-reviewer` timeline
   events and label graph navigation from actual round-bearing work graphs.
5. Hide `strict-workspace` in the frontend selection list while preserving
   repair commands and status visibility for endpoints that already use it.

## Exit Gates

- Two Agent Verifiers run after every durable reviewer task, including when a
  local fact is missing.
- Missing formal checker evidence advances an intermediate round instead of
  ending the run immediately.
- An early successful end requires two Agent Verifier passes.
- A final unresolved reviewer verdict ends only after the configured last
  round.
- Planning and plan review never display as `Round 1/N`.
- Frontend build, focused Bridge tests, task-graph tests, full Go tests, doc
  lint, and experimental deployment health checks pass.

## Reviewer Q&A

**Can a verifier ignore a failed or absent proof checker?**

No. The recorded facts and raw command evidence stay in its prompt, and the
verifier prompt requires `continue` for missing required proof evidence. The
change is that Bridge no longer makes that semantic decision before either
independent agent is asked.

**What reaches the next round?**

The Hub appends the durable reviewer's handoff and conclusion to the next
round's context. The next candidate starts from that recorded state.
