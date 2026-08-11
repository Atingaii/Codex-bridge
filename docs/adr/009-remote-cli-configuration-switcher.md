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
provider/model fields. Existing processes are not restarted and task-level
provider pinning is out of scope. Codex + Codex keeps distinct threads but
shares the machine's Codex provider/model configuration.

Official reset only removes Bridge-managed custom-provider fields. It neither
starts nor impersonates an official authorization flow.

## Consequences

- Old Bridges continue unchanged and do not expose the switcher.
- Hub cannot read stored API keys, although a fully compromised Hub capable of
  replacing both frontend code and Bridge public keys remains outside this
  threat model.
- No Redis or per-task provider revision is introduced.
- A Bridge upgrade is required for this feature.
