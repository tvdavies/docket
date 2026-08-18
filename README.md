# docket

A generic file-backed task system with durable context, event hooks, and a local Kanban board.

> A **docket** is the slip that travels with a job through the shop, carrying its details; a court docket is a list of cases moving through their stages. Both readings are the product: the task folder is the docket — it carries the work and its context between people, tools, and processes.

Durable tasks are the whole point: plain files in a directory, **no database**, surviving across sessions, machines, and `git clone`. A later human or tool resumes by reading the task's complete context bundle. Harness-neutral workspace handlers decide what runs when events arrive; execution protocols and live run interfaces remain outside Docket.

A single static Go binary, ~8MB, zero runtime dependencies. The CLI works by
itself; an optional systemd user service watches all registered workspaces and
serves one local writable Kanban board.

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
- [Configuration reference](docs/configuration.md) — workspace and service config
- [Web interface](docs/web-interface.md) — board features, security, and HTTP API
- [Waits, references, and activity](docs/waits-and-references.md) — durable external dependencies and temporal context
- [Lua hooks and SDK](docs/lua-hooks.md) — runtime, event schema, APIs, and debugging
- [Session attachment](docs/sessions.md) — optional pointer semantics and when to use it

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

## Workspaces and the machine-wide service

A **Docket workspace** is one `.docket/` store, normally rooted in a repository.
A Docket **project** is a logical grouping inside that store. `docket init` both
creates and registers the current workspace, and is safe to repeat. One optional
user service manages any number of registered workspaces:

```sh
cd ~/dev/client-a && docket init
docket workspace add ~/dev/client-b --name client-b  # explicit name for an existing store
docket workspace list

docket serve --all                         # foreground; http://127.0.0.1:7463
docket service install                     # write the systemd user unit
docket service start                       # enable and start it
docket service status
docket service logs                        # journalctl follow
```

The machine-local registry is `~/.config/docket/config.yaml` (or
`$DOCKET_CONFIG`). It contains only names, paths, and the listen address; task
data stays in each workspace. The service notices registry changes within two
seconds, isolates each workspace runtime, drains handler backlogs, and marks
missing workspaces unavailable rather than crashing. Without `--all`,
`docket serve` watches only the current workspace.

The systemd unit runs once per user/machine — never once per workspace — and
serves one writable Kanban UI with all registered workspaces. Board mutations
use the same locks, atomic writes, validation, and events as CLI commands. It
does not enable login lingering
automatically; opt in explicitly with `loginctl enable-linger "$USER"` if the
service must continue outside login sessions. The generated unit captures the
current `PATH` and optionally loads `~/.config/docket/environment`; use that file
for variables required by handler scripts. The UI has no authentication and
refuses non-loopback binds unless `--allow-remote` is passed explicitly.

## Coordination (triggering work elsewhere)

Every mutation appends to an append-only event log. There are three ways to
react:

- **Handlers** — post-hoc executables or embedded Lua scripts declared in
  `.docket/config.yaml`. Every handler owns a durable cursor: delivery is
  ordered and at-least-once, failed batches retry, and an offline handler drains
  its backlog. Inline delivery is the default; `delivery: service` leaves
  execution to `docket.service` so mutations return immediately without
  sacrificing durable retry.
- `docket inbox --mark-read --json` — **poll**: unread events on tasks assigned to
  you, tracked by a per-actor cursor.
- `docket watch` — **stream**: emits each new event as a JSON line for one
  workspace; it remains a diagnostic primitive rather than the daemon.
- `docket serve [--all]` — **service**: watches registered workspaces and drains
  handlers for events written outside a synchronous CLI mutation.
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

```sh
make build        # → bin/docket
make test
go test ./...
make snapshot     # cross-platform release build (needs goreleaser)
```

## On-disk layout

Each workspace is text-first and git-trackable:

```
.docket/
  config.yaml            # statuses, labels, relationships, event handlers
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
