# Tadu — Design Doc

> **Name: Tadu** — **TA**sk **DU**rable. Durable tasks are the whole point: file-backed, no database, surviving across sessions, machines, and `git clone`. It also reads as *to-do* (what it's for) with a hint of *ta-da* (closing one out). Four keystrokes, clean as a CLI binary (`tadu`) and workspace dir (`.tadu/`), no known collision.

A lightweight, **file-backed, CLI-only task system for agents**. Think "local Linear" that lives as plain files in a directory, has no server and no database, and exists so an agent can pick up a task, do work, and hand full context to the next session by *attaching to the task*.

This is deliberately **harness-neutral**: it does not embed pi, Claude Code, OpenClaw, or El Jefe. It is a utility any of them can drive through a small CLI. The task store is the durable thing; the agent runtime is interchangeable.

---

## Goals

- **Durable task context that survives sessions.** A new agent session attaches to a task and recovers everything it needs to continue: description, history, decisions, artifacts.
- **File-backed, no database.** The filesystem is the source of truth. Markdown + YAML frontmatter for human/agent-readable state; raw files for attachments. Git-diffable, greppable, reviewable, syncable.
- **Agent-first CLI.** A small command surface designed to be called by an agent, with stable `--json` output and a human-readable default.
- **Local and zero-infrastructure.** `cd` into a project, run a command. No daemon required for the core model.
- **Rich enough to be useful:** title, description, comments, file attachments (any media), labels, status, projects, and typed task relationships.

## Non-goals (v1)

- No web UI, no server, no realtime sync. (A viewer can come later; the files render fine in any markdown tool.)
- No multi-user auth or access control. Local/trusted use only.
- No built-in agent runtime. Tadu stores and reports; something else runs the agent.
- No automation engine in the core. Lane-transition triggers are designed-for but live in an optional layer (see Extensions).

---

## Core concepts

| Concept | What it is |
|---|---|
| **Workspace** | A directory (`.tadu/`) holding all tasks, projects, config, and attachments. Discovered by walking up from `cwd`, like `.git`. |
| **Task** | The unit of work and **the unit of durable context**. A folder with a markdown dossier, comments, and attachments. |
| **Project** | A named grouping of tasks (a markdown file with metadata). Optional. |
| **Comment** | An immutable, append-only log entry on a task. One file per comment. |
| **Attachment** | Any file (doc/image/video/audio/binary) stored under the task, with metadata in a manifest. |
| **Relationship** | A typed, bidirectional link between two tasks (blocks, parent/subtask, relates, duplicate). |
| **Session attach** | A binding between an agent session and a task. Attaching yields the task's **context bundle** — the handoff mechanism. |

### The headline: context handoff via attach

The whole point. An agent session does not serialize its own memory. Instead **the task is the memory**:

```
Session A:  tadu attach TASK-0007        # binds, prints the context bundle
            ...does work...
            tadu comment TASK-0007 "Found the root cause: stale cache key"
            tadu attach-file TASK-0007 repro.log
            tadu move TASK-0007 in-review
            (session A ends — its working memory evaporates)

Session B:  tadu attach TASK-0007        # fresh session, full continuity
            # bundle = description + every comment + artifact manifest + links + project
```

Because the context lives in files, handoff is robust, harness-neutral, and survives restarts, machine moves, and `git clone`. No runtime coupling. The `attach` command's printed **context bundle** is the contract a new session reads to resume.

---

## On-disk layout

The filesystem **is** the database. Everything below is canonical and git-trackable.

```
.tadu/
  config.yaml                     # statuses, labels, relationship types, settings
  .next-id                        # monotonic id counter (flock-guarded)
  projects/
    PROJ-0001-website.md          # frontmatter + description
  tasks/
    TASK-0007-fix-login-cache/
      task.md                     # frontmatter (metadata) + markdown description
      comments/
        0001--2026-06-12T14-03-22Z.md   # append-only; one file per comment
        0002--2026-06-12T15-10-04Z.md
      attachments/
        manifest.yaml             # [{file, mime, caption, added_by, added_at, bytes}]
        repro.log
        screenshot.png
      sessions.jsonl              # append-only attach/detach audit
  .index/                         # OPTIONAL derived cache — never canonical, always rebuildable
    tasks.json
```

Principles:

- **Markdown frontmatter is the source of truth.** If `.index/` is deleted, Tadu rebuilds it by scanning. If the markdown is deleted, the data is gone. (Mirrors your memory-wiki stance.)
- **No DB means no migrations.** Schema changes are just frontmatter-field additions; old tasks read with defaults.
- **Comments and sessions are append-only** — separate files / JSONL lines never rewritten, so concurrent writers never conflict on them.
- **Binary attachments are just files.** Git or git-LFS handling is the user's choice (see Open Decisions).

### Task frontmatter (`task.md`)

```markdown
---
id: TASK-0007
title: Fix login cache invalidation
status: in-review
project: PROJ-0001
labels: [bug, auth]
assignee: agent:pi            # free-form actor id; optional
relationships:
  blocks: [TASK-0010]
  blocked-by: []
  parent: TASK-0003
  subtasks: [TASK-0008]
  relates: [TASK-0005]
  duplicate-of: null
created_at: 2026-06-12T13:55:01Z
updated_at: 2026-06-12T15:10:04Z
---

Users stay logged in after a password reset because the session cache key
omits the password version. Invalidate on credential change.

## Acceptance
- Password reset forces re-auth on all devices
- Regression test covering the cache key
```

The body below the frontmatter is the **description** — free markdown, the durable spec.

### Comment (`comments/NNNN--<ts>.md`)

```markdown
---
author: agent:pi
session: sess-9f2c
created_at: 2026-06-12T15:10:04Z
---

Root cause confirmed: cache key is `user:{id}` but must be `user:{id}:{pwdVersion}`.
Patch in `repro.log`. Moving to review.
```

### Attachment manifest (`attachments/manifest.yaml`)

```yaml
- file: repro.log
  mime: text/plain
  caption: "Reproduction with failing assertion"
  added_by: agent:pi
  added_at: 2026-06-12T15:09:40Z
  bytes: 4213
- file: screenshot.png
  mime: image/png
  caption: "Still-authenticated session after reset"
  added_by: tom
  added_at: 2026-06-12T15:09:55Z
  bytes: 91022
```

### `config.yaml`

```yaml
# Statuses double as board lanes, in order.
statuses: [backlog, ready, in-progress, blocked, in-review, done]

# Optional: closed statuses (treated as done for filtering)
terminal: [done]

labels: [bug, feature, chore, auth, frontend, backend]   # advisory; free labels allowed

relationships:                # typed link kinds and their inverses
  - { name: blocks,      inverse: blocked-by }
  - { name: parent,      inverse: subtasks }
  - { name: relates,     inverse: relates }      # symmetric
  - { name: duplicate-of, inverse: duplicates }

settings:
  id_prefix: TASK
  id_padding: 4
```

---

## CLI surface

Binary: `tadu` (working name). Every command supports `--json`; default output is concise human markdown. Read commands are pure; write commands take a flock.

### Tasks
```
tadu init                                   # scaffold .tadu/ in cwd
tadu new --title T [--desc D|--desc-file F] [--project P] [--label L]...   → prints new id
tadu list [--status S] [--label L] [--project P] [--assignee A] [--json]
tadu show TASK-0007 [--json]                # full dossier
tadu context TASK-0007 [--json] [--comments N]   # the HANDOFF bundle (see below)
tadu edit TASK-0007 [--title T] [--desc-file F] [--assignee A]
tadu move TASK-0007 in-review               # status change (fires hooks if enabled)
tadu label TASK-0007 --add bug --remove chore
```

### Comments & attachments
```
tadu comment TASK-0007 "text"   |   --file -        # append-only
tadu attach-file TASK-0007 ./repro.log [--caption "..."]
tadu files TASK-0007                                # list attachments
```

### Relationships & projects
```
tadu link TASK-0007 --blocks TASK-0010              # maintains inverse automatically
tadu unlink TASK-0007 --blocks TASK-0010
tadu project new --name "Website" [--desc ...]      → PROJ-0001
tadu project list | show PROJ-0001
```

### Session attach (the continuity primitive)
```
tadu attach TASK-0007 [--session <id>]      # bind + print context bundle; logs to sessions.jsonl
tadu detach [--session <id>]
tadu current [--session <id>]               # what am I attached to?
```
- `--session <id>` identifies the caller; falls back to `$TADU_SESSION` then a workspace-global "current" pointer.
- After attach, scoped commands (`comment`, `move`, `attach-file`) may omit the task id and default to the attached one.

### The context bundle (`tadu context` / printed by `attach`)

The single most important output — what a fresh session reads to continue:

```json
{
  "id": "TASK-0007",
  "title": "Fix login cache invalidation",
  "status": "in-review",
  "project": { "id": "PROJ-0001", "name": "Website" },
  "labels": ["bug", "auth"],
  "assignee": "agent:pi",
  "description": "Users stay logged in after a password reset...",
  "relationships": {
    "blocks":   [{ "id": "TASK-0010", "title": "Ship auth hardening" }],
    "parent":   { "id": "TASK-0003", "title": "Auth epic" },
    "subtasks": [{ "id": "TASK-0008", "title": "Add pwdVersion to key" }]
  },
  "comments": [
    { "author": "agent:pi", "created_at": "...", "body": "Root cause confirmed..." }
  ],
  "attachments": [
    { "file": "attachments/repro.log", "mime": "text/plain", "caption": "Reproduction..." }
  ]
}
```

Relationship entries are resolved to include titles so the agent gets meaning, not just ids.

---

## Concurrency & integrity

- **Mutating writes take a per-task `flock`** on `tasks/<id>/.lock` (read-modify-write of `task.md` frontmatter). Matches your existing flock-based save patterns.
- **ID allocation** takes a global `flock` on `.next-id`; the counter is the durable source, but `new` also reconciles against `max(existing ids)+1` to self-heal if the counter is lost.
- **Append-only files** (comments, `sessions.jsonl`) need no locking for distinct entries — unique timestamped filenames / atomic line appends.
- **Atomic writes**: write to a temp file, `fsync`, rename — never a partial `task.md`.
- All writes update `updated_at` and append a line to `sessions.jsonl` / an event trail for audit.

## Git & sync

Everything text-first → first-class in git: diffable history, blame, PR review of task changes, sync across machines for free. Recommended `.gitignore`: `.tadu/.index/` (rebuildable) and per-session current-pointer files. Binary attachments: see Open Decisions.

## Querying without a database

For a local store (hundreds–low-thousands of tasks), **scan-on-read is fine**: glob `tasks/*/task.md`, parse frontmatter, filter. If/when it matters, `.index/tasks.json` is a derived cache rebuilt on write (or via `tadu reindex`) — never authoritative. No DB, no query engine.

---

## Harness integration

Tadu ships a **skill / usage doc** (markdown) describing the CLI, droppable into any harness:

- **Claude Code / pi / El Jefe / OpenClaw**: add the skill so the agent knows the commands and the attach→work→comment→move loop.
- **El Jefe specifically**: replaces the bespoke `task-wiki` + `el-jefe-cli` task verbs with this portable utility; El Jefe keeps gateway/heartbeat/memory and shells out to `tadu`.
- The agent's session id (channel:conversationId, pi session id, etc.) is passed as `--session` so attach/detach is traceable per harness.

## Extensions (designed-for, not in v1 core)

The earlier idea — **lane-transition triggers** — lives here, kept out of the pure store:

- `config.yaml` may declare per-status `on_enter` hooks (e.g. spawn a session with a prompt/skill).
- A **separate watcher/daemon** (or a harness heartbeat) polls `tadu list --status <lane> --json` or tails an event trail and acts. Tadu stays a passive store; the runtime owns execution.
- Exactly-once, loop-guard, and session-keying concerns (discussed previously) belong to that watcher, not to Tadu.

This keeps the core a clean, testable file library and lets automation be opt-in and harness-specific.

---

## Implementation sketch

- **Bun + TypeScript**, single small package, near-zero deps (YAML + frontmatter parse; both tiny). `bun test`.
- Library core (`createTask`, `appendComment`, `attachFile`, `link`, `move`, `contextBundle`, `query`) with the CLI as a thin wrapper — so a harness can also import the library directly instead of shelling out.
- Pure functions over a `Workspace` rooted at a directory → trivially testable against a tmpdir, no mocks.

### v1 build order
1. Workspace discovery + `init` + `config.yaml` load.
2. Task create/show/list with frontmatter read/write + flock + atomic rename.
3. Comments (append-only) + `context` bundle.
4. Attachments + manifest.
5. Labels, projects, typed relationships (with inverse maintenance).
6. `attach`/`detach`/`current` session binding + `sessions.jsonl`.
7. `--json` everywhere + the harness skill doc.
8. Optional `.index/` cache + `reindex`.

---

## Open decisions (need your call)

1. ~~**Name.**~~ **Decided: Tadu.**
2. **Workspace scope.** Per-directory `.tadu/` discovered upward like git (proposed) vs a single global workspace vs configurable via `$TADU_HOME`. Could support both (local-first, global fallback).
3. **Large binary attachments.** Store in-tree (simple; needs git-LFS for big media) vs a content-addressed blob dir with references vs leave it to the user. Proposed: in-tree + document LFS.
4. **Actor identity.** How is `author` / `assignee` set — explicit `--author`, an env var (`$TADU_ACTOR`), or git user? Proposed: `$TADU_ACTOR` → git user → "unknown".
5. **Triggers in scope?** Keep automation entirely in the optional extension layer (proposed) or bake a minimal `on_enter` hook into v1.
6. **Projects model.** Project as a metadata file referenced by id (proposed) vs project as its own subtree of tasks. Cross-project relationships allowed?
7. **Attach semantics.** Persist a workspace-global "current task" for convenience vs strictly per-`--session` binding. Proposed: per-session, with global fallback only when no session id is supplied.
```
