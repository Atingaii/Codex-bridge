# Formal-Proof Orchestration Contracts

## Goals

- Keep the existing browser-visible `collaboration` and `debate` choices while
  making their `formal-proof` backend prompts proof-state and evidence driven.
- Make every completion claim traceable to an unchanged named statement, the
  proof assistant's current goals, exact checker commands, and a trust audit.
- Make collaboration alternate a proof author and an independent proof auditor.
- Make debate alternate a falsifiable proof claim and an adversarial attempt to
  refute it through counterexamples, statement comparison, dependency review,
  and executable checks.
- Preserve native CLI continuity, turn count, worker-pair selection, browser
  approvals, uploads, the lightweight proof notes, and follow-up `runID`
  behavior.

## Non-Goals

- No frontend, HTTP, WebSocket, SQLite, or protocol shape changes.
- No new proof engine dependency and no direct integration with LeanDojo,
  Pantograph, miniF2F, Lean, Rocq, Isabelle, or another prover.
- No hidden verifier/remediation turns or Bridge-owned semantic proof verdict.
- No requirement that default-profile software tasks use formal-proof roles.

## Open-Source Inputs

The design adapts workflow principles from established open-source projects;
it does not copy their runtime or make them dependencies:

- [LeanDojo](https://github.com/lean-dojo/LeanDojo) treats proof states,
  tactics, and premises as explicit interaction data. The relay therefore asks
  each worker to record the named target, before/after goals, attempted proof
  step, and dependencies rather than returning prose-only confidence.
- [Pantograph](https://github.com/stanford-centaur/Pantograph) exposes Lean goal
  states through executable commands and reports failed tactics as stateful
  feedback. The relay therefore requires executable checker output and carries
  failed attempts and remaining goals into the next turn.
- [miniF2F](https://github.com/openai/miniF2F) freezes statement identity and
  uses consistent named problems for reproducible comparison. The relay
  therefore treats statement weakening, target renaming without a mapping, or
  proving a helper instead of the requested theorem as an unresolved result.

These principles complement the prover-native dependency checks already used
by the formal-proof profile: Rocq/Coq `Print Assumptions`, Lean `#print axioms`,
and Isabelle `thm_oracles` or their project-appropriate equivalents.

## Backend Contract

`internal/bridge/orchestration_relay.go:composeRelayPromptWithWorkerSlot`
selects the specialized contract only when
`internal/bridge/profiles/registry/registry.go:UsesSpecialRules` identifies the
`formal-proof` profile.

Collaboration cycles:

1. The proof author freezes the original named statement, records the initial
   proof state and trust boundary, makes the smallest faithful proof change,
   runs the narrow checker, and leaves an obligation ledger.
2. The proof auditor independently reopens the target, compares the checked
   statement with the requested statement, replays checks, inspects remaining
   goals and dependencies, scans for trust shortcuts, and fixes safe defects.
3. Later cycles continue from the newest remaining goal or failed check rather
   than restarting proof search.

Debate cycles:

1. The proposer states one falsifiable proof claim: exact target, premises,
   proof-state transition, tactic/lemma path, and checker expected to validate
   it.
2. The critic attacks the strongest claim with counterexamples, missing cases,
   type/quantifier mismatches, circular dependencies, hidden axioms, statement
   weakening, and an executable disconfirming check; it offers a repaired claim
   or proof path when possible.
3. Later cycles must answer the strongest surviving objection with new proof
   state or checker evidence.

Every non-final handoff names the target, current goals, files changed, exact
commands and exit status, trust/dependency result, and unmet obligations. The
last scheduled turn produces a user-ready adjudication that separates checked
facts from assumptions. A successful build with `sorry`, `admit`, `Admitted`,
new axioms, oracle-tainted facts, weakened statements, or remaining goals is not
described as a completed proof.

## Data And Protocol Impact

- No stored fields or wire payloads change.
- Existing role identifiers (`implementer`, `reviewer`, `proposer`, `critic`)
  remain stable for frontend rendering and persisted event compatibility.
- Existing `turn.start` prompt text exposes the selected contract to the local
  authenticated browser; public-share sanitization continues to remove it.
- Existing structured `run.conclusion` remains derived from visible CLI
  handoffs. Bridge supplies stronger instructions but does not invent a proof
  verdict or add unscheduled work.

## Implementation Steps

1. Add profile-scoped mode/role and final-turn guidance behind the profile
   registry.
2. Require proof-state, statement-identity, checker, and dependency evidence in
   formal-proof collaboration and debate prompts.
3. Preserve the existing generic contracts byte-for-byte for the default
   profile.
4. Add focused tests for both modes, later-cycle behavior, final adjudication,
   forbidden proof shortcuts, and default-profile compatibility.
5. Update architecture and pass-through orchestration documentation.

## Exit Gates

- Formal-proof collaboration prompts contain proof-author/auditor duties,
  before/after proof state, unchanged target, reproducible checks, dependency
  audit, and unresolved-obligation handoff requirements.
- Formal-proof debate prompts contain falsifiable proof claims, adversarial
  counterexample/statement/dependency checks, and evidence-based adjudication.
- The final formal-proof turn cannot describe compile-only or placeholder-based
  output as a completed proof.
- Default-profile prompt contract tests continue to pass unchanged.
- No frontend source or protocol payload changes are needed for this feature.
- `/usr/local/go/bin/go test ./internal/bridge/...` and the full repository
  verification gates pass.

## Reviewer Q&A

### Why keep the existing role identifiers?

They are already persisted and rendered. Changing their semantics only inside
the formal-proof prompt gives the stronger workflow without a migration or UI
change.

### Why not automatically parse every prover's goal state?

The Bridge supports multiple native CLIs and proof assistants. A universal
parser would expand the runner/protocol boundary and still be incomplete. The
local CLI already has project-specific tools; requiring it to expose exact
commands and proof-state evidence keeps the relay auditable without pretending
the Bridge is another prover.

### Does the Bridge decide whether the proof is valid?

No. The proof assistant's reproducible output and the visible worker conclusion
remain authoritative. Bridge only supplies a disciplined, profile-scoped
collaboration contract and relays the evidence.
