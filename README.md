# docket

A file-backed task CLI for humans and agents, with durable context, event hooks, and an optional headless event runner.

> A **docket** is the slip that travels with a job through the shop, carrying its details; a court docket is a list of cases moving through their stages. Both readings are the product: the task folder is the docket — it carries the work and its context between people, tools, and processes.

Durable tasks are the whole point: plain files in a directory, **no database**, surviving across sessions, machines, and `git clone`. A later human or tool resumes by reading the task's complete context bundle. Harness-neutral workspace handlers decide what runs when events arrive; execution protocols and live run interfaces remain outside Docket.

A single static Go binary with zero runtime dependencies. The CLI works by
itself; an optional event runner (`docket run`) watches registered workspaces
and delivers asynchronous hooks. Docket has no web UI or HTTP API: a separate
UI can drive it through the CLI and its JSON output.

> **Upgrading from a release with the web board?** See
> [Migrating to the headless CLI](docs/migration-headless.md).

## Install

For a human or an agent — one command, no Go required:

```sh
curl -fsSL https://raw.githubusercontent.com/tvdavies/docket/main/scripts/install.sh | sh
```

This drops `docket` into `~/.local/bin` and prints how to add it to `PATH`. Then:

```sh
docket skill        # print the agent usage guide (drop into any harness)
```

Build from source instead:

```sh
make install      # builds and installs to ~/.local/bin
# or
go install github.com/tvdavies/docket@latest
```

## Quick start

```sh
cd my-project
docket init                                          # create + register (safe to repeat)
ID=$(docket new --title "Fix login cache" --label bug)
docket show "$ID"                                    # complete context bundle
docket comment "$ID" "Root cause: cache key omits pwdVersion"
docket attach-file "$ID" ./repro.log --caption "failing assertion"
docket move "$ID" in-review
```

A later caller resumes with full continuity:

```sh
docket show "$ID"       # dossier + waits + references + sessions + activity
```

## Documentation

- [CLI guide](docs/cli.md) — workflows, command map, flags, errors, and examples
- [Configuration reference](docs/configuration.md) — workspace and runner config
- [Waits, references, and activity](docs/waits-and-references.md) — durable external dependencies and temporal context
- [Lua hooks and SDK](docs/lua-hooks.md) — runtime, event schema, APIs, and debugging
- [Inbox consumers](docs/inbox.md) — polling, durable acknowledgement, and recovery
- [Plugins](docs/plugins.md) — manifests, installation, hooks, statuses, and config
- [Authoring plugins](docs/plugins/authoring.md) — build a headless plugin
- [Session attachment](docs/sessions.md) — optional pointer semantics and when to use it
- [Migrating to the headless CLI](docs/migration-headless.md) — removed web features and upgrade steps

Run `docket COMMAND --help` for exact local usage and examples, or `docket skill`
for a self-contained guide suitable for an agent harness.

## The handoff

The task folder *is* durable memory. `docket show TASK-ID` returns the context
bundle a fresh session needs: description, active wait, typed references,
comments, session history, attachments, relationships, and one chronological
activity stream.

Session attachment is optional shorthand that lets later commands omit the task
ID. It does not assign, claim, lock, or start work; explicit IDs are recommended
for automation. See [Session attachment](docs/sessions.md).

## Workspaces and the event runner

A **Docket workspace** is one `.docket/` store, normally rooted in a repository.
A Docket **project** is a logical grouping inside that store. `docket init` both
creates and registers the current workspace, and is safe to repeat. One
optional event runner handles any number of registered workspaces:

```sh
cd ~/dev/client-a && docket init
docket workspace add ~/dev/client-b --name client-b  # explicit name for an existing store
docket workspace list

docket run --all                           # foreground runner for every workspace
docket run --once --all                    # deliver pending hooks once and exit
docket service install                     # optional systemd user unit (runs `run --all`)
docket service start
docket service status
docket service logs                        # journalctl follow
```

