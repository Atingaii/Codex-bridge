# Orchestration Collaboration And Debate Contracts

## Goals

- Give collaboration and debate distinct, browser-visible operating contracts
  instead of relying only on alternating role labels.
- Make every role produce falsifiable evidence: inspected files, executed
  commands, observed failures, assumptions, blockers, and remaining risk.
- Make the last scheduled turn synthesize the whole run into a user-ready
  conclusion rather than hand work to a worker that will never run.
- Preserve native Codex and Claude conversation continuity, configured worker
  order, and the pass-through CLI boundary.
- Cover both modes with focused prompt-contract tests and native-CLI relay
  integration tests that do not require live model credentials.

## Non-Goals

- Do not add hidden verifier, judge, remediation, or proof-acceptance turns.
- Do not let Bridge decide whether a mathematical proof or engineering claim is
  semantically correct.
- Do not automatically replay a completed CLI turn or stop a run early from a
  free-form success phrase; either action could duplicate tool side effects or
  skip the second worker's independent check.
- Do not change HTTP endpoints, WebSocket event kinds, SQLite schema, runner
  interfaces, the configured turn count, or the persisted first-CLI setting.
- Do not force collaboration participants to agree or debate participants to
  disagree when the evidence points elsewhere.

## Data And Protocol Impact

- No HTTP, WebSocket, SQLite, or `internal/protocol.Envelope` shape changes.
- Existing `turn.start` events continue to carry the full authenticated relay
  prompt in `TurnStartData.PromptText`; public-share sanitization continues to
  remove that diagnostic prompt.
- Existing role values remain `implementer`, `reviewer`, `proposer`, and
  `critic`. Existing `worker_pair`, `first_cli`, `max_turns`, native thread ids,
  and follow-up `runID` continuity are unchanged.
- Existing `turn.end`, `run.conclusion`, and terminal run events remain the
  source of visible results. Mode contracts guide CLI output but do not create
  a Bridge-owned verdict field. A final line-anchored `Handoff:` with
  `status=blocked` or `status=needs_next` maps the existing structured
  `RunConclusion.outcome` to `blocked` and records its `next`/`risks` values as
  unmet obligations; lifecycle compatibility still uses `run.end`.

## Design

`internal/bridge/orchestration_relay.go:composeRelayPromptWithWorkerSlot`
adds a compact mode-and-role contract on every scheduled turn.

Collaboration uses an implementation and review loop:

- The implementer owns root-cause analysis, the smallest complete change,
  focused validation, and an explicit ledger of files, commands, and blockers.
- The reviewer independently inspects the implementation and runs relevant
  disconfirming checks. It fixes safe, in-scope defects it finds and then states
  what is accepted, rejected, or still unverified; it is not limited to prose
  review.
- On later cycles, each role must address unresolved evidence from prior turns
  instead of restarting the task or merely agreeing with the other worker.

Debate uses a proposal, falsification, and adjudication loop:

- The proposer states a falsifiable claim, assumptions, evidence, and a
  concrete implementation or experiment when the task requests code.
- The critic tests the strongest version of that claim, searches for
  counterexamples and hidden assumptions, and supplies a stronger alternative
  or an in-scope fix rather than objecting abstractly.
- Later turns must answer the strongest unresolved counterargument and update
  the working result when evidence changes the conclusion.

For both modes, the last scheduled turn receives an explicit final-synthesis
duty. It must compare relevant prior claims with command evidence, finish any
safe in-scope work, distinguish verified facts from unresolved assumptions, and
write a user-facing `最终结论` or `最终测试结果`. It must not address a nonexistent
next worker. Bridge does not add a hidden turn when this instruction is ignored;
the existing interrupted-reply retry remains limited to missing final text or
handoff markers as documented in
[orchestration-continuity.md](orchestration-continuity.md).

Relay history is bounded before prompt composition. Bridge keeps the newest
turn evidence and works backwards within a global byte budget, instead of
adding every full historical result to every later turn. Command lifecycle
events with the same tool id are coalesced to their newest state before at most
six command summaries are selected, so a `running` event cannot crowd its own
terminal exit code or a later audit command out of the prompt.

The role contract is deliberately generic. The opt-in formal-proof profile may
append Coq/Isabelle-specific guidance through
`internal/bridge/profiles/formalproof/formalproof.go:RelayGuidance`, but default
orchestration never infers a proof profile from task keywords.

## Implementation Steps

1. Add mode, role, cycle, and final-turn prompt helpers in
   `internal/bridge/orchestration_relay.go`.
2. Compose the contract before prior-turn evidence and the user task so each
   native CLI receives it through the existing prompt path.
3. Add table-driven tests for all four roles, later-cycle duties, final
   synthesis, unknown-mode fallback, and absence of hidden-turn language.
4. Parse only explicit, anchored machine handoffs for blocked conclusion data,
   bound prior-turn prompt history, and coalesce command lifecycle evidence.
5. Add credential-free relay tests that exercise collaboration and debate
   schedules with deterministic fake native CLI responses.
6. Run the focused Bridge tests, race detector, full Go/frontend/build suite,
   and documentation lint.

## Exit Gates

- Collaboration prompts require implementers to implement and test, and
  reviewers to independently falsify, verify, and fix in-scope findings.
- Debate prompts require proposers to make falsifiable claims and critics to
  seek counterexamples, inspect assumptions, and provide a stronger result.
- A later-cycle prompt tells the current worker to resolve prior objections and
  evidence rather than restart the task.
- The last scheduled prompt requires a user-ready synthesis with evidence,
  unresolved risks, and no handoff to another CLI.
- A completed relay whose final explicit handoff says `blocked` or `needs_next`
  has `RunConclusion.outcome=blocked`, while resolved handoffs remain satisfied.
- A 12-turn history produces a bounded prompt that retains the newest evidence;
  paired command start/end events produce one terminal command summary.
- `worker_pair`, `first_cli`, role order, native session reuse, turn count, and
  follow-up `runID` behavior remain compatible.
- Focused tests and the full project verification commands pass without live
  Codex or Claude credentials.

## Reviewer Q&A

**Q1: Why not add an automatic judge or convergence parser?**

A: Free-form claims are not a trustworthy correctness signal, especially for
formal proofs. A hidden judge would also contradict the current pass-through
design. Independent CLI checks and proof-assistant/compiler output are visible,
auditable evidence without giving Bridge semantic authority.

**Q2: Why always run the configured number of turns?**

A: The user selected that budget, and the second role's independent work is the
main value of both modes. Silent early convergence could skip a counterexample;
replaying a turn could duplicate file or command side effects. Only explicit
cancel or a terminal execution failure shortens the schedule.

**Q3: Why can the reviewer or critic edit code?**

A: These are working CLI participants, not read-only commentators. Requiring an
extra remediation turn after finding a small defect wastes the configured turn
budget. They may make safe in-scope fixes while documenting what changed and
what evidence supports the final claim.
