# docket

A file-backed task system that hands context between agent sessions.

> A **docket** is the slip that travels with a job through the shop, carrying its details; a court docket is a list of cases moving through their stages. Both readings are the product: the task folder is the docket — it carries the work, and all its context, from one agent session to the next.

Durable tasks are the whole point: plain files in a directory, **no database**, surviving across sessions, machines, and `git clone`. An agent picks up a task, does work, and hands full context to the next session by *attaching to the task*. Harness-neutral — workspace handlers decide what runs when events arrive.

A single static Go binary, ~8MB, zero runtime dependencies. The CLI works by
itself; an optional systemd user service watches all registered workspaces and
serves one local status interface.

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
docket attach "$ID"                                  # bind session + print context bundle
docket comment "$ID" "Root cause: cache key omits pwdVersion"
docket attach-file "$ID" ./repro.log --caption "failing assertion"
docket move "$ID" in-review
```

A fresh session resumes with full continuity:

```sh
docket attach "$ID"     # description + every comment + artifacts + links, resolved
```

## The handoff

The task folder *is* the durable memory. A new session does not reconstruct
what the last one knew — it `attach`es and reads the **context bundle**:
description, comments (decisions and dead ends), attachments, and relationships
resolved to human-meaningful titles.

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
serves one UI with all registered workspaces. It does not enable login lingering
automatically; opt in explicitly with `loginctl enable-linger "$USER"` if the
service must continue outside login sessions. The generated unit captures the
current `PATH` and optionally loads `~/.config/docket/environment`; use that file
for variables required by handler scripts. The UI has no authentication and
refuses non-loopback binds unless `--allow-remote` is passed explicitly.

## Coordination (triggering work elsewhere)

Every mutation appends to an append-only event log. There are three ways to
react:

- **Handlers** — post-hoc executables declared in `.docket/config.yaml`.
  Matching events arrive as JSON lines on stdin. Every handler owns a durable
  cursor: delivery is ordered and at-least-once, failed batches retry, and a
  handler that was offline drains its backlog. Mutating commands drain handlers
  synchronously after the event is durable.
- `docket inbox --mark-read --json` — **poll**: unread events on tasks assigned to
  you, tracked by a per-actor cursor.
- `docket watch` — **stream**: emits each new event as a JSON line for one
  workspace; it remains a diagnostic primitive rather than the daemon.
- `docket serve [--all]` — **service**: watches registered workspaces and drains
  handlers for events written outside a synchronous CLI mutation.
- `docket events [--since N]` — the raw log.

A handler declaration is deliberately only delivery configuration; decisions
belong in the script:

```yaml
handlers:
  notify:
    on: [task.moved, task.commented]  # or ["*"]
    run: hooks/notify                 # relative to the directory containing .docket/
```

Handler names use lowercase letters, numbers, hyphens, and underscores.
`hooks/notify` must be executable. It runs from the project root with
`DOCKET_HOME`, `DOCKET_ACTOR=handler:notify`, and `DOCKET_HANDLER=notify` set.
Stdout and stderr are treated as logs and written to the invoking command's
stderr, so `--json` output remains valid. A handler failure warns but cannot
roll back the mutation; its cursor remains before the failed batch. A newly
registered handler starts at cursor zero and therefore sees existing history.
Handlers should enqueue or spawn long-running work and exit within 30 seconds.

## Identity

- `--session <id>` (or `$DOCKET_SESSION`) scopes attach/detach per caller.
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
