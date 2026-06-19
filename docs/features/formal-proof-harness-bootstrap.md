# Formal Proof Harness Bootstrap

## Goal

Create a persistent, Chinese-language proof harness for `profile=formal-proof`
orchestration runs before the first scheduled CLI turn. The harness lives inside
a real run folder under the selected working directory, receives uploaded
projects or an empty project scaffold, and gives later Codex/Claude turns a
stable task shell that can evolve across follow-up prompts.

## Non-Goals

- Do not add new Hub APIs, WebSocket frame kinds, SQLite columns, or frontend
  controls.
- Do not make Bridge a semantic proof verifier.
- Do not reintroduce hidden verifier, remediation, or acceptance-assessment
  turns. The scheduled `maxTurns` budget remains the user-visible CLI relay
  budget.
- Do not affect default-profile orchestration.
- Do not touch production service processes such as `sparkon.cn`; verification
  uses local tests only.

## Current State

- Formal-proof runs already receive prompt-level proof guidance through
  `internal/bridge/profiles/registry`.
- Uploaded orchestration files are materialized under the run cwd by
  `internal/bridge/orchestration.go:PrepareOrchestrationPromptFiles`.
- Follow-up prompts reuse the same run id and the absolute run cwd reported in
  `run.start`.
- Bridge relays CLI turns and command evidence; it does not judge proof
  completion independently.

## Design

For new `profile=formal-proof` runs, Bridge performs a bootstrap step before
emitting `run.start` and before the first scheduled CLI turn. The bootstrap is
Bridge-side file setup, not a CLI turn, so it does not count against
`MaxTurns`.

Bridge resolves the base cwd from the request, creates:

```text
<base>/.codex-bridge/proof-runs/<runID>/
  project/
  AGENTS.md
  CLAUDE.md
  proof-harness/
    任务说明.md
    证明义务.md
    变更影响.md
    检查说明.md
    状态.yaml
    check.sh
    证明决策/
      000-初始任务.md
    evidence/
      README.md
    followups/
      README.md
```

Uploaded regular files are copied into `project/`. Uploaded zip/tar/tgz/tar.gz
archives are extracted into `project/` with path traversal protection. Text-only
tasks get an empty `project/` directory. The orchestration cwd is then locked to
the proof-run folder, so every later CLI turn runs with the harness and project
at stable relative paths.

The generated documents are intentionally Chinese because they are task-facing
and user-facing. `AGENTS.md` and `CLAUDE.md` are thin entry files that tell CLI
agents to read the harness before changing files. `proof-harness/状态.yaml` is
the machine-readable synchronization source, while Markdown files are the
human/agent-readable state.

`proof-harness/check.sh` provides three modes:

- `--harness`: structural drift checks for required harness files, state/doc
  version markers, at least one proof decision, and tracked file changes without
  an updated obligation marker.
- `--proof`: proof-assistant build/scan dispatch for Isabelle, Coq/Rocq, Lean4,
  or a generic fallback.
- `--all`: run both modes.

The script is a generic adapter. It can detect common Isabelle, Coq/Rocq, and
Lean4 project layouts, run the usual build command when the tool exists, and
scan for trust shortcuts such as `sorry`, `quick_and_dirty`, `Axiom`,
`Admitted`, `Parameter`, `Conjecture`, and Lean `axiom`/`unsafe`. Missing local
proof-assistant binaries produce an actionable skip message instead of causing
the harness bootstrap itself to fail.

Bridge also performs an internal structural sync check after each formal-proof
CLI turn. This check does not execute user-editable workspace scripts; it only
checks required harness files, version markers, decision records, state fields,
and obligation sections. The result is emitted as a Bridge note with category
`formal-proof-harness-sync`. If the structural sync check fails, Bridge includes
that note in the next relay prompt so the next CLI turn repairs harness drift
before claiming proof completion.

On resumed formal-proof runs, Bridge reuses the locked `RunCWD`. If the harness
already exists, Bridge does not rebuild it. Instead it appends a follow-up file
under `proof-harness/followups/` and updates `状态.yaml` with the latest prompt
sequence. This preserves original requirements while letting the harness evolve
when users continue the same conversation.

Bootstrap emits a Bridge `turn.delta` note with category
`formal-proof-harness-bootstrap` so the browser timeline shows where the run
folder and harness were created. It does not add a CLI turn or alter the relay
schedule.

## Data And Protocol Impact

- No protocol changes.
- `RunStartData.CWD` becomes the proof-run directory for new formal-proof runs.
  Hub already persists that absolute cwd and sends it back as `RunCWD` for
  follow-up prompts.
- Default-profile runs keep the old cwd and upload behavior.
- Public share sanitization already strips private `RunStartData.CWD` and
  internal Bridge notes.

## Implementation Steps

1. Add Bridge helper code for proof-run folder creation, archive extraction,
   harness document generation, follow-up recording, and prompt augmentation.
2. Call that helper only for `profile=formal-proof` before preparing uploaded
   prompt files and before native session creation.
3. Keep `MaxTurns` unchanged; the bootstrap is a non-CLI setup step.
4. Add focused bridge tests for text-only tasks, uploaded proof files, archive
   extraction, prompt/cwd changes, and resumed follow-up reuse.
5. Update architecture/code-map/change-impact docs.

## Exit Gates

- A new formal-proof text-only run creates a proof-run directory with
  `project/`, Chinese harness docs, `AGENTS.md`, `CLAUDE.md`, and executable
  `proof-harness/check.sh`.
- A formal-proof run with uploaded files places those files under `project/`
  and points the first CLI prompt at the proof-run folder and harness.
- Uploaded zip archives are extracted under `project/` and cannot write outside
  the proof-run directory.
- A resumed formal-proof run with `RunCWD` reuses the existing harness and
  records a follow-up without creating a new proof-run folder.
- Default-profile runs keep existing upload materialization and cwd behavior.
- Each completed formal-proof CLI turn emits a structural harness sync note;
  bootstrap and sync notes do not increase the scheduled `turn.start` count.
- Focused tests pass:
  `/usr/local/go/bin/go test ./internal/bridge`.
- Documentation lint passes:
  `make doc-lint`.

## Reviewer Q&A

**Q1: Why write files instead of only injecting prompt text?**

A: The user can continue the same proof conversation across multiple prompts,
native CLI sessions, and context compaction. Persistent harness files give every
later CLI turn a stable task entry point instead of relying on one prompt.

**Q2: Why keep the harness generic?**

A: Bridge should remain a relay and setup layer. The same shell/document
structure works for Isabelle, Coq/Rocq, Lean4, and text-only tasks, with
proof-assistant-specific behavior isolated to `check.sh` dispatch rules.

**Q3: Does the harness prove correctness?**

A: No. It prevents structural drift and records evidence expectations. Semantic
correctness still belongs to the proof assistant build/audit commands and the
user-visible CLI evidence.
