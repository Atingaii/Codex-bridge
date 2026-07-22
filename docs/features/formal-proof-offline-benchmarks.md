# Formal-Proof Offline Benchmarks

## Goal

Provide a bounded, reproducible local benchmark suite for the
`formal-proof` orchestration workflow. The suite uses CoqGym-style Coq proof
obligations and PISA/AFP-style Isabelle obligations to verify that a produced
proof project compiles, preserves the requested theorem, contains no accepted
proof placeholder, and has no proof-assistant oracle or global-assumption
dependency.

The benchmark is deliberately offline. A normal run uses checked-in fixtures,
the local `coqc` binary, and the local Isabelle launcher; it never downloads a
dataset or calls a model/API.

## Non-Goals

- Do not change orchestration scheduling, prompts, APIs, protocols, or storage.
- Do not claim comparability with full CoqGym, PISA, AFP, or miniF2F leaderboard
  scores. This is a compact product regression suite, not a model leaderboard.
- Do not vendor the multi-gigabyte CoqGym/PISA datasets, AFP sessions, or proof
  assistant distributions.
- Do not accept compile-only evidence when a theorem statement was weakened or
  a proof was replaced with `Admitted` / `sorry`.
- Do not require network access during benchmark execution.

## Current State

Formal-proof runs already create a persistent proof harness through
`internal/bridge/orchestration_harness.go:prepareFormalProofHarness` and inject
proof-assistant-specific review guidance through
`internal/bridge/profiles/formalproof/formalproof.go:RelayGuidance`. Focused Go
tests verify prompt and harness behavior, but they do not compile a varied set
of proof projects with real Coq and Isabelle binaries.

## Design

Fixtures live under `benchmarks/formal-proof/` and are split by assistant and
obligation. Each obligation contains:

- a task file with an explicit proof hole for use as orchestration input;
- a trusted local reference solution used to prove that the fixture is valid;
- a contract file that restates the exact expected target proposition;
- an audit file or command that checks proof dependencies;
- intentionally invalid candidates proving that the scorer rejects placeholders
  and weakened statements.

The cases are small enough to compile on a developer machine but exercise
different proof shapes: induction over arithmetic recursion, accumulator/list
invariants, transition/algorithm invariants, and exact theorem contracts. The
format follows popular benchmark practice without copying opaque dataset
snapshots: Coq cases separate an environment/goal from proof completion in the
style of CoqGym, while Isabelle cases are session-buildable theorem obligations
in the style used by PISA over AFP.

`scripts/run-formal-proof-benchmarks.sh:run_coq_case` compiles each Coq reference
in an isolated temporary directory, compiles a contract against the named
theorem, requires `Print Assumptions` to report `Closed under the global
context`, and scans candidate source for trust shortcuts.

`scripts/run-formal-proof-benchmarks.sh:run_isabelle_case` builds each Isabelle
session in an isolated temporary directory. Its checked-in contract consumes
the named target at the requested proposition, and its audit fails when
`Thm_Deps.all_oracles` reports any transitive oracle dependency. Candidate
source is also scanned for fake-proof commands and global axiomatization.

Each valid case must pass. Each invalid candidate must fail for its expected
class (shortcut scan or contract/build failure), so a broken scorer cannot make
the suite pass merely by accepting every input. `Makefile:benchmark-formal-proof`
is the developer entry point. Tool paths remain overridable through `COQC` and
`ISABELLE`; the runner falls back to `$HOME/.local/bin/isabelle` when Isabelle
is not on `PATH`.

Source, revision, and license decisions are recorded in
`benchmarks/formal-proof/SOURCES.md`. Local execution evidence is recorded under
`benchmarks/formal-proof/results/`; benchmark runs do not rewrite a tracked
result file automatically.

## Data And Protocol Impact

- No HTTP, WebSocket, SQLite, config, or runner-interface changes.
- The generated proof-harness checker chooses the detected target assistant,
  limits builds with `PROOF_CHECK_TIMEOUT`, scans only that target assistant's
  source subtree, and fails on trust shortcuts or missing toolchains. This lets
  Isabelle-to-Coq conversion runs retain uploaded source material without
  building or scanning it as the completed Coq target.
- New repository-only benchmark fixtures live in `benchmarks/formal-proof/`.
- New developer command: `make benchmark-formal-proof`.
- Execution creates only temporary build directories and proof-assistant output;
  no generated `.vo`, `.glob`, Isabelle heap, or session database is checked in.

## Implementation Steps

1. Add source/license metadata and a fixture manifest.
2. Add two Coq and two Isabelle obligations with positive references and
   negative candidates.
3. Add an offline shell runner with isolated builds, timeouts, statement
   contracts, placeholder scans, and dependency audits.
4. Add the Make target and document toolchain prerequisites.
5. Harden generated proof-run checks with target selection, timeouts, and
   fatal shortcut detection.
6. Run both real proof assistants, record versions/results, then run doc lint.

## Exit Gates

- `COQC=/usr/bin/coqc ISABELLE=/home/ubuntu/.local/bin/isabelle
  ./scripts/run-formal-proof-benchmarks.sh --all` passes without network access.
- Both Coq reference solutions compile and report no global assumptions.
- Both Isabelle reference sessions build and report no theorem oracles.
- Coq `Admitted` and Isabelle `sorry` candidates are rejected by source audit.
- Weakened Coq and Isabelle target statements are rejected by their contracts.
- `make doc-lint` passes.
- The exact local tool versions, case counts, command, and result are recorded
  under `benchmarks/formal-proof/results/`.

## Reviewer Q&A

**Q1: Why not download the complete upstream datasets in the script?**

A: CoqGym constituent projects and AFP entries have independent licenses and
version constraints, while complete PISA/AFP installations consume substantial
time and disk. Checked-in, provenance-documented fixtures make the product gate
fast, offline, and deterministic. Full upstream evaluation remains an explicit
research task.

**Q2: Does a passing reference benchmark prove orchestration quality?**

A: It proves the local verifier can distinguish faithful proofs from the
included failure modes. End-to-end orchestration quality additionally needs a
model-driven run, which is intentionally excluded from this deterministic gate.

**Q3: Why test invalid candidates?**

A: Proof assistants intentionally allow some trust shortcuts. Negative cases
show that the product-level acceptance policy rejects `Admitted`, `sorry`, and
statement weakening even when a permissive compile might otherwise succeed.

**Q4: Why pin source metadata but not install toolchains automatically?**

A: Toolchain installation is large and machine-specific, especially Isabelle.
The runner reports a missing prerequisite immediately and accepts explicit
binary paths, keeping normal benchmark execution offline and auditable.
