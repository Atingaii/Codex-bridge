# Orchestration Plan And Progress Workspace

## Goals

- Keep the live orchestration transcript as the primary browser surface while
  using the unused horizontal space for prompt navigation and durable progress.
- Add an administrator-only rollout that prepends a planning node and an
  independent plan-review node to the existing candidate, integration, and
  final-review task graph.
- Show the initial prompt and every persisted turn prompt without replacing or
  duplicating the orchestration conversation.
- Derive a visible checklist from machine-readable planner markers and apply
  later task updates only when their item id and status are valid.
- Preserve existing runs, ordinary users, non-durable endpoints, and older
  Bridge binaries without changing their successful execution path.

## Non-Goals

- No user-authored DAG editor or arbitrary model-authored topology.
- No second scheduler, Redis dependency, or checklist persistence table.
- No replacement for the independent final reviewer or its formal-proof
  evidence requirements.
- No requirement for an old Bridge to understand new browser-only progress
  response fields.
- No layout change for users outside the rollout.

## User Experience

For users with the `orchestration-plan-workspace` feature, the transcript
header gains a Progress action that opens a default-closed overlay. The overlay
never changes the three-column transcript layout or reduces its reading width.
It contains two responsive information rails:

1. The prompt rail switches between the original request and persisted
   `turn.start` prompts. The selected prompt is read-only.
2. The progress rail shows the current graph generation, six ordered nodes,
   and the planner checklist. Active and completed states use green, waiting
   and review states use amber, and failed or blocked states use red.

Below desktop width the overlay stacks prompt navigation above progress with
bounded, independently scrollable rows. Closing it restores the untouched live
conversation. Runs created outside the rollout keep the existing four-node
graph and existing layout.

## Data And Protocol Impact

- Register `orchestration-plan-workspace` in
  `internal/rollout/rollout.go:RegisteredFeatures`; the default policy is
  `admin`.
- Add the optional `planWorkspace` field to
  `internal/protocol.OrchestrationStartPayload`. JSON omission preserves old
  Hub and Bridge compatibility.
- The durable task graph payload persists the flag. New enabled runs use:

  ```text
  plan -> plan-review -> candidate-a -> candidate-b -> integrate -> review
  ```

  Existing and non-enabled runs keep:

  ```text
  candidate-a -> candidate-b -> integrate -> review
  ```

- `GET /api/orchestrations/{runID}/progress` returns the latest owned graph and
  a reconstructed checklist. It returns 404 outside the rollout so the backend,
  rather than only the browser, enforces the boundary.
- Planner output uses `[PLAN_ITEM id="P1" status="pending"] title`; later nodes
  may emit `[PLAN_UPDATE id="P1" status="completed"] evidence`. The Hub accepts
  only known item ids and `pending`, `in_progress`, `completed`, or `blocked`.
  Invalid markers remain ordinary transcript text and never mutate progress.
- Checklist state is reconstructed from persisted orchestration events. The
  durable task graph remains the scheduling source of truth and no schema is
  added for presentation state.

## State And Compatibility

- Node states are the existing durable graph states: `pending`, `ready`,
  `dispatching`, `running`, `succeeded`, `failed`, `blocked`, `canceled`, and
  `unknown`.
- Planning and plan review are serialized because all nodes share the selected
  workspace. Plan review may correct the checklist before implementation starts.
- A missing or malformed structured plan does not fail the run. The UI falls
  back to the six durable nodes as a high-level checklist.
- Older Bridge binaries decode the optional start field harmlessly and execute
  the fixed task instructions supplied by Hub. Ordinary users never receive the
  expanded topology.
- Follow-ups keep the same run id. The latest graph payload determines whether
  the expanded topology continues, so rollout changes do not silently alter an
  existing run.

## Implementation Steps

1. Register and test the administrator rollout.
2. Extend task roles and build the conditional six-node topology.
3. Add the owned, rollout-gated progress endpoint and marker reducer.
4. Add prompt, node-map, and checklist rails to the existing workspace.
5. Rebuild embedded assets and run focused and full regression suites.

## Exit Gates

- [x] Administrator `/api/me` includes the feature; a normal user does not.
- [x] Enabled new runs create six ordered nodes; existing behavior creates four.
- [x] Follow-ups preserve the original run id and topology mode.
- [x] Cross-user and non-rollout progress requests return 404.
- [x] Malformed or unknown checklist updates cannot change a plan item.
- [x] Prompt selection includes initial and persisted per-turn prompts.
- [x] Navigation, active-run deletion, cancellation convergence, and live event
      rendering retain regression coverage.
- [x] Frontend tests/build, Go tests/build, document lint, and diff checks pass.

## Reviewer Q&A

**Why two planning nodes?**

The first decomposes the initial requirement; the second independently checks
coverage, ordering, and proof obligations before implementation consumes the
plan. Neither can declare the overall run complete.

**Why not store checklist rows in SQLite?**

The model markers and task events are already durable. Reconstructing a small,
bounded checklist avoids a second state machine and guarantees that browser
progress cannot disagree with scheduler truth.

**Can this affect a user with an old Bridge?**

No existing flow is replaced. The new start field is optional, task identity is
already supported by durable Bridges, and malformed structured output degrades
to node-level progress rather than failing execution.

**Does the planning phase change conversation continuity?**

No. Creation still produces one run id and follow-ups still use
`POST /api/orchestrations/{runID}/prompts`.
