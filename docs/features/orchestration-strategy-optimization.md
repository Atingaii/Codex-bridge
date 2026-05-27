# Orchestration Strategy Optimization

## Goal

Make the existing collaboration and debate modes feel more like structured
multi-agent work while keeping token use bounded. The Bridge should pass compact
handoffs between Claude Code and Codex CLI instead of replaying large raw turn
transcripts.

## References

- AutoGen teams use round-robin group chat, selector teams, and handoff-based
  teams for multi-agent coordination:
  <https://microsoft.github.io/autogen/stable/user-guide/agentchat-user-guide/tutorial/teams.html>
- LangGraph handoffs emphasize explicit context engineering and summarized
  handoff payloads instead of passing full sub-agent history:
  <https://docs.langchain.com/oss/python/langchain/multi-agent/handoffs>
- CrewAI separates sequential and hierarchical processes, with manager-style
  planning, delegation, and validation:
  <https://docs.crewai.com/en/concepts/processes>
- OpenAI Swarm models lightweight orchestration around agents, handoffs, and
  context variables:
  <https://github.com/openai/swarm>
- Coq/Rocq documents assumptions as global postulates and provides
  `Print Assumptions` to audit theorem dependencies:
  <https://rocq-prover.org/doc/V8.19.2/refman/proof-engine/vernacular-commands.html#print-assumptions-reference>
- Coq/Rocq documents `Axiom`, `Conjecture`, and `Parameter` as assumptions /
  postulates:
  <https://rocq-prover.org/doc/V8.19.1/refman/language/core/assumptions.html#assumptions>
- Isabelle/Isar documents `sorry` as a fake proof and explains that facts from
  fake proofs are not real proofs:
  <https://isabelle.in.tum.de/dist/library/Doc/Isar_Ref/Proof.html>
- Isabelle/Isar notes that definitions such as `function` and `termination`
  require explicit proof justification:
  <https://isabelle.in.tum.de/dist/library/Doc/Isar_Ref/Spec.html>
- Lean documents `sorryAx`, `#print axioms`, and that `sorry` is not intended in
  finished proofs:
  <https://lean-lang.org/doc/reference/latest/Axioms/>
- Lean's tactic reference documents `admit` as a synonym for `sorry` and shows
  well-founded recursion / `termination_by` behavior:
  <https://lean-lang.org/doc/reference/latest/Tactic-Proofs/Tactic-Reference/>

## Non-Goals

- Do not add a new model, queue, database, or resident multi-agent runtime.
- Do not change `internal/protocol.Envelope` or add new event kinds.
- Do not change orchestration create/continue/cancel endpoints.

## Current State

- `internal/bridge/orchestration.go:run` alternates turns between Claude and
  Codex.
- `internal/bridge/orchestration.go:composeOrchestrationPrompt` appends prior
  turn content directly, which can become token-heavy and does not define a
  shared handoff contract.
- `internal/hub/orchestration.go:compactOrchestrationContext` already compacts
  prior persisted events for follow-up prompts.

## Design

Keep the existing two modes but make each mode a protocolized workflow:

| Mode | Updated strategy |
| --- | --- |
| `collaboration` | Claude acts as builder/implementer, Codex acts as reviewer/verifier, and each turn ends with a compact handoff containing status, changed files, verification, next action, and risks. |
| `debate` | Claude proposes a concrete thesis or patch, Codex tries to falsify it with evidence, and the final turn synthesizes accepted claims, rejected claims, verification, and remaining uncertainty. |

Bridge prompt construction uses three context layers:

1. Latest user task, always authoritative.
2. Follow-up compacted run context from Hub when `Resume=true`.
3. Compact prior-turn handoffs generated locally from visible outputs and tool
   summaries.

Prompts require each CLI to track the user's core acceptance criterion
explicitly. In collaboration mode the reviewer must audit whether the previous
turn advanced that criterion, not merely whether the project compiles. This
prevents any task with a stronger acceptance condition from being marked
resolved after only a narrow validation check.

For formal proof tasks, both modes add a proof-specific sub-strategy. This is
triggered by proof assistant names, uploaded proof files, or terms such as
`theorem`, `termination`, `sorry`, `Admitted`, and `Axiom`. The two strategies
remain distinct:

| Mode | Proof-task behavior |
| --- | --- |
| `collaboration` | The implementer must keep a proof-obligation ledger: target theorem/definition, missing proof, semantic constraints, attempted proof path, and exact blocker. The reviewer must inspect for semantic weakening before accepting build success. |
| `debate` | The proposer may present the strongest proof plan or patch, but the critic must first try to falsify it by looking for changed statements, fuel wrappers, admitted axioms, hidden placeholders, or proof obligations that were moved rather than discharged. |

For Coq/Isabelle/Lean work, compiling is only a smoke check. A result must not
be marked `status=resolved` if it weakens the original statement, replaces a
recursive definition with a bounded/fuel version, changes the target theorem, or
adds trust assumptions such as `Axiom`, `Admitted`, `sorry`,
`quick_and_dirty`, or opaque placeholders. A bounded/fuel implementation is only
acceptable when the same result proves the original recursive semantics,
termination/decrease obligation, and fuel sufficiency/equivalence to the
original specification.

