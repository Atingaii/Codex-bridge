> **DEPRECATED - proof-strategy gates replaced by pass-through CLI relay**
>
> Current design: [orchestration-pass-through-cli.md](orchestration-pass-through-cli.md).
> Historical only; do not implement proof-specific Bridge prompt guardrails,
> final proof assessment gates, or controlled-background Isabelle build
> requirements from this document.

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
| `debate` | The proposer may present the strongest proof plan or patch, but must leave a falsifiable proof claim and named audit checks. The critic must first try to falsify it by looking for changed statements, fuel wrappers, admitted axioms, hidden placeholders, missing equivalence lemmas, or proof obligations that were moved rather than discharged. |

For Coq/Isabelle/Lean work, compiling is only a smoke check. A result must not
be marked `status=resolved` if it weakens the original statement, replaces a
recursive definition with a bounded/fuel version, changes the target theorem, or
adds trust assumptions such as `Axiom`, `Parameter`, `Conjecture`, `Admitted`,
`sorry`, `quick_and_dirty`, `Guard Checking` changes, `bypass_check`, or opaque
placeholders. A bounded/fuel implementation is only
acceptable when the same result proves the original recursive semantics,
termination/decrease obligation, and fuel sufficiency/equivalence to the
original specification.

Formal proof handoffs should include an audit plan and result. Depending on the
proof assistant and project, useful checks include `rg` scans for proof
shortcuts, Coq `Print Assumptions <target>` output that reports `Closed under
the global context`, Lean `#print axioms <target>` with no `sorryAx` or unexpected
axioms, Isabelle `thm_oracles <target>` plus scans for `sorry` /
`quick_and_dirty`, and a build command such as `make`, `coqc`, `lake build`, or
`isabelle build`. For termination work, the audit must identify the original
recursive call or measure and the exact decrease / well-founded proof obligation
rather than accepting a wrapper that merely bounds execution.

The proof-strategy prompts follow the standard proof-assistant workflows rather
than ad hoc completion claims. They are spec-first: the worker must identify the
exact uploaded theorem/function obligation, name the target fact or theorem, and
keep a traceable mapping from uploaded constructs to generated Coq/Rocq or
Isabelle definitions before making proof-script changes. Isabelle/HOL total
functions must discharge termination: the worker may try `lexicographic_order`,
but after that it should use an explicit `relation` / `measure` or `measures`
proof and record the well-foundedness and recursive-call decrease subgoals.
Coq/Rocq proof claims are not accepted from compilation alone; the verifier must
run `Print Assumptions` on the target theorem and require `Closed under the
global context`, and source scans must also catch `Variable` / `Hypothesis`
trust shortcuts when they are used as fake proof assumptions.

Formal proof tasks also use serial tool execution. Workers must wait for each
Coq/Rocq or Isabelle command to finish before starting the next build, scan, or
proof probe, and they must not leave detached/background proof-assistant jobs as
evidence. This keeps browser smoke tests auditable: a timeline with stale
in-progress version checks or scans cannot be accepted as a completed proof
assessment.

Every proof-assistant command also needs an explicit timeout. Quick toolchain
probes such as `coqc --version`, `rocq --version`, `isabelle version`,
`command -v`, and source scans should use short timeouts, typically 10s to 60s.
Full Isabelle or Coq builds may use longer documented timeouts, but if a tool is
missing or a probe times out, the worker should stop and emit a visible
`needs_next` / `blocked` obligation ledger rather than leaving the browser run
stuck.

For Coq/Rocq specifically, probes should first locate a binary with a
POSIX-safe command such as `timeout 20s sh -lc 'type -P coqc || type -P rocq ||
true'` or a Python `shutil.which` check. Workers should not start with bare
`coqc --version` or `timeout 20s command -v coqc` on remote smoke machines,
because those probes have produced stale tool events when the shell/tool layer
does not return a final result.

Proof exploration is intentionally bounded. After three failed proof strategies
or one long proof-assistant build/proof attempt, the worker should stop blind
search and hand off a compact obligation ledger instead of trying another
similar measure guess. That ledger names the failed goals, attempted
measures/relations, semantic constraints, and the next lemma that would need to
be proved. This lets the next role review or switch strategy while the browser
still receives a timely, user-visible assessment.

