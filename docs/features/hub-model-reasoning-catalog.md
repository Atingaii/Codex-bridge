# Hub Model Reasoning Catalog

## Goals

- Keep the reviewed model and reasoning-level directory in the Hub so the UI
  does not depend on a private Bridge filesystem.
- Cover only model/CLI pairs whose documented native effort levels and
  defaults have been reviewed; do not equate provider thinking parameters
  with Codex or Claude effort fields.
- Refresh legacy presets when they are read or bound to an orchestration.
- Persist Claude Code effort in `settings.json` while preserving user values on
  reset.

## Non-goals

- Discovering or approving capabilities from a provider `/models` response.
- Changing the public Hub/Bridge protocol or the main production deployment.

## Data and protocol impact

No wire or SQLite schema changes. Existing `reasoningLevels`,
`reasoningDefault`, and `reasoningEffort` fields are normalized by the Hub.
Stale generic metadata saved by an earlier experimental build is removed on
read/bind for models absent from the reviewed catalog.

## Implementation and exit gates

1. Add the Hub catalog and normalization helpers.
2. Apply catalog metadata while listing, saving, and binding presets.
3. Materialize Claude `effortLevel` in global and isolated settings files.
4. Verify family coverage, legacy refresh, Claude reset, and low-parallelism Go
   tests.

## Reviewer Q&A

The Hub catalog is the only capability approval authority. Bridge is limited
to credential decryption and native-file materialization from the immutable
snapshot. It has no fallback catalog and never writes `model_catalog_json`.

Provider-native thinking switches and token budgets are not interchangeable
with `model_reasoning_effort` or Claude Code `effortLevel`. Therefore an
unreviewed DeepSeek, Kimi, GLM, Gemini, Qwen, MiniMax, or Doubao alias remains
usable but exposes no explicit effort selector and uses its native default.
