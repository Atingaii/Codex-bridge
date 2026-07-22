# Formal-Proof Offline Benchmarks

This directory contains a deterministic regression suite for proof artifacts
produced by Codex Bridge's `formal-proof` orchestration profile. It is inspired
by the obligation-oriented organization of CoqGym and PISA/AFP, but it is not a
sample or score from those full datasets.

Run every case with the real local proof assistants:

```bash
make benchmark-formal-proof
```

Or select one assistant and explicit binary paths:

```bash
COQC=/usr/bin/coqc ./scripts/run-formal-proof-benchmarks.sh --coq
ISABELLE=/home/ubuntu/.local/bin/isabelle \
  ./scripts/run-formal-proof-benchmarks.sh --isabelle
```

The runner does not access the network. It copies fixtures to temporary
directories, compiles reference solutions, enforces exact proposition
contracts, audits proof dependencies, and verifies that deliberately invalid
candidates are rejected. Temporary directories are deleted on exit.

## Layout

```text
coq/<case>/
  Task.v                 orchestration input with an intentional hole
  Reference.v            trusted local reference solution
  Contract.v             exact target/definition checks + Print Assumptions
  target.txt             named theorem audited by the runner
  invalid/
    shortcut-*.v         must fail the trust-shortcut scan
    contract-*.v         must fail the exact contract

isabelle/<case>/
  ROOT                    isolated Isabelle session
  Task.thy                orchestration input with an intentional hole
  Reference.thy           trusted local reference solution
  Contract.thy            exact target/definition checks
  Audit.thy               transitive theorem-oracle check
  target.txt              named theorem audited by Audit.thy
  invalid/
    shortcut-*.thy        must fail the fake-proof scan
    contract-*.thy        must fail the exact contract/session build
```

The task files are intentionally incomplete and are never compiled as positive
references. Give a task file to collaboration or debate mode, then place the
produced candidate in the corresponding `Reference` slot to evaluate it with
the same contracts.

## Prerequisites

- Coq 8.15 or newer, exposing `coqc`.
- Isabelle2025-2, exposing `isabelle`; the default lookup also checks
  `$HOME/.local/bin/isabelle`.
- Bash, `timeout`, `grep`, `sed`, `mktemp`, and other standard Unix tools.

Override `COQ_TIMEOUT` (default `120`) or `ISABELLE_TIMEOUT` (default `600`)
with timeout values in seconds when running on slower hardware.

See `SOURCES.md` for provenance/licensing and `results/` for recorded local
runs.
