# Orchestration Strategy Optimization

## Goal

Make the existing collaboration and debate modes feel more like structured
multi-agent work while keeping token use bounded. The Bridge should pass compact
handoffs between Claude Code and Codex CLI instead of replaying large raw turn
transcripts.

## References

- AutoGen teams use round-robin group chat, selector teams, and handoff-based
  teams for multi-agent coordination:
  <https://microsoft.github.io/autogen/stable/user-guide/agentchat-user-guide/tutorial/teams.html>
- LangGraph handoffs emphasize explicit context engineering and summarized
  handoff payloads instead of passing full sub-agent history:
  <https://docs.langchain.com/oss/python/langchain/multi-agent/handoffs>
- CrewAI separates sequential and hierarchical processes, with manager-style
  planning, delegation, and validation:
  <https://docs.crewai.com/en/concepts/processes>
- OpenAI Swarm models lightweight orchestration around agents, handoffs, and
  context variables:
  <https://github.com/openai/swarm>

## Non-Goals

- Do not add a new model, queue, database, or resident multi-agent runtime.
- Do not change `internal/protocol.Envelope` or add new event kinds.
- Do not change orchestration create/continue/cancel endpoints.

## Current State

- `internal/bridge/orchestration.go:run` alternates turns between Claude and
  Codex.
- `internal/bridge/orchestration.go:composeOrchestrationPrompt` appends prior
  turn content directly, which can become token-heavy and does not define a
  shared handoff contract.
- `internal/hub/orchestration.go:compactOrchestrationContext` already compacts
  prior persisted events for follow-up prompts.

## Design

Keep the existing two modes but make each mode a protocolized workflow:

| Mode | Updated strategy |
| --- | --- |
| `collaboration` | Claude acts as builder/implementer, Codex acts as reviewer/verifier, and each turn ends with a compact handoff containing status, changed files, verification, next action, and risks. |
| `debate` | Claude proposes a concrete thesis or patch, Codex tries to falsify it with evidence, and the final turn synthesizes accepted claims, rejected claims, verification, and remaining uncertainty. |

Bridge prompt construction uses three context layers:

1. Latest user task, always authoritative.
2. Follow-up compacted run context from Hub when `Resume=true`.
3. Compact prior-turn handoffs generated locally from visible outputs and tool
   summaries.

The Bridge must avoid sending full raw previous outputs unless they are short.
Each prior turn in the next prompt should be capped and should prefer parsed
handoff fields when the agent provided them. The visible `Handoff:` line remains
the compatibility surface, while `internal/bridge/orchestration.go:parseHandoffFields`
stores the important values as structured turn state for later prompts.

Agents are instructed to end visible responses with compact routing and handoff
lines:

```text
Msg: to=<next-role|user>; intent=<implement|review|challenge|final>; need=<one request or none>
Handoff: status=<needs_next|blocked|resolved>; changed=<files or none>; verified=<commands or none>; next=<one action>; risks=<open issue or none>
```

`Msg:` keeps inter-agent communication targetable and compact. `Handoff:`
remains the compatibility line for status, verification, next action, and
early-stop detection.

The `status=resolved` signal may end an orchestration early after at least two
turns, but only when the same visible answer includes a user-facing conclusion.

When a run reports changed files, unresolved risks, turn errors, or failed tool
commands, the Bridge adds one lightweight final verifier turn. It is skipped for
clean resolved runs with verification, so successful no-change runs do not pay a
fixed extra model call.

## Data And Protocol Impact

- No SQLite schema changes.
- No protocol payload changes for strategy; browser approval reuses existing
  `approval_request` / `approval_response` frames as described in
  [orchestration-deep-collaboration-and-approval.md](orchestration-deep-collaboration-and-approval.md).
- No frontend event shape changes.
- Existing `turn.start`, `turn.delta`, `command.start`, `command.end`,
  `turn.end`, and `run.end` events remain the only emitted successful-path
  event kinds. The verifier is represented as another normal turn with a
  verifier turn id.

## Implementation Steps

1. Add a compact handoff formatter in `internal/bridge/orchestration.go`.
2. Update `composeOrchestrationPrompt` to use strategy-specific instructions
   and compact prior-turn handoffs.
3. Add early completion detection for explicit resolved handoffs.
4. Add structured handoff parsing and a conditional final verifier prompt.
5. Add tests for compact handoff prompts, collaboration guidance, debate
   guidance, and completion detection.
6. Run full Go tests, frontend build, Go build, and doc lint.

## Exit Gates

- Collaboration prompts mention builder/reviewer duties and the compact `Msg:`
  plus `Handoff:` contracts.
- Debate prompts mention proposer/critic duties, evidence, and synthesis.
- Large prior outputs are compacted before being sent to the next CLI.
- No new event kind is introduced, so frontend rendering stays on existing
  branches.
- `status=resolved` can stop after at least two turns only when a final answer
  is present.
- A final verifier turn runs only for file changes, failed commands/errors, or
  unresolved risks.
- Full test and build commands pass:
  `/usr/local/go/bin/go test ./...`, `cd frontend && npm run build`,
  `CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .`,
  and `make doc-lint`.

## Reviewer Q&A

**Q1: Why not add a real supervisor agent?**

A: A supervisor would add another CLI/model call per turn and increase token
cost. The current boundary can get most of the benefit by making handoffs
explicit and compact.

**Q2: Why keep round-robin instead of dynamic speaker selection?**

A: Dynamic selection needs another model decision or protocol field. Round-robin
is deterministic, easy to test, and matches the existing Bridge implementation.

**Q3: Why show handoffs to the user?**

A: The UI already renders `turn.delta` as visible timeline entries. Visible
handoffs make the collaboration auditable without adding hidden state that the
browser cannot replay.