Isabelle upload tasks get an Isabelle-specific prompt block in addition to the
generic proof guardrails. The Bridge tells the worker to keep the uploaded
`ROOT`, `Model.thy`, and `Termination.thy` semantics intact, create a visible
new project folder under the requested working directory rather than treating the
hidden `.codex-bridge` upload staging directory as the deliverable, and preserve
or explicitly remap `ROOT` directory declarations such as `directories "HWQ-U"`
so the first build reaches the real proof obligation instead of failing on a
missing copied layout. The worker then runs `isabelle build` with a long timeout,
scans source files and `ROOT` for
`sorry`, `quick_and_dirty`, `oops`, `sketch`, `admit`, and placeholders, run
`thm_oracles` or an equivalent oracle-free audit, and treat `termination
modify_lin` as the original obligation. Generated-subgoal probes and failed
measure attempts must stay in scratch space or be removed before the final
build; even scratch probes must not use `sorry`, `quick_and_dirty`, `oops`,
`sketch`, or `admit` as fake proof steps. Subgoals should be obtained by running
an incomplete candidate proof and capturing Isabelle's failure output. Before a
worker uses guessed simplification facts such as `_def` rules, it must confirm
them with `find_theorems name:<pattern>` or `thm <fact>` and include undefined
fact failures in the obligation ledger. Deliverable projects may not keep
`Repro.thy`, `*_original.thy`, scratch theories, or diagnostic-only `ROOT`
imports. Full Isabelle builds for slow sessions should use an explicit long
timeout, such as 30m or 45m, and the final output must be captured in the same
turn when possible. Because Isabelle builds can legitimately run for tens of
minutes and a single foreground command does not stream useful output into the
browser timeline, build visibility is part of the workflow: every full
`isabelle build -D` or `isabelle build -d` check must start with one short
controlled-background command that writes `build.log`, `build.pid`, and
`build.pgid`, and `build.exit`, then periodically emit separate short
`tail -n 80 build.log` and PID/PGID/exit-status checks. Foreground full-build
commands such as `timeout ... isabelle build ...` or `isabelle build ... | tee
build.log` do not satisfy the web-visible smoke path. If the build exceeds the
practical turn window, the worker may hand the build back to the user instead
of repeating blind automation: it must record the exact manual command, log
path, elapsed time, PID/PGID/exit-status file when available, and current log
tail. Once that handoff
appears, later CLI turns should not rerun the same long Isabelle build
automatically; the Bridge inserts an explicit carry-over prompt block that tells
the next CLI to inspect source files, existing logs, and PID/PGID/exit files
only, unless the user explicitly asks for a fresh build. The final run result
must say that acceptance is pending the user's manual Isabelle build output. A
compile-only framework, weakened theorem, changed function semantics, remaining
fake proof, diagnostic leftover, or detached background build whose final output
is not captured or explicitly handed off cannot satisfy the task.

Coq/Rocq conversion tasks get a separate Coq-specific prompt block. The Bridge
requires a self-contained `_CoqProject`/`Makefile` style project in a visible new
folder under the requested working directory, not just files left in the hidden
upload staging directory. It also requires a source-only scan for Coq trust
bypasses including `Axiom`, `Parameter`, `Variable`, `Hypothesis`,
`Conjecture`, `Admitted`, `Guard Checking`, `bypass_check`, and fuel wrappers,
and `Print Assumptions` output showing `Closed under the global context`. The
target theorem must name the translated `modify_lin` obligation and mention the
chosen well-founded relation, semantic equivalence, or branch-decrease
invariant; tautologies, length-only lemmas, helper-only structural recursion
totality, and bounded-evaluator statements do not count as the original proof
obligation. The verifier must inspect or print the final `modify_lin` definition
and reject structural helper rewrites such as `modify_loop` unless a named
bisimulation/equivalence theorem connects them to the original recursive step
relation. Fixed-fuel translations remain unresolved unless the result also
proves equivalence, decrease, and fuel sufficiency.

The debate path must converge through adversarial evidence, not role labels
alone. A proposer handoff is only useful if the critic can falsify it with a
specific check. A critic finding that the patch relies on `default_fuel`,
weakens the theorem, lacks an equivalence lemma, or hides an admission overrides
proposer confidence and prevents `status=resolved` until that finding is
discharged.

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

Direct Codex CLI turns also have a runtime guard outside the prompt strategy.
The Bridge puts spawned CLI commands in a managed process group and cancels that
group when the run is canceled or the turn fails. For direct Codex JSONL, if all
command events have completed and no assistant text or new tool event arrives
within the idle guard window, Bridge turns the stall into a visible turn error
and terminal run status. Long-running proof commands remain allowed while their
`command.start` is active; the guard targets the post-command idle case where
the browser would otherwise show completed command cards but a permanently
running orchestration.

After the final verifier and before emitting `run.end`, the Bridge performs a
post-test result assessment from recorded turns, tool events, handoffs, and the
workspace diff. The terminal event content is not the generic
`Orchestration completed.` string; it is a browser-visible checklist that names
the task acceptance criterion, workspace changes, command verification, proof
audits when applicable, and remaining risks. If any required dimension is
missing, the Bridge emits `run.error` with the same visible checklist so the
browser shows why the run is not a real success.

