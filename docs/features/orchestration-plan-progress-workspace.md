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
- Separate orchestration execution progress from proof-domain progress: the
  former explains which Agent stage is running, while the latter explains the
  overall goal, proof branches, dependencies, difficulty, priority, current
  focus, and evidence.
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

For users with the `orchestration-plan-workspace` feature, a compact,
default-closed progress row appears directly below the runtime status toolbar.
Its collapsed state shows whole-task completion and current focus. Expanding
the inline section keeps the three-column transcript in normal document flow
and contains two responsive information rails:

1. The prompt rail switches between the original request and persisted
   `turn.start` prompts. The selected prompt is read-only.
2. The progress rail starts with an overall Chinese goal and completion
   summary. Its primary map is a proof-task dependency graph whose nodes show
   difficulty, priority, status, and recommended order. A synchronized local
   checklist lists active, ready, dependency-blocked, and completed proof
   obligations with rationale and evidence.
3. The six durable Agent nodes remain visible as a compact secondary execution
   chain. They explain where the orchestration runtime is, but do not masquerade
   as proof completion.

Below desktop width the section stacks prompt navigation above progress with
bounded, independently scrollable rows. Collapsing it leaves only the compact
summary row. Runs created outside the rollout keep the existing four-node graph
and existing layout.

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
- Planner output may include `[PLAN_GOAL] Chinese goal summary` and uses the
  enriched fixed-order marker
  `[PLAN_ITEM id="P1" status="pending" kind="proof" difficulty="hard"
  priority="1" depends="P0"] Chinese title | Chinese rationale`. `depends=""`
  represents a root item. The Hub also accepts the original short
  `[PLAN_ITEM id="P1" status="pending"] title` form for existing runs.
- Later nodes may emit `[PLAN_UPDATE id="P1" status="completed"] evidence`.
  The Hub accepts only known item ids, bounded metadata values, and `pending`,
  `in_progress`, `completed`, or `blocked`. Invalid markers remain ordinary
  transcript text and never mutate progress.
- Enriched markers may place attributes in any order. Optional `branch` is
  display text; `kind` stays one of `proof`, `implementation`, `verification`,
  or `research`; `difficulty` stays one of `easy`, `medium`, `hard`, or
  `critical`; priority is `1..99`; and update progress is clamped by accepting
  only `0..100`. Unknown, duplicate, and self dependencies are removed after
  the reviewed plan establishes the canonical id set.
- The response includes Chinese labels for goal, branch, difficulty, priority,
  dependencies, and local progress. Protocol statuses and enum values remain
  stable English values so old browsers and Bridges do not depend on locale.
- `GET /api/orchestrations/{runID}/progress` projects summary counts, completion
  percentage, current focus, and dependency readiness from that same event
  ledger. The browser does not maintain a second copy of plan state.
- Checklist state is reconstructed from persisted orchestration events. The
  durable task graph remains the scheduling source of truth and no schema is
  added for presentation state.

## State And Compatibility

- Node states are the existing durable graph states: `pending`, `ready`,
  `dispatching`, `running`, `succeeded`, `failed`, `blocked`, `canceled`, and
  `unknown`.
- Planning and plan review are serialized because all nodes share the selected
  workspace. Plan review may correct the checklist before implementation starts.
- A missing or malformed structured plan does not fail the run. The plan map
  and checklist show an explicit loading or unavailable state; durable Agent
  nodes remain visible only in the separate runtime diagnostics view.
- Existing short plan markers render with neutral difficulty, source order as
  priority, and no dependencies. Existing runs and old Bridges therefore remain
  readable without migration.
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
4. Project overall/local proof progress and keep the durable Agent graph as a
   separate execution-stage view.
5. Add prompt, proof-map, checklist, and execution-stage rails to the existing
   workspace.
6. Rebuild embedded assets and run focused and full regression suites.

## Exit Gates

- [x] Administrator `/api/me` includes the feature; a normal user does not.
- [x] Enabled new runs create six ordered nodes; existing behavior creates four.
- [x] Follow-ups preserve the original run id and topology mode.
- [x] Cross-user and non-rollout progress requests return 404.
- [x] Malformed or unknown checklist updates cannot change a plan item.
- [x] Prompt selection includes initial and persisted per-turn prompts.
- [x] Enriched and legacy plan markers produce one canonical proof plan.
- [x] Overall counts, readiness, current focus, dependencies, and completion
      percentage are derived from the canonical plan.
- [x] The proof map and local checklist share the same projected plan state;
      the Agent graph is visibly secondary.
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
