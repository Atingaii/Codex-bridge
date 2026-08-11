# ADR-009: Remote CLI Configuration Switcher

## Status

Accepted

## Context

Provider selection currently comes from private-machine Codex and Claude Code
configuration. Editing those files is too demanding for ordinary Hub users.

## Decision

Add a capability-gated, machine-scoped switcher. Hub stores named presets and
API-key ciphertext. Each Bridge advertises a persistent P-256 public key;
browser WebCrypto encrypts keys directly to that key. Hub relays bounded test,
apply, and official-reset controls. Bridge decrypts and edits only native
provider/model fields. For Codex, Bridge writes a Bridge-owned model catalog
from the provider's tested `/models` response and points `model_catalog_json`
at it so the selected custom model is recognized by the native picker. Unknown
models use a conservative 200k context window instead of an unverified provider
claim. Existing processes are not restarted and task-level provider pinning is
out of scope. Codex + Codex keeps distinct threads but shares the machine's
Codex provider/model configuration.

For Claude Code, Bridge writes the requested provider model as the native
`model` and `ANTHROPIC_MODEL`, registers it with
`ANTHROPIC_CUSTOM_MODEL_OPTION`, and maps the Opus, Sonnet, Haiku, Fable, and
subagent defaults to that same provider model. This avoids coupling to a
versioned Anthropic model ID that a later Claude Code release can retire.
Bridge-launched Claude processes receive the actual provider model through
`--model`; usage events record that same ID. A manually configured
`bridge.claude_model` remains a direct CLI model argument for compatibility.
Bridges upgrading from the short-lived versioned-slot implementation restore
the pre-existing override and migrate the active preset on startup.

Official reset only removes Bridge-managed custom-provider fields. It neither
starts nor impersonates an official authorization flow.

## Consequences

- Old Bridges continue unchanged and do not expose the switcher.
- Hub cannot read stored API keys, although a fully compromised Hub capable of
  replacing both frontend code and Bridge public keys remains outside this
  threat model.
- No Redis or per-task provider revision is introduced.
- A Bridge upgrade is required for this feature.
- Switching with another native configuration tool remains last-writer-wins;
  Bridge-owned catalog files do not modify MCP, Skill, permission, or session
  data.
