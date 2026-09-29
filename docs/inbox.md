# Inbox consumers

`docket inbox` reads events after a per-actor cursor. The cursor is a count of
physical event-log records, not an event `seq`, which is advisory and may
repeat. Cursors live in `.docket/.cursors/` and are separate from handler
cursors in `.docket/.cursors/handlers/`, so an actor and a handler with the
same name never acknowledge each other's progress.

`--all` returns every event; without it, only events whose denormalised
`assignee` is the actor.

## Notification readers

```sh
docket inbox --actor researcher --mark-read --json
```

`--mark-read` prints a JSON array and advances the cursor to the end of the
records it read. An event appended while the command runs stays unread. The
cursor moves before the caller processes the output, so a crash after the
command returns loses that batch. Use this for notifications, not intake.

## Durable consumers

A durable consumer reads, records, and then acknowledges:

```sh
docket inbox --actor sal --all --peek --json
# {"actor":"sal","all":true,"events":[...],"from":12,"to":15,"checkpoint":"dkinbox1...."}

# ...durably store the events, deduplicating on repeat...

docket inbox ack --actor sal --all dkinbox1....
```

- `--peek` never moves the cursor. Until acknowledged, every peek returns the
  same events plus any appended since.
- The checkpoint binds the workspace, actor, filter mode, the starting cursor,
  and the exact log prefix read. It is opaque; do not parse it.
- `ack` applies the checkpoint only if the cursor is still where the read
  began and the log prefix is unchanged. Acknowledging a checkpoint the
  cursor already covers succeeds (`"applied": false`) and never moves the
  cursor backwards.
- A stale checkpoint, or one from another actor, workspace, or filter mode,
  is rejected and the cursor is left unchanged. Read again.
- An empty `events` array still carries a checkpoint. Acknowledge it so
  records that did not match the filter are not rescanned.
- Use one actor name and one filter mode per consumer. Changing `--all`
  between reads does not recover events that were already acknowledged.

Acknowledgement means the batch has been accepted into the consumer's durable
intake, not that the resulting work is complete. The contract is
at-least-once:

| Crash point | Result |
|---|---|
| Before the consumer stores the batch | The next peek returns it again |
| After storing, before `ack` | The next peek returns it again; deduplication absorbs the repeat |
| After `ack` | The next peek starts after the batch |

There is no exactly-once execution. Deduplicate on the event content (for
example `time`, `type`, `task`, and `seq` together), because `seq` alone is not
unique.

## History changes

If the event log is truncated or rewritten under an acknowledged cursor (for
example by restoring `.docket/` from an older copy), `--peek` and `ack` fail
with "event log history changed". Nothing is acknowledged against the
replacement history. Recover explicitly:

```sh
docket inbox --actor sal --reset
```

The next peek replays the whole current log, and the consumer's deduplication
decides what is new.

Cursors written by older Docket versions are plain numbers. They are accepted
as they are, and the first acknowledgement records the log prefix they refer to
in a `.checkpoint` file beside the cursor. The `.cursor` file keeps its numeric
format, so older binaries can still read it.

## Low-latency delivery

A configured handler with `delivery: service` receives matching events without
polling. For the same durable intake, the hook stores its receipt and exits
successfully only after that write is durable. Docket retries failed batches.
The consumer decides what to do with each receipt: processing policy, model
invocation, and notifications belong to the consumer, not Docket.

`docket watch` is a live diagnostic stream. It has no cursor and is not a
substitute for durable intake.
