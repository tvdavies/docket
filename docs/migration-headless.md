# Migrating to the headless CLI

Docket is now a CLI for agents and humans, plus an optional headless event
runner. The web board, HTTP API and plugin hosting have been removed. Task
files, event logs, cursors and hooks are unchanged, so no workspace migration
is needed.

## What stays the same

- Task files, comments, attachments, relationships, waits, references,
  projects and `events.jsonl`. Existing workspaces open unchanged.
- Executable and Lua hooks, `delivery: inline|service`, match filters,
  retries, and every handler checkpoint under `.cursors/handlers/`.
- `events` and `inbox`. `inbox --json` output is unchanged. (`watch` later
  became `events --follow`; see [Command consolidation](#command-consolidation).)
- Plugin install/enable/disable/update/validate, plugin hooks, contributed
  statuses, scoped config, CLI passthrough, and the `--adopt-cursors`
  ownership handoff. Plugin handler names and cursors are unchanged.
- The machine registry at `~/.config/docket/config.yaml`.

## What was removed

| Removed | Replacement |
|---|---|
| Web board, classic board and settings pages | The CLI and its `--json` output. A separate UI can drive Docket through the CLI. |
| HTTP API (`/api/...`), SSE streams, live widget ingest | `docket show/list --json`, `docket events --follow` for live events, hooks and `docket inbox` for delivery. |
| `docket serve` (including `--listen/--allow-remote`) | `docket run` (no listener). The deprecated `serve` alias has since been removed. |
| Plugin UI frames, iframe bridge SDK (`@docket/plugin-sdk`, `plugin-sdk.d.ts`) | None. Manifest `ui` sections still parse; nothing is served. |
| Plugin service proxy (`/plugins/<name>/`) and `options_from` fetching | None. Call plugin services directly from hooks or their own clients. |
| Supervised `service.command` processes and `docket plugin logs` | Run the process under systemd, a container or another supervisor. |
| `docket plugin new` / `docket plugin dev` scaffolding | `docket docs plugins/authoring` and `examples/plugins/hello-hooks`. |
| Widget publishing (`POST .../widgets/create|finalise`) | Record outcomes as comments, references or attachments. Existing widget events stay readable in `docket show`. |
| Settings UI and `PATCH /api/.../config` | `docket plugin config get/set`. |
| `listen` in the registry | Ignored. An existing value is kept for rollback. |

## New

- `docket run [--all]` — the foreground event runner, previously `serve`.
- `docket run --once [--all]` — one bounded drain for a heartbeat or
  scheduler; exits non-zero if a handler failed and leaves failed events
  pending.
- `docket plugin config get|set` — validated plugin settings at instance,
  workspace or status scope.
- `docket inbox --peek` and `docket inbox ack CHECKPOINT` — acknowledgement for
  durable consumers. See [Inbox consumers](inbox.md).
- `--mark-read` now acknowledges exactly the batch it returned. Previously an
  event appended during the read could be skipped.

## Upgrade steps

These are operator steps for an existing installation. Check each one against
your own machine before running it.

1. **Stop anything that publishes to the HTTP API.** For Dispatch, disable
   widget publishing (`DISPATCH_WIDGETS=0` in its environment) before upgrading.
   Look for other HTTP consumers too: scripts calling
   `http://127.0.0.1:7463`, browser bookmarks and reverse proxies. They will
   get connection refused after the upgrade.
2. **Check installed plugins for hosted processes:**

   ```sh
   docket plugin list
   docket plugin validate <path>    # warns on service.command
   ```

   Any `service.command` is no longer launched. Move it to a systemd unit or
   container before upgrading. `docket run` (including `--once`) also logs a
   warning for each enabled plugin that declares one, and for any enabled
   plugin whose manifest fails to load.
3. **Install the new binary**, keeping the old one for rollback.
4. **Rewrite the systemd unit** if you use it. The old unit runs
   `docket serve --all`, which no longer exists, so the unit fails until you
   reinstall it; reinstalling switches it to `run --all`:

   ```sh
   docket service install
   docket service restart
   docket service status
   ```

   Container or supervisor definitions should run `docket run --all` in the
   foreground.
5. **Verify delivery.** Pending `delivery: service` events drain on start. To
   check a workspace by hand:

   ```sh
   docket run --once
   docket events --json | tail
   ```

## Command consolidation

A later release reduced the command surface. These commands were removed
outright, with no compatibility aliases; scripts must use the replacements.

| Removed | Use instead |
|---|---|
| `docket serve [--all]` | `docket run [--all]` |
| `docket attach`, `detach`, `current` | `docket session attach`, `detach`, `current` |
| `docket context TASK-ID` | `docket show TASK-ID` |
| `docket files TASK-ID` | `docket show TASK-ID` (attachments section, or `--view current --json \| jq .attachments`) |
| `docket wait show TASK-ID` | `docket show TASK-ID --view current --json \| jq .wait` |
| `docket reference list TASK-ID` | `docket show TASK-ID --view current --json \| jq .references` |
| `docket unlink TASK-ID --REL TARGET-ID` | `docket link TASK-ID --REL TARGET-ID --remove` |
| `docket watch [--from-start]` | `docket events --follow [--from-start]` |
| `docket reindex` | `docket workspace reindex` |

Removed names are no longer reserved, so a plugin or a `docket-NAME`
executable on `PATH` may now claim them. JSON from `docket link` gains a
`removed` boolean.

## Rollback

Task and event files are forward and backward compatible across this change.
To roll back, reinstall the previous binary and its unit (`docket service
install` from the old binary), then restart it. The board and API return
without rewriting job history. Handler cursors advanced by the new runner are
valid for the old one.

Inbox checkpoints written by `inbox ack` live in `.cursors/<actor>.checkpoint`
beside the unchanged numeric `.cursor` file. Older binaries ignore them.
