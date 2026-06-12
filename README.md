# tadu

**TA**sk **DU**rable — a file-backed, CLI-only task store that hands context between agent sessions.

> *to-do → ta-da.* A task starts as a to-do and ends as a ta-da; tadu is the file that carries the work — and all its context — from one agent session to the next.

Durable tasks are the whole point: plain files in a directory, **no database**, surviving across sessions, machines, and `git clone`. An agent picks up a task, does work, and hands full context to the next session by *attaching to the task*. Harness-neutral — it stores and reports; something else runs the agent.

A single static Go binary, ~8MB, zero runtime dependencies.

## Install

For a human or an agent — one command, no Go required:

```sh
curl -fsSL https://raw.githubusercontent.com/tvdavies/tadu/main/scripts/install.sh | sh
```

This drops `tadu` into `~/.local/bin` and prints how to add it to `PATH`. Then:

```sh
tadu skill        # print the agent usage guide (drop into any harness)
```

Build from source instead:

```sh
make install      # builds and installs to ~/.local/bin
# or
go install github.com/tvdavies/tadu@latest
```

## Quick start

```sh
cd my-project
tadu init                                          # scaffold .tadu/
ID=$(tadu new --title "Fix login cache" --label bug)
tadu attach "$ID"                                  # bind session + print context bundle
tadu comment "$ID" "Root cause: cache key omits pwdVersion"
tadu attach-file "$ID" ./repro.log --caption "failing assertion"
tadu move "$ID" in-review
```

A fresh session resumes with full continuity:

```sh
tadu attach "$ID"     # description + every comment + artifacts + links, resolved
```

## The handoff

The task folder *is* the durable memory. A new session does not reconstruct
what the last one knew — it `attach`es and reads the **context bundle**:
description, comments (decisions and dead ends), attachments, and relationships
resolved to human-meaningful titles.

## Coordination (triggering work elsewhere)

tadu is a passive store. Every mutation appends to an append-only event log;
other processes react. tadu never executes anything itself.

- `tadu inbox --mark-read --json` — **poll**: unread events on tasks assigned to
  you, tracked by a per-actor cursor. A heartbeat drains this to pick up work.
- `tadu watch` — **stream**: blocks and emits each new event as a JSON line the
  instant it happens, for push-based harnesses.
- `tadu events [--since N]` — the raw log.

## Identity

- `--session <id>` (or `$TADU_SESSION`) scopes attach/detach per caller.
- `$TADU_ACTOR` (else git user, else "unknown") is the authorship identity.

## Develop

```sh
make build        # → bin/tadu
make test
go test ./...
make snapshot     # cross-platform release build (needs goreleaser)
```

## On-disk layout

Everything is text-first and git-trackable:

```
.tadu/
  config.yaml            # statuses, labels, relationship types
  events.jsonl           # append-only event log
  tasks/TASK-0001-fix-login-cache/
    task.md              # YAML frontmatter + markdown description
    comments/0001--<ts>.md
    attachments/{manifest.yaml, ...files}
    sessions.jsonl       # attach/detach audit
  projects/PROJ-0001-website.md
```

The filesystem is the source of truth. `.index/` (if present) is a rebuildable
cache (`tadu reindex`), never authoritative.

## License

MIT — see [`LICENSE`](./LICENSE).
