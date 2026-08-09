# Embedded CLI Usage Ledger

## Goals

- Count every usage-bearing provider event produced by all native CLI sessions
  participating in an orchestration run, including retries and continuations.
- Normalize usage into non-cached input, cache read, cache write, output, and
  reasoning without double counting reasoning or cached input.
- Keep collection inside the existing Bridge process and persistence inside the
  existing Hub SQLite database. Installation and connection commands do not
  change.
- Backfill completed orchestration runs whenever their native CLI session logs
  are still present on the private endpoint.
- Expose whether statistics are complete, partial, unavailable, or still based
  on the legacy per-turn snapshots.

## Non-Goals

- Intercept provider network requests or operate an LLM proxy.
- Add a daemon, listening port, local database, or setup command on the private
  endpoint.
- Reconcile subscription plans, credits, enterprise discounts, or provider
  invoices.
- Guess usage from characters or assign a fuzzy model price when provider and
  model identity are unavailable.
- Upload prompts, responses, tool output, private paths, or other raw session
  log content to the Hub.

## Data And Protocol Impact

The Hub sends an `orchestration_usage_sync_request` envelope to the Bridge with
the run ID and the native session IDs already persisted for that run. The
Bridge locates matching standard CLI session logs, parses them read-only, and
returns an `orchestration_usage_sync_result` containing normalized usage events
and sanitized diagnostics. Each event has a deterministic ID scoped by agent,
CLI, and native session so replay and reconnect are idempotent.

The Hub stores normalized events in `orchestration_usage_events` and run/session
scan state in `orchestration_usage_syncs`. A successful rescan transactionally
replaces the previous events for that run and native session. A partial or
failed scan keeps already verified events while exposing the incomplete state.
The statistics API prefers this ledger and retains legacy `turn.usage` only as
an explicitly incomplete compatibility view.

Codex input usage includes cached input. The scanner therefore records:

```text
non-cached input = max(input_tokens - cached_input_tokens, 0)
total tokens = non-cached input + cache read + cache write + output
```

Reasoning is displayed separately because it is part of output. Cumulative
token snapshots are converted to non-negative deltas; response-level events
are used directly when present. Unknown records are ignored rather than
invented. No raw log text or source path crosses the reverse WebSocket.

Cost remains an API-list-price equivalent. It is calculated only from a strict
provider and model catalog match (or an explicit provider-reported cost), and
otherwise remains unavailable while token totals remain valid.

## Runtime Flow

1. A run persists native session IDs through the existing run-end metadata.
2. On terminal run state, the Hub requests a scan from its owning online agent.
3. The Bridge scans only the named sessions and sends normalized events.
4. The Hub validates ownership and atomically updates the usage ledger.
5. The statistics endpoint returns ledger totals and sync completeness.
6. Opening legacy statistics or explicitly retrying sync requests a backfill
   without blocking the HTTP response.

Scanner errors never change orchestration status. Disconnects and duplicate
results are safe because event IDs and run/session replacements are
idempotent.

## Implementation Steps

1. Add usage sync envelopes and ownership validation.
2. Add a bounded, read-only Codex session-log scanner with fixture tests.
3. Add Hub ledger migration, replacement, aggregation, and sync-state CRUD.
4. Trigger terminal and on-demand historical synchronization.
5. Return ledger provenance and completeness from the statistics API.
6. Update the statistics UI and rebuild embedded assets.

## Exit Gates

- All usage-bearing calls in a Codex fixture are counted exactly once.
- Cached input and reasoning are not double counted.
- Replaying a sync result does not change totals.
- A run cannot accept usage from an agent that does not own it.
- Missing or malformed private logs produce partial/unavailable state without
  failing the run or exposing private content.
- Completed runs can be backfilled after upgrading the owning Bridge.
- No extra user service, port, database, environment variable, or command is
  required.
- Focused tests, full Go tests, frontend tests/build, document lint, and static
  binary build pass.

## Reviewer Q&A

**Why not keep summing `turn.usage`?** Native turn completion can contain only
the latest cumulative snapshot. Provider calls made during tool loops are then
lost. The native session log is the durable per-call source available after a
turn or run ends.

**Does this upload private conversations?** No. The Bridge sends only native
session identity, timestamps, model/provider labels, normalized counters,
stable hashes, and sanitized scan status.

**Does backfill rewrite orchestration history?** No. It builds a separate
derived ledger linked to the existing run and native sessions. Timeline events
remain unchanged.

**Is the displayed price an invoice?** No. Provider-reported cost is preserved
when available; otherwise the UI labels strict catalog calculations as API
list-price equivalents.
