# User-Scoped CLI Provider Presets

## Goals

- Store provider preset metadata at user scope so every machine and workspace
  owned by that user can select the same Codex or Claude preset.
- Store provider credentials in a Hub-side user vault encrypted at rest, and
  wrap them to the selected Bridge only when that machine needs the preset.
- Keep native CLI activation machine-scoped: applying or resetting a preset on
  one Bridge must not change another machine's native CLI configuration.
- Preserve every existing machine-scoped preset during migration without
  merging presets that happen to share a name.

## Non-Goals

- Do not share presets between users.
- Do not make one machine's active native configuration a user-wide default.
- Do not require all of a user's Bridges to be online merely to list preset
  metadata.
- Do not interrupt active chat or orchestration processes.

## Data And Protocol Impact

- `internal/store.Store.Migrate` replaces the legacy all-in-one
  `cli_config_presets` layout with user-owned preset metadata,
  `cli_config_preset_credentials` for Bridge-specific ciphertext, and
  `cli_config_active_presets` for machine-specific activation.
- Every legacy row becomes a distinct user preset. Its existing encrypted
  secret and active flag become credential and activation rows for the legacy
  agent.
- `internal/protocol.Envelope` adds a configuration export request. An upgraded
  Bridge decrypts the browser envelope and encrypts the key to Hub's vault
  receiver. Hub then encrypts the vault plaintext at rest and wraps it to a
  target Bridge only while resolving a user preset.
- Existing agent-oriented HTTP paths remain stable. Their `agentID` identifies
  the Bridge used to test, apply, edit, or materialize a credential, while list
  and ownership checks operate at user scope.

## Implementation Steps

1. Normalize preset metadata, per-agent credentials, and per-agent activation
   into three SQLite tables with a lossless startup migration.
2. List and authorize presets by authenticated user, while resolving active and
   credential availability against the selected agent.
3. Add Bridge-to-Hub vault migration. Prefer an online, owner-matched source
   whose advertised key ID matches its stored legacy envelope.
4. Lazily materialize a target credential when applying, editing with a blank
   API key, testing an existing preset, or binding orchestration workers.
5. Show the same user preset list on every eligible machine and explain when a
   credential must be re-entered because no source Bridge can rewrap it.

## Exit Gates

- A preset created on machine A appears for machine B owned by the same user.
- Machine B can apply or bind a vault-backed preset even while machine A is
  offline; the API key is never returned to browser responses.
- Presets never appear for another user.
- Applying or resetting on one machine does not change another machine's active
  marker.
- Deleting an agent does not delete user presets.
- Existing preset rows migrate without loss and remain usable on their original
  machines.
- Focused store, Hub, Bridge, frontend, and full Go tests pass.

## Reviewer Q&A

**Why use a Hub-side vault?**

The product requirement is a complete user-level model library that works even
when the machine where a key was first entered is offline. The vault is the
smallest reliable way to provide that behavior. Values are encrypted at rest
with a key derived from the Hub auth secret and are never serialized to the
browser, events, prompts, public shares, or logs.

**What if the only Bridge holding a credential is offline?**

An existing legacy preset remains selectable and usable on its original
machine. Its original upgraded Bridge must come online once to migrate the key
to the vault; newly saved presets are vault-backed immediately.

**Why is activation still machine-scoped?**

Activation edits native files under that machine's CLI home. Making it global
would falsely imply that all machines changed together and could overwrite
different local login choices.
