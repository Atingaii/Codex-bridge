# ADR-013: Hub Model Catalog And Claude Effort Persistence

## Status

Accepted

## Context

Model presets are owned by Hub but previous reasoning-level approval depended
on a Bridge-local Codex catalog. This produced inconsistent choices across
machines and caused saved GPT presets to show no selectable effort. Claude
Code was also started with `--effort` but did not persist the same preference in
its documented `settings.json` field.

## Decision

Hub owns an explicit, CLI-scoped reviewed catalog for model families whose
native effort field and exact levels have been verified.
It normalizes reasoning levels and defaults whenever presets are saved, listed,
applied, or bound to an orchestration. Provider `/models` responses remain a
connectivity/model-name aid only; they cannot grant a new reasoning level.

Bridge receives an immutable Hub-authorized effort and context snapshot with
the encrypted worker binding. It does not infer capabilities, approve browser
metadata, or generate a Codex `model_catalog_json`. Codex receives only native
`config.toml` and `auth.json`; Claude Code receives the top-level `effortLevel`
and reviewed context environment values in both its managed and isolated
`settings.json`. The global manager preserves and restores the operator's
previous values on official reset. The existing Claude `--effort` remains a
per-process compatibility override.

GPT-5.6 exposes `none`, `low`, `medium`, `high`, `xhigh`, and `max`, with
`medium` as the catalog default. Claude effort levels are cataloged only for
the matching Claude CLI model families. DeepSeek, Kimi, GLM, Gemini, Qwen,
MiniMax, and Doubao aliases do not receive generic Codex/Claude effort enums:
their provider-side thinking switches or budgets are not equivalent to these
native CLI fields. Until an exact mapping is reviewed, Hub sends no explicit
effort and the CLI/provider uses its native default.

## Consequences

- Presets see a stable catalog regardless of the private Bridge filesystem.
- Legacy presets immediately expose newly reviewed levels without browser or
  database migration.
- A second Claude/Codex worker can use a distinct effort without leaking that
  value into another worker's runtime configuration.
- Unknown models still use provider/CLI defaults and do not receive invented
  reasoning controls.
- Legacy experimental presets with invented levels are cleared when Hub reads
  or binds them, without a SQLite migration.
