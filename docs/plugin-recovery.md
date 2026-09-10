# Plugin handler recovery

Restoring legacy config after plugin handlers advance replays their acknowledged
suffix. Retained cursor files are evidence, not a rollback recipe. Ordinary
`plugin disable` removes a declaration without restoring handlers or transferring
progress. Neither a service restart nor restoring an old binary fixes this.

The opt-in handoff transfers each handler's **acknowledged prefix**, keeping its
pending suffix deliverable. It does not provide exactly-once external effects.
This document describes source/fixture capability, not permission to deploy or
run a live handoff. JOB-0098 and its approved plan v1 cover the implementation;
JOB-0056 owns the separate Dispatch operational window and migration note.

## Commands

After independent review and authorization of the specific operation:

```sh
# Legacy -> plugin. An explicit hash and receipt path are recommended.
docket plugin enable NAME --workspace PROJECT --adopt-cursors \
  --expect-config-sha256 CURRENT_CONFIG_SHA256 \
  --receipt-dir NEW_ATTEMPT_DIRECTORY --json

# Plugin -> legacy. These three inputs are required for a fresh attempt.
docket plugin disable NAME --workspace PROJECT --adopt-cursors \
  --legacy-config REVIEWED_LEGACY_TEMPLATE \
  --expect-config-sha256 CURRENT_CONFIG_SHA256 \
  --receipt-dir NEW_ATTEMPT_DIRECTORY --json

# Inspection only, including when the original attempt was forward.
docket plugin disable NAME --workspace PROJECT --adopt-cursors \
  --receipt-dir EXISTING_ATTEMPT_DIRECTORY --json
```

The hash covers the exact current `.docket/config.yaml` bytes, not parsed YAML.
An explicit receipt parent must already exist, outside handler state. The attempt directory must be
new; an existing directory is **always read-only inspection**, even after a
partial attempt, source/destination advancement or a later opposite-direction
handoff. A missing/corrupt receipt requires inspection, not reusing that path.
Forward adoption without a receipt path allocates
`.docket/.cursors/handlers/handoffs/ATTEMPT_ID`. The CLI reports the path on stderr
before preparing evidence or writing cursors; stdout remains one JSON summary.
SIGINT/SIGTERM cancel the handoff's lock waits/pre-publication work, not an
in-flight handler. Lock waits also have a 30-second bound.

`--set` remains forward-only. `--from-start` is explicitly replaying enablement,
not recovery, and cannot be combined with adoption. Handoff rejects a nonempty
`DOCKET_HANDLER_STACK`; never run it from a handler holding a delivery lock.

## Preconditions and configuration scope

- The installed manifest defines the one-to-one mapping `NAME/h <-> h`. Every
  source must be active and every destination inactive. Missing legacy
  declarations, mixed wiring and an already enabled forward target are errors.
- Each source must have checkpointed JSON with an explicit nonnegative integer
  `position` and `prefix_hash`. Zero uses an empty hash. Positive positions
  require a lowercase SHA-256 matching that many non-empty physical ledger
  lines. Plain-integer, missing, malformed, duplicate-field, shortened-log and
  stale/hash-mismatched checkpoints are rejected, never converted to zero.
  Advisory `event.seq` is not the cursor position.
- An inactive destination may be missing or have a valid older/equal checkpoint.
  Ahead, corrupt or unreadable destinations are rejected. Sources and previous
  backup bytes are never modified. A new reverse transition updates *working*
  legacy cursors; immutable pre-cutover backups stay untouched.
- The reviewed template supplies only mapped legacy handler declarations. Extra
  handlers must already exist unchanged in current config; none are imported.
  Template labels, settings, plugin values and status lists are not restored.
- Normalized event filters, match predicates, delivery class and runtime must
  match the manifest. Executable/Lua paths must be relative and remain inside
  their project/package root, outside workspace state. Missing scripts, symlink
  paths and multiply hard-linked state/scripts are rejected in this strict mode.
- Reverse pins only the removed plugin's contributed statuses/terminal flags at
  their current effective positions. Forward removes matching contributions
  only if composition preserves ordering. Both directions validate the target
  and require unrelated effective configuration to remain unchanged. A layout
  that cannot be preserved by the bounded patch is rejected.

