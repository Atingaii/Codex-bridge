# Formal-Proof Lightweight Workspace

## Goals

- Keep the user-selected project directory as the stable source directory for
  uploaded proof sources.
- Replace the multi-file Proof Harness and generated checker with one concise,
  persistent `proof-notes.md` evidence ledger.
- Preserve formal-proof collaboration/debate prompts, proof-assistant command
  evidence, native CLI continuity, and follow-up `runID` behavior.
- Reduce setup work, filesystem churn, prompt instructions, and agent time spent
  maintaining orchestration metadata instead of solving the proof task.

## Non-Goals

- No frontend, HTTP, WebSocket, SQLite, or protocol shape changes.
- No change to collaboration/debate role scheduling or turn budgets.
- No Bridge-owned semantic proof verdict and no generated proof checker.
- No migration or deletion of files in existing proof-run directories.

## Design

For a new `profile=formal-proof` run, Bridge uses the selected project directory
as the CLI working directory and creates only this ledger:

```text
<selected-project>/.codex-bridge/proof-notes/<runID>.md
```

Uploaded regular files and safely extracted archives are materialized in the
selected project directory. The ledger records the original task, detected proof
assistant, uploaded inputs, target/obligations, command evidence, blockers, and
decisions. Workers update this document only when it helps preserve material
state for a later turn; Bridge does not structurally validate it after every
turn.

A follow-up reuses the persisted `RunCWD` and records the latest user request in
the same document. `internal/bridge/orchestration_harness.go:appendFormalProofFollowup`
maintains only a delimited Bridge-owned block: each request is bounded, the
newest eight remain verbatim, and a counter records compacted predecessors.
Worker-maintained goals, obligations, validation evidence, blockers, and
decisions outside that block are not rewritten. Existing runs with the
historical `proof-harness/` layout remain usable: Bridge creates
`proof-notes.md` alongside those files when it is missing and does not remove
user work. Legacy unmarked follow-up entries are adopted into the bounded block
on the next continuation.

The generated prompt identifies the selected project directory, ledger, and
detected assistant. It asks workers to run the project-appropriate proof assistant
commands directly and record only important evidence. It does not require
`AGENTS.md`, `CLAUDE.md`, YAML state, decision directories, structural sync, or
`check.sh`.

## Data And Protocol Impact

- No stored fields or wire payloads change.
- `RunStartData.CWD` remains the user-selected project directory.
- Existing bootstrap notes remain browser-visible but describe the lightweight
  workspace rather than a Harness.
- The `formal-proof-harness-sync` Bridge note is no longer emitted.

## Implementation Steps

1. Retain run directory resolution, upload decoding, archive extraction, and
   proof-assistant detection.
2. Generate one `proof-notes.md` file and compact only its Bridge-owned
   follow-up block.
3. Remove generated scripts, metadata files, and per-turn structural sync.
4. Update focused tests and architecture/code-map documentation.

## Exit Gates

- New formal-proof runs create only the project-local `proof-notes/<runID>.md`
  ledger as a Bridge bootstrap artifact.
- No generated `check.sh`, YAML state, agent entry files, or governance
  subdirectories are created.
- Upload extraction and traversal rejection remain covered by tests.
- Follow-ups reuse the same project directory, preserve worker evidence, and keep a
  bounded recent-request window in `proof-notes.md`.
- Formal-proof collaboration/debate contract tests continue to pass.
- `/usr/local/go/bin/go test ./...` and `make doc-lint` pass.

## Reviewer Q&A

### Why keep a document at all?

Native context compaction and cross-CLI handoffs can discard detail. One ledger
retains the exact target, failed attempts, commands, and blockers without
turning orchestration metadata maintenance into a second task.

### Who verifies the proof now?

As before, Codex or Claude runs the actual project-specific Coq/Rocq, Isabelle,
or Lean commands. Their visible command output is the evidence; Bridge does not
replace the proof assistant with a shell wrapper.
