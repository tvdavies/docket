# tadu — agent skill

`tadu` is a file-backed, CLI-only task store. It is your **durable memory across
sessions**: the task folder *is* the context. You do not serialize your own
working memory — you attach to a task, and everything you need is printed back.

No server, no database. Tasks are plain markdown + YAML under a `.tadu/`
directory, discovered by walking up from the current directory like `.git`.

## The core loop

```
tadu attach TASK-0007            # bind this session + print the context bundle
# ...do work, using the bundle as your ground truth...
tadu comment TASK-0007 "Root cause: stale cache key omits pwdVersion"
tadu attach-file TASK-0007 ./repro.log --caption "failing assertion"
tadu move TASK-0007 in-review
# session ends — your working memory evaporates, the task retains everything
```

A fresh session resumes with one command:

```
tadu attach TASK-0007            # full continuity: description, comments, files, links
```

After `attach`, scoped commands (`comment`, `move`, `attach-file`, `show`,
`label`, `edit`, `files`) may **omit the task id** — they default to the
attached task.

## Identity & sessions

- Pass `--session <id>` (your harness session/conversation id) so attach/detach
  is traceable. Falls back to `$TADU_SESSION`, then a global pointer.
- The acting identity for comments/authorship comes from `$TADU_ACTOR`, else the
  git user, else "unknown". Set `TADU_ACTOR=agent:pi` (etc.) in your harness.

## Commands

Every command supports `--json` for stable machine output.

### Tasks
- `tadu init` — scaffold `.tadu/` in the current directory.
- `tadu new --title T [--desc D | --desc-file F] [--project P] [--label L]...` — prints the new id.
- `tadu list [--status S] [--label L] [--project P] [--assignee A]`
- `tadu show [TASK-ID]` — full dossier.
- `tadu context [TASK-ID] [--comments N]` — the handoff bundle (same as attach prints).
- `tadu edit [TASK-ID] [--title T] [--desc-file F] [--assignee A]`
- `tadu move [TASK-ID] STATUS` — change lane.
- `tadu label [TASK-ID] --add L --remove L`

### Comments & files
- `tadu comment [TASK-ID] "text"` (or `--file -` for stdin) — append-only.
- `tadu attach-file [TASK-ID] ./path [--caption "..."]` — any media.
- `tadu files [TASK-ID]` — list attachments.

### Relationships & projects
- `tadu link TASK-ID --blocks TASK-0010` — inverse maintained automatically.
  Other kinds: `--parent`, `--relates`, `--duplicate-of` (see `tadu link --help`).
- `tadu unlink TASK-ID --blocks TASK-0010`
- `tadu project new --name "Website" [--desc ...]` → `PROJ-0001`
- `tadu project list | tadu project show PROJ-0001`

### Coordination (triggering work elsewhere)
tadu is a passive store; it records every change to an append-only event log and
lets other processes react. It never executes anything itself.

- `tadu inbox --mark-read --json` — **poll**: unread events on tasks assigned to
  you, since your last cursor. A heartbeat drains this to pick up new work.
- `tadu watch` — **stream**: blocks and emits each new event as a JSON line the
  instant it happens, for push-based harnesses.
- `tadu events [--since N]` — the raw event log.

## How to use this as durable context

1. When you start a unit of work, `tadu new` it (or `attach` an existing one).
2. Record **decisions, root causes, and dead ends** as `comment`s — these are
   what the next session reads. Be specific; the comment log is the memory.
3. Attach artifacts (logs, screenshots, diffs) with `attach-file`.
4. `move` the task as state changes so a watcher/heartbeat can route follow-up.
5. Never assume the next session remembers anything you did not write down.