Matching filters, scripts and hashes do **not** establish behavioral equivalence
across different plugin environments. Review must account for `DOCKET_PLUGIN`,
plugin config, paths, credentials and external dependencies. Missing packages
cannot use this strict mode. Ordinary disable has weaker opt-out semantics.

## Locks and publication

The shared forward/reverse operation takes the sorted, deduplicated union of
source and destination handler locks, then the declared-config lock, then a
shared registry lock. It rereads the manifest/config under those locks and
rejects a changed identity set instead of acquiring new locks out of order.
Normal enablement follows the same handler-before-config-before-registry order
for reset/seeding; service seeding checks existence and writes under its handler
lock. An explicit zero or prepared checkpoint is therefore preserved.

While both sets are locked, an already-running invocation has finished (or
failed) and cannot move its checkpoint. The operation captures independent
`p_h/hash_h` values; handlers do not need to agree or drain to a common log end.
Event appends and unrelated task mutations may continue. They remain pending
suffix work. No event-log lock, ledger rewrite, registry update, handler
execution or supervisor/session cancellation is part of handoff.

The command writes private prepared evidence before destination checkpoints.
Every destination is prepared while inactive; the exact validated target config
is then renamed once. That rename is the ownership boundary. Production drains
reload config after taking their own delivery lock, so stale drains skip a
removed identity. The config watcher schedules a fresh destination drain without
requiring a new event or restarting the service process.

Cooperative writers must honor the locks. Registry and supported config writers
serialize with publication. Linked-package edits, out-of-band replacements,
symlink races and ledger rewrites are prohibited during an operational window.
Observed config, manifest, script, source-prefix or ledger-prefix drift fails
closed before publication. These checks do not defend against a hostile writer
racing filesystem checks.

## State and failure matrix

`B/P` denote the separately verified compatible command/service/plugin builds;
they do not change during handoff. `Csrc/Cdst` are exact config byte hashes.
Each handler has its own checkpoint and pending range `(p_h, observed_end]`.

| Boundary or result | Config; build | Active identity and checkpoint | Pending work / response |
| --- | --- | --- | --- |
| Validation/lock failure (`rejected`) | Csrc; B/P | Source unchanged; no cursor copies | Diagnose the precondition. This does not repair pre-existing corruption. |
| Prepared receipt or partial/all destination writes, before config rename (`source_active`) | Csrc; B/P | Source active; destination copies inert | Inspect. A new attempt must use a new directory, reacquire locks and capture fresh source progress. |
| Config rename succeeded, no valid final receipt (`target_active` or `needs_inspection`) | Cdst if validated; B/P | Destination starts at p_h or later; source retained | Never restore Csrc or recopy active cursors. Inspect prepared evidence and current state. |
| Directory sync failed or evidence/config is inconsistent (`needs_inspection`) | Observe, do not infer; B/P unchanged | No repair writes | Keep current wiring; obtain an operator decision. The CLI reports an error for a fresh durability failure. |
| Committed | Cdst; B/P | Destination advances; source frozen | Pending and later matching events stay deliverable. |
| Existing committed attempt (`already_committed`) | May differ from historical Cdst | Historical evidence only | No ownership change, even after a later round trip. |
| Opposite direction | New Csrc -> Cdst; B/P | Recapture now-active sources | New explicit request, new private receipt; never reuse an old numeric capture. |

Inspection validates version, workspace/registry paths, identity map, private
config/template hashes, raw checkpoint evidence, ledger prefix, pending ranges
and committed receipt contents. Active destinations must validate and be at
least as far as their recorded transfer. It is a point-in-time read, not a lock
or authorization to retry writes. Equal config bytes cannot prove that no
intervening round trip occurred.

Atomic writes expose `renamed`, `dir_synced` and `dir_sync_error`. Preparation
syncs files and their containing/new-parent directories before publication; the
final receipt directory is synced too. `power_loss_durable: true` records that
these requested syncs succeeded. It is **not** a hardware/filesystem guarantee.
Subprocess termination tests prove process-crash boundaries, not sudden power
loss, network-filesystem semantics or dishonest storage-controller behavior.
There is no multi-file rollback.

## Evidence format and privacy

The create-once directory is mode 0700 and its files are 0600:

