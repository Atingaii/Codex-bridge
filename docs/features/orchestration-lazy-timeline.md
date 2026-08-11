# Orchestration Lazy Timeline

## Goals

- Keep long orchestration runs readable without making the Hub or browser load the full event history at once.
- Automatically reveal older persisted turns as the user scrolls upward.
- Keep command output available while giving text responses visual priority.

## Non-goals

- Redis, a second database, or a change to orchestration event persistence.
- Dropping command events or changing the orchestration wire protocol.

## Data and Protocol Impact

None. The frontend continues to use `GET /api/orchestrations/{runID}/events` with the existing `beforeSeq` cursor and bounded page size.

## Implementation

Live WebSocket events are coalesced once per browser animation frame before
updating the timeline and run summary. Polling remains a quiet gap-recovery
path and does not reactivate the already selected run, replace its event list,
or reconnect its WebSocket. Automatic follow mode scrolls only the timeline
container so streaming output does not move the surrounding workspace.

1. Trigger the existing older-event request when the timeline is within 240px of its top.
2. Guard requests with a ref so scroll events cannot create concurrent page loads.
3. Preserve the scroll anchor after prepending a page, preventing an automatic request chain and reducing render pressure.
4. Keep command batches collapsed by default; only active or failed commands open automatically.
5. Treat a run id in the URL as an explicit selection so endpoint synchronization cannot replace it with a remembered run from another machine.

## Exit Gates

- Scrolling to the top loads at most one 300-event page at a time.
- The viewport remains visually anchored after a page is inserted.
- A completed command's output is available after expanding its card.
- Refreshing a deep link keeps the URL-selected run and its endpoint selected.
- `npm run build` refreshes the embedded static UI and Go tests remain green.

## Reviewer Q&A

**Why no Redis?** SQLite remains the durable source of truth and the existing bounded cursor already limits Hub work. Redis would add an operational dependency without fixing the presentation problem.

**Can users still load history manually?** Yes. The existing button remains available for keyboard and accessibility use.
