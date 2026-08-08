> **DEPRECATED - superseded by the durable bounded task graph design**
>
> Current design: [Durable Bounded Orchestration Task Graph](durable-bounded-orchestration-task-graph.md). Historical only; do not implement from this doc.

# Persistent Task Queue

## Goal

Allow users to submit additional work while the current chat prompt or
orchestration run is still active. Hub persists those follow-up tasks in a
per-context serial queue and automatically dispatches the next queued task when
the current task reaches a successful terminal state.

The feature is about continuity and convenience, not parallel execution:

- Chat tasks queue behind the selected chat session.
- Orchestration follow-up tasks queue behind the selected orchestration run.
- Bridge continues to receive one task at a time through the existing prompt or
  orchestration-start paths.
- Hub remains the source of truth for queue state, persistence, and recovery.

## Non-Goals

- Do not run multiple tasks concurrently in the same chat session or
  orchestration run.
- Do not add Redis, Postgres, or an external broker. The queue is SQLite-backed
  in the existing Hub database.
- Do not make Bridge a scheduler. Bridge remains the private execution client
  and only receives already-dispatched work.
- Do not create a global cross-user or cross-agent fairness scheduler.
- Do not silently create a new orchestration run for queued follow-ups.
- Do not merge, rewrite, or summarize queued prompts before dispatch.
- Do not add task reordering, editing pending prompts, or priority scheduling in
  the first implementation.
- Do not expose pending private queue items in public conversation shares.

## Current State

Chat submission is rejected while the selected session has an active run.
`internal/hub/ws_browser.go:handleBrowserEnvelope` checks
`internal/store/store.go:ActiveRunBySession` and returns `RUN_ACTIVE` if a run
is still pending.

Orchestration follow-up submission is rejected while the selected run is active.
`internal/hub/orchestration.go:handleContinueOrchestration` currently returns
`RUN_ACTIVE` unless the selected run has reached a terminal status.

Chat continuity already depends on the selected `sessions.id` plus the
persisted `remote_thread_id`; see
`docs/features/orchestration-continuity.md` for the same continuity invariant on
orchestration runs. Queue dispatch must preserve those existing invariants.

Bridge already has the right execution boundary: chat receives existing
`protocol.TypePrompt` frames, and orchestration receives existing
`protocol.TypeOrchestrationStart` frames. The queue should feed those paths
rather than adding a new Bridge execution protocol.

## Design

Add a Hub-owned persistent queue with one serial lane per execution context:

```text
chat lane          = chat:<sessionID>
orchestration lane = orchestration:<runID>
```

Only one task in a lane may be `dispatching` or `running`. Later tasks remain
`queued` until the active task completes successfully.

### Queue Tables

Add SQLite tables owned by `internal/store/store.go:Migrate`.

```text
task_queue_items
  id
  user_id
  agent_id
  lane_type          chat | orchestration
  lane_id            session id or orchestration run id
  status             queued | dispatching | running | completed | failed | canceled
                     | blocked
  sequence           monotonically increasing per lane
  client_prompt_id   optional idempotency key from the browser
  prompt
  attachments_json   chat image attachments or orchestration file payloads
  options_json       orchestration mode/cwd/maxTurns/profile/etc.
  dispatched_run_id  chat run id, or orchestration run id for follow-ups
  error
  created_at
  started_at
  finished_at
  updated_at
```

```text
task_queue_lanes
  user_id
  lane_type
  lane_id
  paused             boolean
  pause_reason
  updated_at
```

The lane table is intentionally small. It persists pause state after a failed or
canceled active task so Hub does not accidentally continue a dependent queue
after restart.

Queue item payloads should reuse existing prompt-size and attachment-size
validation. For the first implementation, queued attachments can be stored in
SQLite JSON using the same base64 shape already accepted by Hub. If attachment
volume becomes a problem later, a separate queued-blob table or filesystem
staging directory can replace the JSON field without changing the user-facing
queue model.

### Chat Flow

When the browser sends `protocol.TypePrompt` for a chat session:

1. Hub validates prompt size and attachments exactly as it does today.
2. If the session has no active run and the lane is not paused, Hub dispatches
   immediately using the existing path.
3. If the session has an active run, Hub inserts a `queued` item instead of
   returning `RUN_ACTIVE`.
4. Hub broadcasts a queue update to all browsers attached to that session.

Queued chat prompts are not inserted into the `messages` table at enqueue time.
They are visible through the queue UI, but they become real chat history only
when dispatched. This preserves transcript order:

```text
user A
assistant answer to A
user B
assistant answer to B
```

Dispatching a queued chat item performs the same work as the current immediate
path:

1. Create a run with `internal/store/store.go:CreateRun`.
2. Insert the user message with `internal/store/store.go:AddMessage`.
3. Send the existing `protocol.TypePrompt` frame to the selected Bridge agent.
4. Mark the queue item `running` and store the created run id.

When `internal/hub/ws_bridge.go:handlePromptComplete` or
`internal/hub/ws_bridge.go:handleBridgeError` marks the active run terminal, Hub
calls the queue dispatcher for `chat:<sid>`.

### Orchestration Flow

When the browser posts to
`internal/hub/orchestration.go:handleContinueOrchestration` while the selected
run is active:

1. Hub validates ownership, same-agent continuity, prompt size, and attachments.
2. Hub stores the normalized follow-up request as a queued item.
3. The HTTP response is `202 Accepted` with the queued item metadata.
4. The selected run remains active and unchanged until the current run reaches a
   terminal state.

Queued orchestration follow-ups are dispatched through the existing continue
path, not through new-run creation. Context compaction must happen at dispatch
time, not enqueue time, so the just-finished run output is included in the next
prompt's compacted context.

For `profile=formal-proof`, this is a product invariant: queued follow-ups keep
the same `runID`, `RunCWD`, and proof-run directory. Bridge then appends the
follow-up to `proof-notes.md` through the existing formal-proof resume path.

When Hub persists an orchestration terminal event in
`internal/hub/orchestration.go:handleOrchestrationEvent`, it calls the queue
dispatcher for `orchestration:<runID>` if the terminal status was successful.

### Dispatch Rules

The dispatcher is a Hub helper, not a long-running external worker. It runs
after terminal chat or orchestration events, after Bridge reconnects, and during
Hub startup recovery.

For one lane:

1. Return immediately if the lane is paused.
2. Return immediately if the lane already has an active run/task.
3. Claim the earliest `queued` item in a SQLite transaction by moving it to
   `dispatching`.
4. Dispatch it through the existing chat or orchestration path.
5. On successful send to Bridge, mark it `running`.
6. On send failure because the agent is offline, move it back to `queued` and
   leave the lane unpaused.
7. On validation or continuity failure that cannot be retried, mark it
   `failed`, pause the lane, and surface the error to the browser.

Terminal policy:

| Active task outcome | Queue behavior |
| --- | --- |
| completed | Automatically dispatch next queued task. |
| failed | Pause lane; leave later tasks queued. |
| canceled by user | Pause lane; leave later tasks queued. |
| agent offline before dispatch | Keep queued; retry when the agent reconnects. |
| approval pending | Active task is still running; do not dispatch later tasks. |

The first implementation should use pause-on-error. A later policy toggle can
offer "continue after failure", but defaulting to pause is safer because queued
tasks usually depend on the previous context.

### Browser UI

In chat and orchestration views, the composer stays enabled while the current
task is running.

Submit behavior:

| Context state | Button label | Behavior |
| --- | --- | --- |
| Idle | Send | Dispatch immediately. |
| Running | Add to queue | Persist as queued item. |
| Paused queue | Add to queue | Persist item, but do not auto-dispatch until resumed. |

MVP queue controls:

- Show pending count near the composer.
- Show a compact queue list with prompt preview, created time, and status.
- Allow canceling queued items.
- Show paused state and a "resume queue" action.
- Keep reorder/edit/deprioritize actions out of the first implementation.

Queue updates can be delivered with a new browser-visible frame such as
`task_queue_update` or through an authenticated polling endpoint. If a new
frame is added, `internal/protocol/envelope.go` must define it and the frontend
must handle it in both chat and orchestration pages. The Bridge does not need a
new frame because queued work is eventually sent as existing execution frames.

### API Shape

The exact endpoint naming can be adjusted during implementation, but the
feature needs these operations:

```text
GET    /api/task-queue?laneType=chat&laneId=<sid>
GET    /api/task-queue?laneType=orchestration&laneId=<runID>
DELETE /api/task-queue/items/<taskID>
POST   /api/task-queue/lanes/resume
POST   /api/task-queue/lanes/pause
```

Existing submission paths should remain the primary way to enqueue work:

- Chat: `protocol.TypePrompt` over `/ws/chat?sid=<session>`.
- Orchestration: `POST /api/orchestrations/{runID}/prompts`.

This keeps the product model simple: users submit from the same composer, and
Hub decides whether the task starts now or enters the queue.