- `config-before.yaml` and `config-target.yaml` contain exact private bytes.
- `legacy-template.yaml` preserves reverse input bytes.
- `prepared.json` contains version/attempt/direction/map, config hashes,
  command executable SHA-256 and Go/VCS build data when available, manifest and
  plugin source/version, registry/instance-config hashes, resolved script paths/digests, original source and
  destination cursor bytes, and ledger/pending ranges.
- `committed.json` binds the attempt to the publication report and transfers.

The command's build evidence never attests the running service binary. Receipts
explicitly mark that service evidence unverified. Runtime/supervisor evidence
belongs in a separate reviewed operational packet. Receipts are local diagnostic
evidence, not signed attestations against an attacker who can rewrite them.
Keep raw snapshots private; exported CLI summaries contain hashes, paths,
positions and diagnostics, not configuration values. For example:

```json
{
  "status": "committed",
  "direction": "reverse",
  "plugin": "example",
  "receipt_dir": "/private/recovery/attempt-2",
  "transfers": [
    {"source": "example/notify", "destination": "notify", "position": 4,
     "prefix_hash": "<64 lowercase hex characters>", "observed_end": 5,
     "destination_had_checkpoint": true}
  ]
}
```

This is an abbreviated illustrative summary, not a receipt to apply.

## At-least-once side effects

The guarantee is no **handoff-induced replay of acknowledged events**, not
exactly-once effects. A failed executable batch, several successful Lua calls
followed by failure, or death after an effect but before checkpoint advancement
can repeat effects on normal retry. No durable failure journal here can prove
that uncertainty absent. Locks do not wait for arbitrary detached external work.
Known or suspected partial delivery must be reconciled before an operational
handoff; never hide it with end-seeding, cursor edits or an invented force flag.

The fixtures distinguish failure-before-effects (pending work transfers once)
from partial executable/Lua effects (normal retry visibly duplicates them).
Successful round trips deliver every matching event once across identities and
preserve nonmatching-event progress, source bytes and private backups.

## Runtime compatibility and reproducible proof

Supported source protocol: this implementation's locked seeding and existing
refresh-under-lock drains. `TestHandoffServiceAndSupervisorContinuity` runs the
real service Manager/config watcher in a separate fixture process, checks the
same service and supervisor PIDs across forward/reverse/forward, and proves
pending delivery without an append after publication. The supervisor is a stub,
not a Dispatch session or a systemd cgroup test.

**Old unlocked seeders at `30ec1b1`/`a019053` are unsupported for a concurrent
handoff.** `TestLegacyUnlockedSeedCanOverwritePreparedCheckpoint` reproduces
that exact Stat -> Count/PrefixHash -> write ordering with a barrier after Stat.
The old seeder ignores the held destination lock and overwrites a prepared
prefix with the log end, skipping pending work. This is a deterministic
algorithm counterexample, not an execution of the deployed service binary.
The installed/live service was not exercised or upgraded. A later operation
must verify a compatible service revision; an upgrade needs its own authorized
quiescent window. Do not restart or downgrade as an automatic fallback.

From this task's checkout, with Go and standard system tools installed:

```sh
bash scripts/test-plugin-recovery.sh test -timeout 120s \
  ./internal/handlers ./internal/pluginmgr ./internal/store \
  ./internal/workspace ./internal/registry ./internal/service ./internal/cli
bash scripts/test-plugin-recovery.sh test -race -timeout 180s \
  ./internal/handlers ./internal/pluginmgr ./internal/store \
  ./internal/workspace ./internal/registry ./internal/service ./internal/cli
bash scripts/test-plugin-recovery.sh test -timeout 180s ./...
bash scripts/test-plugin-recovery.sh vet ./...
git diff --check
```

The script clears the inherited environment and supplies temporary HOME,
registry/plugin and XDG paths. Migration subprocess fixtures further restrict
PATH to controlled helpers and use temporary cwd/state/output. They never load
the real Dispatch package/hooks, registry or wake commands. Tests cover both
handoff directions at each destination write, prepared/final receipts, config
rename and directory-sync boundaries; stale receipts, concurrent writers,
queued drains/seeds, corrupt prefixes, aliases and observed file drift are
covered separately. Exact command results and source/build hashes belong in
the JOB-0098 handoff packet, not inferred from this list of commands.
