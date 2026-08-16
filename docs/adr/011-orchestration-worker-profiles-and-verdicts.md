> **DEPRECATED - the early-completion execution detail was replaced by Agent
> Verifier round continuation.**
>
> Current design: [Agent-Verifier Round Continuation](../features/agent-verifier-round-continuation.md).
> This ADR remains historical context for worker-profile isolation; do not
> implement its former local-hard-gate verifier behavior.

# ADR-011: Orchestration Worker Profiles And Verifier Verdicts

## Status

Accepted

## Context

The existing remote CLI configuration switcher changes a machine-wide native
configuration. That is useful for normal interactive use, but two Codex
workers in one orchestration still inherit the same provider and model.
Additionally, a bounded orchestration keeps consuming its requested turns even
when the workspace already contains a completed, independently checked proof.

## Decision

An orchestration may bind a saved, Bridge-encrypted CLI preset to each worker
slot. Hub authorizes the binding and relays the encrypted preset only to the
selected Bridge. Bridge decrypts it in memory and creates a private,
mode-0700 runtime home for that run and slot. The CLI process receives that
home and provider values through its process environment; it never mutates the
operator's global Codex or Claude configuration. An absent binding preserves
the existing native configuration path.

The first release supports `codex-a`, `codex-b`, `claude`, `claude-a`, and
`claude-b` slots. The new-run browser workflow explicitly binds every active
slot to a matching saved preset and does not offer a machine-global default.
Bindings
are snapshot data on the run-start payload and in Hub's private
`orchestration_worker_profiles` table, so a follow-up retains the same provider
identity unless the user explicitly changes the binding. Native thread/session
continuity remains slot-specific and is only resumed with the same profile
fingerprint.

The binding also snapshots the selected reasoning effort. Bridge applies this
setting in the private slot runtime, and turn/usage events carry safe model,
preset-name, and effort fields from that same binding. A machine active preset
is only the default for ordinary single-CLI sessions; it is not used to report
or execute a bound orchestration worker.

Hub is the sole capability and context-policy authority. Each binding includes
the resolved native effort levels/default and, for Claude, the reviewed context
window flags. Bridge validates snapshot self-consistency, decrypts the secret,
and writes native files; it does not infer model capabilities or generate a
Codex model catalog.

The former design applied deterministic local hard-evidence gates before Agent
Verifier calls. That sub-decision is superseded: durable reviewer nodes now
always invoke two bounded Agent Verifiers, using role 1 and role 2's presets in
fresh native sessions. Local command, handoff, and proof-checker observations
are supplied as recorded facts, not as an execution gate. See the current
feature design for the complete continuation and terminal-round semantics.

## Consequences

- Two Codex workers can use distinct providers/models in one workspace without
  touching each other's credentials or the machine-wide CLI configuration.
- Two Claude workers and mixed Claude/Codex workers receive the same isolation
  guarantee, with no cross-slot configuration fallback.
- Hub continues to store ciphertext only. Runtime homes and decrypted secrets
  stay on the private Bridge and are removed when the native session closes.
- The browser keeps its existing orchestration workflow. It gains compact slot
  profile selectors and a visible verifier outcome in the existing timeline,
  plan, and branch-map event data.
- The checker quorum is intentionally conservative. A successful command
  alone is insufficient, and the model evaluator may decline to terminate when
  evidence is incomplete. Each successful worker turn adds up to two bounded
  model calls until the run ends.
- Verifier execution requires an updated Bridge binary. Hub remains protocol
  compatible because the existing verdict event and terminal reason are reused.
- This does not add a general arbitrary-command verifier or an unbounded
  multi-agent scheduler. Those would require a separate security and capacity
  design.

`claude-claude`, slot-aware native resume, and the checker quorum are specified
by [ADR-012](012-dual-claude-and-quorum-verification.md).
