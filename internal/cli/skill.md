# docket — agent skill

`docket` is a file-backed task store. It is your **durable memory across
sessions**: the task folder *is* the context. You do not serialize your own
working memory — you attach to a task, and everything you need is printed back.

Tasks are plain markdown + YAML under a `.docket/` directory, discovered by
walking up from the current directory like `.git`. An optional machine-wide
service watches registered workspaces; task files remain the source of truth.

## The core loop

```
docket attach TASK-0007            # bind this session + print the context bundle
# ...do work, using the bundle as your ground truth...
docket comment TASK-0007 "Root cause: stale cache key omits pwdVersion"
docket attach-file TASK-0007 ./repro.log --caption "failing assertion"
docket move TASK-0007 in-review
# session ends — your working memory evaporates, the task retains everything
```

A fresh session resumes with one command:

```
docket attach TASK-0007            # full continuity: description, comments, files, links
```

After `attach`, scoped commands (`comment`, `move`, `attach-file`, `show`,
`label`, `edit`, `files`) may **omit the task id** — they default to the
attached task.

## Identity & sessions

- Pass `--session <id>` (your harness session/conversation id) so attach/detach
  is traceable. Falls back to `$DOCKET_SESSION`, then a global pointer.
- The acting identity for comments/authorship comes from `$DOCKET_ACTOR`, else the
  git user, else "unknown". Set `DOCKET_ACTOR=agent:pi` (etc.) in your harness.

## Commands

Every command supports `--json` for stable machine output.

### Tasks
- `docket init` — ensure the current directory has `.docket/` and is registered with the service; safe to repeat.
- `docket new --title T [--desc D | --desc-file F] [--project P] [--label L]...` — prints the new id.
- `docket list [--status S] [--label L] [--project P] [--assignee A]`
- `docket show [TASK-ID]` — full dossier.
- `docket context [TASK-ID] [--comments N]` — the handoff bundle (same as attach prints).
- `docket edit [TASK-ID] [--title T] [--desc-file F] [--assignee A]`
- `docket move [TASK-ID] STATUS` — change lane.
- `docket label [TASK-ID] --add L --remove L`

### Comments & files
- `docket comment [TASK-ID] "text"` (or `--file -` for stdin) — append-only.
- `docket attach-file [TASK-ID] ./path [--caption "..."]` — any media.
- `docket files [TASK-ID]` — list attachments.

### Relationships & projects
- `docket link TASK-ID --blocks TASK-0010` — inverse maintained automatically.
  Other kinds: `--parent`, `--relates`, `--duplicate-of` (see `docket link --help`).
- `docket unlink TASK-ID --blocks TASK-0010`
- `docket project new --name "Website" [--desc ...]` → `PROJ-0001`
- `docket project list | docket project show PROJ-0001`

### Coordination (triggering work elsewhere)
docket records every change to an append-only event log. Optional post-hoc
handlers in `.docket/config.yaml` receive matching events as JSON lines on stdin;
each has a durable cursor and failed deliveries retry. Mutating commands drain
handlers after the event is durable.

- `docket inbox --mark-read --json` — **poll**: unread events on tasks assigned to
  you, since your last cursor.
- `docket watch` — **stream**: blocks and emits one workspace's events as JSON lines.
- `docket serve --all` — watches every registered workspace and drains handlers.
- `docket workspace add [PATH] --name NAME` — register a workspace with that service.
- `docket events [--since N]` — the raw event log.

## How to use this as durable context

1. When you start a unit of work, `docket new` it (or `attach` an existing one).
2. Record **decisions, root causes, and dead ends** as `comment`s — these are
   what the next session reads. Be specific; the comment log is the memory.
3. Attach artifacts (logs, screenshots, diffs) with `attach-file`.
4. `move` the task as state changes so an event handler can route follow-up.
5. Never assume the next session remembers anything you did not write down.
