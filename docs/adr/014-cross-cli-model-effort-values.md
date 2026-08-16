# ADR-014: Measured Model Effort Values Across CLIs

## Status

Accepted

## Context

ADR-013 approved effort values per model and CLI pair. That made the same model
show effort controls under Codex but no controls under Claude Code even though
Bridge already writes the selected value to the CLI-specific native key:
`model_reasoning_effort` for Codex and `effortLevel` for Claude Code.

The Hub needs an explicit list of current models. A model appearing in both CLI
transports should share the same measured value set, while different models
must not receive values that have not been observed for them.

## Decision

Hub keeps an explicit list of current models and the effort values observed or
documented for each model. A listed model exposes the same reviewed set through either CLI.
Bridge writes the selected value only to the native key for the chosen CLI:
`model_reasoning_effort` for Codex and `effortLevel` for Claude Code.

The initial 2026 catalog uses a supplied evaluation table for value coverage
and vendor documentation for model-name verification. It includes GPT 5.5 and
5.6, Claude Opus 4.8 and Claude 5, DeepSeek V4, Kimi K3, Grok 4.6, Gemini 3.5
through 3.7 Flash, GLM 5.2, Qwen 3.8 Max, and the exact measured
`muse-spark-1.2` gateway alias. Provider-prefixed aliases are normalized;
official and measured Grok punctuation aliases are both accepted. Models not
present in the evaluation table remain usable but receive no explicit effort
unless separately verified. Previously reviewed GPT-5.6 Terra and Claude 4.6
through 4.8 entries remain in the catalog so this change does not remove
existing controls.

DeepSeek's official thinking-mode documentation supersedes the narrower
evaluation-table coverage for `deepseek-v4-flash` and `deepseek-v4-pro`. Both
models expose `low`, `high`, and `max`; thinking is enabled by default at
`high`. Requests for `medium` or `xhigh` map to `high`, so those aliases are not
shown as distinct Hub choices.

Provider model discovery does not mutate this policy catalog. The explicit
list remains reviewable and deterministic, and unknown model names continue to
use the CLI or provider default.

Model-name verification sources:

- OpenAI: <https://developers.openai.com/api/docs/models>
- Anthropic: <https://docs.anthropic.com/en/docs/about-claude/models/overview>
- Google: <https://ai.google.dev/gemini-api/docs/models>
- xAI: <https://docs.x.ai/docs/models>
- DeepSeek: <https://api-docs.deepseek.com/quick_start/pricing>
- DeepSeek thinking mode: <https://api-docs.deepseek.com/guides/thinking_mode>
- Moonshot AI: <https://platform.moonshot.cn/docs/api/chat>
- Alibaba Cloud Model Studio: <https://help.aliyun.com/zh/model-studio/getting-started/models>
- Zhipu AI: <https://docs.bigmodel.cn/cn/guide/models/text/glm-5>

`muse-spark-1.2` is retained as an exact evaluation/gateway alias because no
authoritative vendor model page was identified; it does not grant matching to
other `muse-*` names.

## Consequences

- The same listed model exposes the same selectable values in either CLI.
- Each model receives only the values present in its measured set.
- Adding a current model requires one explicit catalog entry rather than two
  CLI-specific entries.
- A provider may ignore or reject a value; that runtime behavior remains
  observable as an upstream error and does not change the Hub policy silently.
- Existing saved presets acquire the current catalog on read or bind without a
  SQLite migration.