The runner opens no network listener. It watches each workspace's event log
and config, drains every handler's durable cursor, and reloads hooks when
`config.yaml` or an installed plugin manifest changes. `--once` performs one
bounded drain for a heartbeat or scheduler and exits non-zero if any handler
failed; failed events stay pending. Containers run `docket run --all` in the
foreground under their own supervisor.

The machine-local registry is `~/.config/docket/config.yaml` (or
`$DOCKET_CONFIG`). It contains workspace and installed-plugin registrations,
instance plugin config, and the prune grace; task data stays in each
workspace. The runner notices registry changes within two seconds, isolates
each workspace, drains handler backlogs, and marks missing workspaces
unavailable rather than crashing. A registration whose directory stays missing
beyond `prune_after` (default one hour; `never` disables) is unregistered
automatically with its task files untouched. Without `--all`, `docket run`
watches only the current workspace.

The systemd unit runs once per user/machine, never once per workspace. It does
not enable login lingering automatically; opt in explicitly with
`loginctl enable-linger "$USER"` if hooks must keep running outside login
sessions. The generated unit captures the current `PATH` and optionally loads
`~/.config/docket/environment`; use that file for variables required by
handler scripts.

## Coordination (triggering work elsewhere)

Every mutation appends to an append-only event log. There are three ways to
react:

- **Handlers** — post-hoc executables or embedded Lua scripts declared in
  `.docket/config.yaml`. Every handler owns a durable cursor: delivery is
  ordered and at-least-once, failed batches retry, and an offline handler drains
  its backlog. Inline delivery is the default; `delivery: service` leaves
  execution to the event runner so mutations return immediately without
  sacrificing durable retry.
- `docket inbox --mark-read --json` — **poll**: unread events on tasks assigned to
  you, tracked by a per-actor cursor. Durable consumers use `--peek` and
  `docket inbox ack CHECKPOINT` instead (see [docs/inbox.md](docs/inbox.md)).
- `docket watch` — **stream**: emits each new event as a JSON line for one
  workspace; it remains a diagnostic primitive rather than the daemon.
- `docket run [--all] [--once]` — **runner**: watches registered workspaces and
  drains handlers for events written outside a synchronous CLI mutation.
- `docket events [--since N]` — the raw log.

Handlers subscribe by event type, may add exact-value `match` predicates, and
use exactly one runtime:

```yaml
handlers:
  notify:
    on: [task.moved]
    match:
      data.to: done
    lua: hooks/notify.lua
    delivery: service
```

- `lua:` runs a trusted Lua 5.1 `handle(event, docket)` function in an isolated
  Docket child process with full standard libraries and a lightweight SDK.
- `run:` preserves the executable JSONL hook interface.

See the [configuration reference](docs/configuration.md) for every field and
[Lua hooks and SDK](docs/lua-hooks.md) for the event schema, complete API,
examples, retry semantics, and debugging guide.

## Identity

- `--session <id>` (or `$DOCKET_SESSION`) selects an optional current-task pointer; see [Session attachment](docs/sessions.md).
- `$DOCKET_ACTOR` (else git user, else "unknown") is the authorship identity.

## Develop

Development needs only Go.

```sh
make build        # → bin/docket
make test
make vet
make fmt-check
make snapshot     # cross-platform release build (needs goreleaser)
```

## On-disk layout

Each workspace is text-first and git-trackable:

```
.docket/
  config.yaml            # statuses, labels, relationships, handlers, plugins
  events.jsonl           # append-only event log
  tasks/TASK-0001-fix-login-cache/
    task.md              # YAML frontmatter + markdown description
    comments/0001--<ts>.md
    attachments/{manifest.yaml, ...files}
    sessions.jsonl       # attach/detach audit
  projects/PROJ-0001-website.md
```

The filesystem is the source of truth. `.index/` (if present) is a rebuildable
cache (`docket reindex`), never authoritative. Machine-local handler cursors
live under the gitignored `.cursors/handlers/` directory.

## License

MIT — see [`LICENSE`](./LICENSE).
