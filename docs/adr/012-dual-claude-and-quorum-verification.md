# ADR-012: Dual Claude Workers And Quorum Verification

## Status

Accepted

## Context

ADR-011 introduced per-worker profile isolation for Codex + Codex and the
Claude + Codex pair. A single Claude participant is not sufficient for users
who need two separately configured Claude workers. Its single native session
would also make two nominal workers share history and provider state.

A single completion checker is likewise too weak as the authority for ending a
proof run early. The initial implementation therefore used three local checks;
ADR-011 now supersedes that execution detail with two independent bounded Agent
Verifier calls whose structured results still pass through the same
three-check quorum.

## Decision

`claude-claude` is a third worker pair. It uses `claude-a` and `claude-b`
slots. Each slot has its own saved preset binding, private runtime directory,
Claude process, deterministic native session id, transcript lookup, and
resume-mode state. Claude + Codex retains the legacy `claude` and `codex`
slots for compatibility.

Early completion uses a serial, visible checker quorum. Two bounded Agent
Verifiers independently evaluate the three named checks:

1. **handoff checker**: requires a machine-readable resolved final handoff
   with no next action or risk;
2. **evidence checker**: requires successful command evidence and, for formal
   proofs, a recognized proof checker;
3. **independence checker**: requires the assigned reviewer/critic role and
   evidence from at least two participating slots (or the durable graph's
   independent reviewer boundary).

Every check result is included in the persisted verdict. A run ends early
only when every checker passes. A rejection, an incomplete check, a malformed
model response, verifier disagreement, or a verifier execution error is a
continue verdict. Bridge uses both worker presets in fresh native sessions and
keeps local command/formal-check evidence gates.

The isolated production surface is `proofbridge.sparkon.cn`. It has a separate
Hub service, configuration directory, SQLite file, JWT secret, administrator,
and Bridge enrollment token. Cloudflare provides DNS, proxying, and edge TLS;
Caddy terminates the origin connection and routes only this hostname to the
isolated Hub port. The existing `sparkon.cn` Hub and SQLite database remain
untouched.

## Consequences

- Users can run two different Claude presets in one orchestration without
  cross-contaminating credentials, model configuration, or native history.
- The protocol is extended with `claude-claude` and slot-aware native resume
  metadata; legacy Claude metadata remains available for Claude + Codex.
- The checker quorum borrows the useful DeepSeek Harness AgentTeams ideas of
  explicit responsibility, durable state, dependency boundaries, and visible
  activity. It deliberately does not import its daemon, mailbox, or extra
  model runtime.
- The checker quorum remains conservative and bounded for the 2-core, 4-GB
  host. It spends two additional model calls only when local hard gates identify
  a successful worker turn as a completion candidate.
