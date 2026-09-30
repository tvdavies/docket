# Plan 002: Make inbox consumption safe for Sal

> Follow this plan in an isolated implementation worktree. It is independent of the web-removal plan and must not modify installed workspaces or run notification hooks. Update the plan index when completed.
>
> Drift check: `git diff --stat 0af2dda0aad26e8578ded011797761fe16f73f62..HEAD -- internal/events internal/cli/coordination.go internal/store docs/cli.md docs/configuration.md docs/waits-and-references.md internal/cli/skill.md`. Recheck cited behaviour if these paths changed.

## Status

- Priority: P1; effort: M; change risk: medium.
- Category: correctness and CLI contract.
- Depends on: none; required before using the inbox as Sal's durable intake.
- Planned at: `0af2dda`, 30 September 2026.
- **Implementation status: merged** in [PR #16](https://github.com/tvdavies/docket/pull/16).

## Background and proposed outcome

Sal may poll Docket during a heartbeat or receive immediate wake events through hooks. The current inbox is suitable for marking notifications read, but it advances its cursor before the CLI consumer has durably handled the result. It also contains a narrower race that can acknowledge an event never included in the result. Fix that race and add an explicit read/acknowledge contract for durable consumers, while retaining existing notification behaviour for compatible callers.

## Current state

`internal/events/inbox.go:23` reads a batch, filters it, then independently counts the log:

```go
cursor := Cursor(ws, opts.Actor)
evs, err := Since(ws, cursor)
// ...filter by assignee...
if opts.MarkRead {
    if err := AdvanceCursor(ws, opts.Actor, Count(ws)); err != nil {
        return nil, err
    }
}
```

If an event is appended after `Since` reaches EOF but before `Count`, the cursor advances over that unseen event. This is a high-confidence code-path finding; it was not reproduced with a timing stress test during the audit.

`internal/events/events.go:134` already exposes `ReadBatch` with the position actually read; `ReadBatchCheckpoint` adds the prefix hash. Physical log position is the cursor, not `Event.Seq`, which is advisory and may repeat. `internal/cli/coordination.go:36` invokes `Inbox` before printing, so `--mark-read` cannot provide acknowledgement after external durable processing. `AdvanceCursor` writes atomically but does not serialise a complete actor read/advance transaction.

Relevant tests are `TestInboxCursorDrains`, `TestInboxAllIgnoresAssignee`, and the physical cursor tests in `internal/events/events_test.go`. Handler tests already cover checkpoint validation, rewritten history, concurrency, and isolation from actor inbox cursors. Reuse the underlying event/store primitives without merging the two cursor namespaces.

## Scope and conventions

Modify `internal/events/inbox.go`, supporting cursor helpers and tests in `internal/events/`, `internal/cli/coordination.go` and focused CLI tests, plus the inbox/help documentation. Use existing `internal/store` locking/atomic-write helpers. Add small private helpers as required; avoid a new queue service or schema for tasks.

Do not change event bytes, event sequence assignment, handler cursor files, hook invocation, task ownership, unrelated CLI output, HTTP/UI code, or live actor cursors. The implementation belongs entirely to Docket; Sal's intake receipt store is a documented consumer responsibility, not part of this patch.

## Steps

### 1. Bound legacy acknowledgement to the snapshot read

Use the end position returned by the same `ReadBatch`/`ReadBatchCheckpoint` that supplied the returned events. Serialise actor cursor read/advance where needed so concurrent calls cannot regress the position. Never replace the returned end with a later `Count` or the maximum matching `Event.Seq`. Preserve the existing array output and assignee filter for current `inbox --json` callers.

Add a deterministic regression test that inserts an event between snapshot read and acknowledgement through a narrow internal test seam or factored helper. The next read must return the appended event. Add concurrent-reader coverage preventing cursor regression; do not rely on sleeps to expose the race.

**Verify:** `go test ./internal/events ./internal/cli` exits 0. Existing inbox tests still pass and the regression tests demonstrate acknowledgement cannot cross the returned snapshot boundary.

### 2. Add explicit acknowledgement for durable readers

Introduce an additive inbox read mode returning an envelope with events and an opaque checkpoint token, without changing the cursor. The token must bind the actor, workspace, read/filter mode, prior position, returned high-water position, and log-prefix identity. Use a versioned format and validate it; do not interpret event sequence numbers as positions.

Add an explicit acknowledgement operation that accepts this token. Under the actor lock, validate the workspace/actor binding, exact log prefix, and expected cursor state before publishing the new position. A repeated acknowledgement of an already applied token succeeds without moving backwards. A stale or incompatible acknowledgement must not advance or reset progress silently. Detect rewritten/truncated logs and require explicit recovery rather than acknowledging unrelated replacement history. Continue to support old numeric cursor files through a documented lazy migration with fixtures.

Choose and document the smallest additive syntax in `inbox --help`; for example, an explicit peek/envelope flag and an acknowledgement flag or subcommand. Keep existing `inbox --json` array output unchanged. Do not combine legacy `--mark-read` with explicit-token operations. Empty filtered batches can still contain a valid high-water token for inspected nonmatching records. Document use of a consistent actor/filter identity; changing filters must not pretend to recover previously acknowledged events.

**Verify:** `go test ./internal/events ./internal/cli ./internal/handlers` exits 0. Cover peek without acknowledgement, successful ack, duplicate ack, stale/out-of-order ack, wrong actor/workspace, concurrent append, malformed log records, truncated/replaced history, empty filtered batches, legacy cursor migration, and handler/inbox isolation. Tests use temporary workspaces and config paths.

### 3. Document the Sal intake boundary

Document the intended sequence: read a batch, durably record it in Sal with deduplication, then acknowledge Docket. If Sal crashes before the receipt, it must be able to reread; if it crashes after the receipt but before acknowledgement, deduplication absorbs the replay. Acknowledgement means accepted into durable intake, not completion of an agent job. Do not claim exactly-once execution.

For low-latency delivery, a configured Docket hook can use the same Sal intake boundary and return success only after the receipt is durable. Keep processing policy, model invocation, and notifications in Sal. Explain that `watch` is a live diagnostic stream and current `--mark-read` remains immediate read acknowledgement, not a transaction with the consumer.

**Verify:** `go test ./...` and `go vet ./...` exit 0. CLI help and docs agree with executable tests. `git diff --check` passes.

## Done criteria

- A new event appended between read and acknowledgement is available on the next read.
- Unacknowledged batches can be replayed; duplicate acknowledgements do not regress cursors.
- Invalid/stale/wrong-store tokens cannot silently advance a cursor.
- Existing notification readers retain their output contract and handler consumers retain their cursor namespace.
- Tests cover the race deterministically, plus durable-consumer crash boundaries.
- All final Go checks pass and the plan index records the actual implementation state.

## Risks and stop conditions

Actor filenames currently normalise some characters to underscores. Do not quietly change their naming scheme in this patch and split existing consumers; if the new binding contract needs a storage change, design and test migration explicitly. Stop and report if reliable validation would require destructive event-log migration, conflating handler/inbox checkpoints, or changing existing output without an additive mode. Refactor existing checkpoint helpers rather than inventing a second incompatible interpretation of physical log positions.


## Implementation notes

- **Race fix.** `Inbox --mark-read` now advances to the end of the same `ReadSnapshot` scan that produced the returned events, under a per-actor `store.WithLock` (`.cursors/<actor>.cursor.lock`). `afterInboxSnapshot` is the test seam; `TestInboxMarkReadDoesNotAcknowledgeEventAppendedAfterSnapshot` and `TestInboxMarkReadIsSerialisedPerActor` both fail against the old `Count()` behaviour (checked by mutation).
- **Syntax chosen:** `docket inbox --peek [--all] --json` returns `{actor, all, events, from, to, checkpoint}`; `docket inbox ack [--actor A] [--all] CHECKPOINT` applies it; `docket inbox --reset` is the explicit recovery after a history change. `--peek`, `--mark-read` and `--reset` are mutually exclusive. `inbox --json` still prints the bare array.
- **Token:** `dkinbox1.` + base64url JSON binding version, resolved workspace root, actor, filter mode, from/to line positions and prefix hashes at both boundaries. Positions are physical non-empty line counts (the existing `Cursor` unit), never `Event.Seq`.
- **Storage / lazy migration:** the `.cursor` file keeps its plain-integer format so older binaries and `events.Cursor` callers are unaffected. A sidecar `.cursors/<actor>.checkpoint` records `{position, prefix_hash}`; it is trusted only when its position equals the numeric cursor, so an older binary advancing the cursor simply makes it unverified until the next ack. Actor filename sanitisation is unchanged.
- **Ack rules:** under the actor lock, the `to` prefix must still match; a cursor already at or beyond `to` is a no-op success (`applied: false`); otherwise the cursor must equal `from` (and match `from_hash` when verified) or the ack fails with `ErrInboxCheckpointStale`. Truncation or rewrite beneath a verified cursor makes `--peek` fail with `ErrInboxHistoryChanged` until `--reset`.
- **Docs:** new `docs/inbox.md` (embedded as `docket docs inbox`) documents the read → durable intake → ack sequence, crash boundaries, at-least-once/no exactly-once, `watch` as diagnostic only, and hook-based low-latency intake.
- **Deviation:** none from the plan's intent. `--reset` was added because the plan requires explicit recovery after history changes and there was no existing command for it.
