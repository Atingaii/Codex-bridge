# Structured Agent Dialogue Relay

## Goals

- Improve collaboration and debate turns by treating each CLI response as an
  explicit Agent-to-Agent handoff instead of replaying prose as conversation
  history.
- Keep Codex and Claude in their existing long-lived native sessions while
  forwarding only the newest cross-Agent facts, request, evidence, and risk.
- Allow a run to finish before its configured turn ceiling only after the
  reviewer or critic independently confirms a resolved result.
- Preserve the current browser workflow, role order, worker selection, run
  continuity, stored events, and final user-visible CLI response.
- Retain a bounded prose fallback for older CLIs or malformed handoffs.

## Non-Goals

- Do not restore the removed CCB backend, mailbox daemon, external Agent
  runtime, or filesystem job queue.
- Do not add HTTP endpoints, WebSocket frames, SQLite columns, frontend
  controls, or new orchestration modes.
- Do not let implementers or proposers end a run based on their own success
  claim.
- Do not replace the selected fixed role/CLI schedule with model-selected
  routing or parallel execution.
- Do not interpret delivery of a reply as proof that the task is complete.

## Data And Protocol Impact

There is no wire or persistence schema change. The Bridge parses two optional,
anchored lines already used by orchestration replies:

```text
Msg: to=reviewer; intent=review; need=verify parser regression
Handoff: status=needs_next; changed=parser.go; verified=go test ./...; next=review edge case; risks=none
```

`internal/bridge/orchestration_relay.go:newOrchestrationTurnRecordWithSlot`
builds an internal relay packet with sender role/CLI plus `to`, `intent`,
`need`, `status`, `changed`, `verified`, `next`, and `risks`. Unknown fields are
ignored. A packet is advisory unless both anchored lines parse successfully;
the fixed schedule remains authoritative.

The next Agent receives:

- the sender and intended recipient;
- the requested intent and result;
- changed-file, verification, next-action, and risk fields;
- objective command events and exit codes;
- a small prose excerpt only when structured fields are absent or incomplete.

Returning workers continue to rely on their own native session memory. Bridge
does not resend that worker's older output. `MaxTurns` remains a hard ceiling.

## Convergence Rules

`internal/bridge/orchestration_relay.go:relayCanConverge` may stop scheduling
additional turns only when all of these conditions hold:

1. At least two Agents/worker slots have participated in the prompt sequence.
2. The current role is `reviewer` in collaboration mode or `critic` in debate
   mode.
3. The anchored machine handoff says `status=resolved`, `to=user`,
   `intent=final`, `next=none`, and `risks=none`.
4. The handoff names non-empty verification evidence or the turn contains a
   successful command event.
5. For the formal-proof profile, the reviewing turn itself must contain a
   successful checker/audit command event. Prose-only certification cannot
   converge a proof run early.

`needs_next` means delivered but incomplete. `blocked` means no useful next
turn can finish without a missing prerequisite. Missing or malformed fields
mean “continue until the turn ceiling”, not success.

## Context And Fallback

`internal/bridge/orchestration_relay.go:formatRelayPriorTurn` prefers the
structured packet and command evidence. It includes at most a short result
excerpt when the packet lacks fields needed by the recipient. If no structured
packet exists, the current bounded handoff/result formatter remains active.

This design deliberately keeps delivery and completion separate: parsing an
Agent message confirms only that Bridge can route its facts. Completion still
requires the designated reviewing role and evidence described above.

## Implementation Steps

1. Parse anchored `Msg:` and `Handoff:` lines into an internal relay packet and
   keep the existing summary parser as fallback.
2. Render structured task packets and objective command evidence in the next
   prompt while avoiding repeated native-session history.
3. Prompt each non-final Agent to emit the compact contract and tell reviewing
   roles how to explicitly close a verified run.
4. Add conservative reviewer/critic convergence to
   `internal/bridge/orchestration.go:run` without changing
   the public run events.
5. Cover parsing, malformed fallback, compact rendering, convergence, formal
   proof safeguards, and unchanged role scheduling with tests.

## Exit Gates

- Structured handoffs render as compact relay packets without copying the full
  prior response.
- Malformed and legacy handoffs still use bounded prose fallback.
- Implementer/proposer `resolved` claims cannot finish a run early.
- Reviewer/critic confirmation can finish an ordinary run early.
- Formal-proof early completion requires successful reviewing-turn command
  evidence.
- Role order and Codex worker-slot selection remain unchanged.
- `/usr/local/go/bin/go test ./internal/bridge`
- `/usr/local/go/bin/go test ./...`
- `CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .`
- `make doc-lint`

## Reviewer Q&A

**Q: Why not copy the reference project's mailbox runtime?**

A: ProofBridge already owns two durable native CLI sessions and a persisted
run timeline. Adding another daemon and job store would duplicate those
responsibilities. The useful behavior is the explicit message/completion
contract and compact task memory, which fit inside the current relay.

**Q: Why keep fixed role order when messages name a recipient?**

A: The current UI promises collaboration and debate rounds, and the two native
workers are already mapped deterministically. Recipient metadata catches bad
handoffs and guides the next prompt without introducing nondeterministic
routing or silently changing the selected worker pair.

**Q: Why can only reviewer or critic end early?**

A: An implementation or proposal is delivery, not independent acceptance.
Requiring the opposite role prevents a single Agent from certifying its own
claim while still avoiding empty rounds after agreement is evidenced.
