# Hub Model Reasoning Catalog

## Goals

- Keep the reviewed model and reasoning-level directory in the Hub so the UI
  does not depend on a private Bridge filesystem.
- Persist and display the exact provider Base URL that the Bridge successfully
  probes, including a discovered `/v1` prefix, instead of the browser's raw
  convenience input.
- Cover only model/CLI pairs whose documented native effort levels and
  defaults have been reviewed. Reuse a model's measured values across CLI
  transports while writing only the selected CLI's native key.
- Refresh legacy presets when they are read or bound to an orchestration.
- Persist Claude Code effort in `settings.json` while preserving user values on
  reset.

## Non-goals

- Discovering or approving capabilities from a provider `/models` response.
- Changing the public Hub/Bridge protocol or the main production deployment.

## Data and protocol impact

No wire or SQLite schema changes. Existing `reasoningLevels`,
`reasoningDefault`, and `reasoningEffort` fields are normalized by the Hub.
`internal/hub/cli_config.go:authorizeReviewedReasoning` also replaces the
submitted Base URL with `protocol.CLIConfigResult.BaseURL` after a successful
Bridge probe, so create and update persist the endpoint that actually passed.
Stale generic metadata saved by an earlier experimental build is removed on
read/bind for models absent from the reviewed catalog.

## Implementation and exit gates

1. Add the Hub catalog and normalization helpers.
2. Apply catalog metadata and the probed canonical Base URL while saving and
   binding presets.
3. Materialize Claude `effortLevel` in global and isolated settings files.
4. Verify family coverage, legacy refresh, Claude reset, and low-parallelism Go
   tests.

## Reviewer Q&A

The Hub catalog is the only capability approval authority. Bridge is limited
to credential decryption and native-file materialization from the immutable
snapshot. It has no fallback catalog and never writes `model_catalog_json`.
The Bridge remains the endpoint probe authority: users may enter a root or
versioned URL, but only the successful candidate returned by the Bridge is
stored and shown in later preset and orchestration views.

Codex writes the selected effort to `model_reasoning_effort`; Claude Code writes
it to `effortLevel`. The allowed values come from each model's measured or
vendor-documented set, not a universal enum: for example GPT-5.6 Sol has five
levels, GLM 5.2 has `high` and `max`, and DeepSeek V4 Flash has `low`, `high`,
and `max` with thinking enabled at the `high` default. DeepSeek's `medium` and
`xhigh` inputs map to `high`, so Hub does not expose them as distinct choices.
An unreviewed alias remains usable but exposes no explicit effort selector and
uses its native default.