Before that terminal `run.error`, the Bridge gets one bounded remediation
attempt for assessment failures that are plausibly fixable. The remediation
prompt includes the failed assessment dimensions, the pre-remediation checklist,
compact prior handoffs, and the original user task. The next CLI must make a
concrete fix or add the missing proof/verification evidence, then rerun the
relevant checks. After that single remediation turn, the Bridge recomputes the
workspace diff and the multi-dimensional assessment; only a still-failing result
is emitted as `run.error`.

If the selected Bridge disconnects while an orchestration is queued or running,
the Hub also emits the existing `run.error` event kind with browser-visible
diagnostic content. That content must distinguish a transport/CLI interruption
from a proof acceptance result and include a short summary of recent progress
from stored orchestration events when available. This keeps the web UI from
showing only a generic offline error when the user is using the page as the
smoke-test surface.

For the Coq upload benchmark using `Model.thy`, `Termination.thy`, and `ROOT`,
a resolved handoff is insufficient unless the final record contains evidence for
all required proof dimensions: the uploaded files were accounted for, a new Coq
project folder under the requested working directory was written, `make` or
`coqc` passed, source-only placeholder scans found no forbidden proof shortcuts,
Coq `Print Assumptions` reported `Closed under the global context` for the target,
and the original `termination modify_lin` obligation was audited. Any
`modify_lin_fuel`, `default_fuel`, or similar bounded-fuel replacement remains a
blocker unless the same result proves equivalence to the original recursive
semantics, the decrease / well-founded measure, and fuel sufficiency.

The same acceptance gate applies when the selected Bridge uses the local CCB
orchestration runner. CCB can coordinate its own Codex and Claude agents, but
Codex Bridge is still responsible for the browser-visible terminal result. The
Bridge wraps the CCB prompt with the same proof guardrails, then converts the CCB
reply, streamed agent text, and provider tool events into the same assessment
model used by the built-in round-robin runner. A completed CCB job therefore
emits `run.end` only when the multi-dimensional assessment passes; otherwise the
browser sees `run.error` with the failed dimensions instead of a generic
`CCB job completed.` message.

## Data And Protocol Impact

- No SQLite schema changes.
- No protocol payload changes for strategy; browser approval reuses existing
  `approval_request` / `approval_response` frames as described in
  [orchestration-deep-collaboration-and-approval.md](orchestration-deep-collaboration-and-approval.md).
- No frontend event shape changes.
- Existing `turn.start`, `turn.delta`, `command.start`, `command.end`,
  `turn.end`, and `run.end` events remain the only emitted successful-path event
  kinds. Repeated blockers use the existing `run.error` event kind. The verifier
  is represented as another normal turn with a verifier turn id. Bridge
  disconnect diagnostics also use the existing `run.error` event kind.
- Terminal `run.end` and `run.error` content carries the browser-visible result
  assessment; no new event kind or protocol payload field is introduced.

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
7. Add post-test assessment generation and proof-task evidence checks before
   terminal run events.
8. Ensure the frontend treats the terminal assessment as the final visible
   conclusion, without adding a weaker fallback summary.
9. Add a bounded final-assessment remediation turn before terminal failure when
   missing dimensions are fixable.
10. Apply the same prompt guardrails and terminal assessment to local CCB
    orchestration runs.
11. Run full Go tests, frontend build, Go build, and doc lint.

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
- The browser timeline shows a terminal multi-dimensional result assessment for
  completed or failed runs.
- If the post-test assessment fails on a fixable dimension, one remediation turn
  runs before final failure and the assessment is recomputed afterward.
- Formal proof prompts carry proof-specific guidance in both collaboration and
  debate modes, including explicit rejection of compile-only, weakened, or
  fuel-wrapper solutions unless equivalence and termination obligations are
  proved.
- The Coq upload smoke task cannot complete unless the visible assessment covers
  uploaded input mapping, new project folder outside hidden upload staging, Coq
  build, placeholder scan, `Print Assumptions` / `Closed under the global
  context` audit, a named target theorem, branch-decrease or semantic
  equivalence evidence, and the original `termination modify_lin` obligation.
- The Isabelle upload smoke task cannot complete unless the visible assessment
  covers uploaded input mapping, new Isabelle project folder outside hidden
  upload staging, `isabelle build`, source-only fake-proof scan, `thm_oracles` /
  oracle-free audit, no diagnostic leftovers in the final `ROOT` / `.thy`
  files, no detached background build left unresolved, a named target fact,
  branch-decrease evidence, and the original `termination modify_lin` obligation.
- The local CCB path cannot report a generic completed result for proof tasks;
  its final browser-visible `run.end` / `run.error` content must be the same
  multi-dimensional assessment used by non-CCB orchestration.
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