Formal proof handoffs should include an audit plan and result. Depending on the
proof assistant and project, useful checks include `rg` scans for placeholders,
Coq `Print Assumptions <target>` or dependency output that reports no
assumptions, Lean `#print axioms <target>` with no `sorryAx` or unexpected
axioms, Isabelle `thm_oracles <target>` plus scans for `sorry` /
`quick_and_dirty`, and a build command such as `make`, `coqc`, `lake build`, or
`isabelle build`. For termination work, the audit must identify the original
recursive call or measure and the exact decrease / well-founded proof obligation
rather than accepting a wrapper that merely bounds execution.

The Bridge must avoid sending full raw previous outputs unless they are short.
Each prior turn in the next prompt should be capped and should prefer parsed
handoff fields when the agent provided them. The visible `Handoff:` line remains
the compatibility surface, while `internal/bridge/orchestration.go:parseHandoffFields`
stores the important values as structured turn state for later prompts.
Compact prior turns include both successful verification commands and failed
command summaries so the next CLI can see what has already been tried.
Fallback summaries are stripped before reuse so generated "turn completed"
boilerplate does not recursively grow across turns.

Agents are instructed to end visible responses with compact routing and handoff
lines:

```text
Msg: to=<next-role|user>; intent=<implement|review|challenge|final>; need=<one request or none>
Handoff: status=<needs_next|blocked|resolved>; changed=<files or none>; verified=<commands or none>; next=<one action>; risks=<open issue or none>
```

`Msg:` keeps inter-agent communication targetable and compact. `Handoff:`
remains the compatibility line for status, verification, next action, and
early-stop detection.

The `status=resolved` signal may end an orchestration early after at least two
turns, but only when the same visible answer includes a user-facing conclusion.
If a turn errors or reports `status=blocked`, fallback text must say the turn or
run did not complete and must preserve the blocker. It must not claim the run is
complete.

When the same normalized blocker repeats for three consecutive turns without
concrete progress, the Bridge emits `run.error` and stops the run before
exhausting `maxTurns`. This keeps round-robin deterministic while avoiding a
dead loop of the same failed command or environmental blocker.

When a run reports changed files, unresolved risks, turn errors, or failed tool
commands, the Bridge adds one lightweight final verifier turn. It is skipped for
clean resolved runs with verification, so successful no-change runs do not pay a
fixed extra model call.

## Data And Protocol Impact

- No SQLite schema changes.
- No protocol payload changes for strategy; browser approval reuses existing
  `approval_request` / `approval_response` frames as described in
  [orchestration-deep-collaboration-and-approval.md](orchestration-deep-collaboration-and-approval.md).
- No frontend event shape changes.
- Existing `turn.start`, `turn.delta`, `command.start`, `command.end`,
  `turn.end`, and `run.end` events remain the only emitted successful-path event
  kinds. Repeated blockers use the existing `run.error` event kind. The verifier
  is represented as another normal turn with a verifier turn id.

## Implementation Steps

1. Add a compact handoff formatter in `internal/bridge/orchestration.go`.
2. Update `composeOrchestrationPrompt` to use strategy-specific instructions
   and compact prior-turn handoffs.
3. Add early completion detection for explicit resolved handoffs.
4. Add structured handoff parsing and a conditional final verifier prompt.
5. Add blocker detection, failed-command carry-forward, and non-completion
   fallback summaries for failed turns.
6. Add tests for compact handoff prompts, collaboration guidance, debate
   guidance, completion detection, and repeated blockers.
7. Run full Go tests, frontend build, Go build, and doc lint.

## Exit Gates

- Collaboration prompts mention builder/reviewer duties and the compact `Msg:`
  plus `Handoff:` contracts.
- Debate prompts mention proposer/critic duties, evidence, and synthesis.
- Large prior outputs are compacted before being sent to the next CLI.
- No new event kind is introduced, so frontend rendering stays on existing
  branches.
- `status=resolved` can stop after at least two turns only when a final answer
  is present.
- Failed commands are carried into the next turn's compact state.
- Failed turns do not receive fallback summaries that claim successful
  completion.
- A repeated blocker stops the run with `run.error` before all turns are
  consumed.
- A final verifier turn runs only for file changes, failed commands/errors, or
  unresolved risks.
- Formal proof prompts carry proof-specific guidance in both collaboration and
  debate modes, including explicit rejection of compile-only, weakened, or
  fuel-wrapper solutions unless equivalence and termination obligations are
  proved.
- Full test and build commands pass:
  `/usr/local/go/bin/go test ./...`, `cd frontend && npm run build`,
  `CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .`,
  and `make doc-lint`.

## Reviewer Q&A

**Q1: Why not add a real supervisor agent?**

A: A supervisor would add another CLI/model call per turn and increase token
cost. The current boundary can get most of the benefit by making handoffs
explicit and compact.

**Q2: Why keep round-robin instead of dynamic speaker selection?**

A: Dynamic selection needs another model decision or protocol field. Round-robin
is deterministic, easy to test, and matches the existing Bridge implementation.

**Q3: Why show handoffs to the user?**

A: The UI already renders `turn.delta` as visible timeline entries. Visible
handoffs make the collaboration auditable without adding hidden state that the
browser cannot replay.