### Public Shares

Pending queue items are private operational state and should not appear in
public share payloads. Once a queued item dispatches, it becomes normal chat
messages or orchestration events and is included in shares through the existing
sanitized transcript flow.

## Data And Protocol Impact

- Add SQLite queue tables and store CRUD methods.
- Add Hub queue HTTP endpoints for listing, canceling, pausing, and resuming.
- Chat prompt submission changes from `RUN_ACTIVE` to enqueue-on-active.
- Orchestration continue changes from `RUN_ACTIVE` to enqueue-on-active for
  valid follow-up payloads.
- Add browser-visible queue updates, either via a new WebSocket frame or a
  lightweight polling path.
- No new Bridge-to-CLI execution protocol is required.
- No change to public share schema for pending queue items.

## Implementation Steps

1. Add `task_queue_items` and `task_queue_lanes` migrations plus store methods
   for enqueue, list, cancel, pause/resume, claim-next, mark-running, and
   terminal updates.
2. Add Hub queue helpers that dispatch one lane at a time through existing chat
   and orchestration paths.
3. Integrate chat enqueueing into
   `internal/hub/ws_browser.go:handleBrowserEnvelope`.
4. Trigger chat queue dispatch from
   `internal/hub/ws_bridge.go:handlePromptComplete` and
   `internal/hub/ws_bridge.go:handleBridgeError`.
5. Integrate orchestration enqueueing into
   `internal/hub/orchestration.go:handleContinueOrchestration`.
6. Trigger orchestration queue dispatch from terminal orchestration event
   handling in `internal/hub/orchestration.go:handleOrchestrationEvent`.
7. Add queue list/cancel/pause/resume endpoints in `internal/hub/server.go`.
8. Add browser queue update handling in
   `frontend/src/app/pages/Workspace.tsx` and
   `frontend/src/app/pages/OrchestrationWorkspace.tsx`.
9. Rebuild `internal/web/static/` after frontend changes.
10. Add store, Hub, integration, and frontend rendering tests.

## Exit Gates

- While chat task A is running, submitting chat task B creates a queued item
  instead of returning `RUN_ACTIVE`.
- After chat task A completes successfully, Hub automatically dispatches task B
  in the same session and preserves chat `remote_thread_id` continuity.
- While orchestration run A is active, submitting follow-up B creates a queued
  item instead of returning `RUN_ACTIVE`.
- After orchestration run A completes successfully, Hub automatically dispatches
  B through the same run id and the same selected Bridge agent.
- Formal-proof queued follow-ups reuse the existing proof-run cwd and notes.
- Canceling a pending queue item removes only that pending item and does not
  interrupt the active task.
- Canceling or failing the active task pauses the lane and leaves later items
  queued.
- Hub restart preserves queued items and paused lanes.
- Bridge offline leaves items queued and dispatches them after reconnect when
  the lane is not paused.
- Public shares do not expose pending queue items.
- Verification:
  - `/usr/local/go/bin/go test ./...`
  - `cd frontend && npm test`
  - `cd frontend && npm run build`
  - `CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .`
  - `make doc-lint`

## Reviewer Q&A

**Q: Why put the queue in Hub instead of the browser?**

A: Browser-local queues disappear on refresh, cannot recover after reconnect,
and cannot safely continue after the active task completes while the page is
closed. Hub already owns SQLite state and agent routing, so it is the right
place to persist and dispatch queued tasks.

**Q: Why not let Bridge accept a batch of tasks?**

A: Bridge should stay a private execution endpoint. Sending one task at a time
keeps approval routing, cancel behavior, native CLI continuity, and failure
handling aligned with existing chat and orchestration code.

**Q: Why not auto-continue after failure?**

A: Queued tasks usually depend on the previous task's result. If the current
task fails or is canceled, automatic continuation can compound a bad state.
Pause-on-error makes the user explicitly decide whether later prompts are still
valid.

**Q: Why not show queued prompts as chat messages immediately?**

A: That would put future user prompts before the assistant answer to the current
prompt in the transcript. Queue UI can show pending work without corrupting
conversation order. The queued prompt becomes a normal message when it is
actually dispatched.

**Q: Does this change orchestration continuity?**

A: It should reinforce it. Queued orchestration prompts must use
`POST /api/orchestrations/{runID}/prompts` semantics at dispatch time, not
`POST /api/orchestrations`, so the same run id, native resume metadata, locked
cwd, and formal-proof workspace are reused.
