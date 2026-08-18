# docket — agent skill

`docket` is a file-backed task store and durable memory across sessions. Tasks are Markdown + YAML under `.docket/`; waits, references, comments, sessions, attachments, relationships, and events preserve context after a model session ends.

## Start here

Use explicit task IDs unless your harness intentionally uses session pointers:

```sh
docket show TASK-0007 --json
# ...perform the work...
docket comment TASK-0007 "Root cause: stale cache key omits pwdVersion"
docket attach-file TASK-0007 ./repro.log --caption "Failing assertion"
docket move TASK-0007 in-review
```

A fresh session resumes with:

```sh
docket show TASK-0007 --json
```

`show` returns the task description, active wait, typed references, comments, files, project, labels, assignee, sessions, resolved relationships, and chronological activity.

## Correcting command mistakes

Every command has examples and exact flags:

```sh
docket --help
docket move --help
docket workspace --help
```

On an argument or flag mistake, Docket prints the exact usage line and relevant help command. Operational failures report their underlying error directly. Do not guess repeatedly; read that command's `--help`. Use quoted arguments for multi-word text or `--file` for multiline content.

Data-returning commands accept `--json`. Successful JSON is written to stdout; errors and handler logs use stderr. Foreground and service-control commands may stream native output instead; inspect their help.

## Workspace and identity

```sh
docket init                 # create + register current workspace; safe to repeat
docket workspace check      # validate config and summarise store
```

- Workspace discovery walks upward for `.docket/`.
- `DOCKET_HOME` overrides discovery.
- `DOCKET_ACTOR` sets authorship; otherwise Git user, then `unknown`.
- `DOCKET_SESSION` identifies an optional session pointer.

## Task commands

```sh
docket new --title TITLE [--desc TEXT | --desc-file FILE] [--project ID] [--status STATUS] [--label LABEL]...
docket list [--status STATUS] [--label LABEL] [--project ID] [--assignee ACTOR]
docket show TASK-ID [--comments N]
docket edit TASK-ID [--title TITLE] [--desc TEXT | --desc-file FILE] [--assignee ACTOR]
docket move TASK-ID STATUS
docket wait set TASK-ID --kind KIND --reason TEXT [--ref URL]
docket wait show TASK-ID
docket wait resolve TASK-ID --wait-id WAIT-ID [--result RESULT]
docket reference add TASK-ID --kind KIND --url URL [--title TITLE]
docket reference list TASK-ID
docket reference remove TASK-ID REFERENCE-ID
docket comment TASK-ID "TEXT"
docket comment TASK-ID --file FILE
docket label TASK-ID --add LABEL --remove LABEL
docket attach-file TASK-ID PATH [--caption TEXT]
docket files TASK-ID
docket link TASK-ID --blocks TARGET
docket unlink TASK-ID --blocks TARGET
```

Use `--file -` to read a description or comment from stdin.

## Durable-context practice

1. Create or identify one task for each unit of work.
2. Read `docket show TASK-ID --json` before acting.
3. Record decisions, evidence, root causes, and dead ends as comments.
4. Store plan, pull-request, ticket, and transcript URLs as typed references.
5. Attach logs, screenshots, and other file artifacts.
6. Keep workflow status unchanged while waiting; set one explicit wait instead.
7. Resolve only the exact wait ID observed by the external watcher.
8. Move status when ownership or workflow phase changes.
9. Never assume a future session remembers facts absent from the task.

## Optional session shorthand

Attachment only stores a current-task pointer. It does not assign, claim, lock, or launch work.

```sh
export DOCKET_SESSION="agent-turn-42"
docket session attach TASK-0007
docket comment "TASK-ID can now be omitted"
docket move in-review
docket session detach
```

Prefer explicit IDs for hooks, concurrent agents, and independently generated commands. Without `--session` or `DOCKET_SESSION`, attachment uses a shared `_global` pointer.

Legacy `docket attach`, `detach`, and `current` commands remain compatible but `docket session ...` is the documented surface.

## Projects

```sh
docket project new --name "Website"
docket project list
docket project show PROJ-0001
docket new --title "Improve navigation" --project PROJ-0001
```

Projects group tasks inside one workspace; they are not separate workspaces.

## Event automation

Every mutation appends to `.docket/events.jsonl`. Prefer durable configured handlers over polling or `docket watch`.

```yaml
handlers:
  route-ready:
    on: [task.moved]
    match:
      data.to: ready
    lua: hooks/route.lua
    delivery: service
```

- `on` is a list of exact event types or `["*"]`.
- `match` uses exact dotted paths; nested paths are supported under `data`.
- Use exactly one of `lua:` or `run:`.
- `delivery: service` is asynchronous and durable; `inline` is the default.
- A new handler name starts at cursor zero and may receive historical events.
- Failures retain the cursor and retry; hooks must be idempotent.

Validate configuration with:

```sh
docket workspace check
```

## Lua hooks

Lua handlers define one global function:

```lua
function handle(event, docket)
    docket.log.info("handling", event.type, event.task)
end
```

Each matching event runs in a fresh isolated Docket child process with full GopherLua standard libraries, including `io`, `os`, `package`, and `debug`.

Common event fields:

```lua
event.seq
event.time
event.type
event.task
event.title
event.actor
event.assignee
event.data
```

For `task.moved`, use `event.data.from` and `event.data.to`.

### Lua SDK

```lua
docket.path(...)                         -- relative to project root
docket.asset(...)                        -- relative to Lua script directory
docket.paths.project
docket.paths.workspace
docket.paths.script
docket.paths.script_dir

docket.log.info(...)
docket.log.warn(...)
docket.log.error(...)
docket.fs.write_atomic(path, content, permission)
docket.process.run(command, {args})

docket.task.get(id)
docket.task.move(id, status)              -- returns task, previous status
docket.task.assign(id, assignee)
docket.task.comment(id, text)             -- returns comment filename
docket.task.label(id, {add}, {remove})
docket.task.wait(id, {kind=KIND, reason=TEXT, reference=URL})
docket.task.resume(id, wait_id, result)
docket.task.reference_add(id, kind, url, title)
docket.task.reference_remove(id, reference_id)
```

Example using ordinary Lua IO:

```lua
function handle(event, docket)
    local file = assert(io.open(docket.path("reports", event.task .. ".txt"), "w"))
    file:write("Handled " .. event.type .. "\n")
    file:close()
end
```

SDK task mutations use Docket's locks, atomic writes, validation, and event production. Do not edit `.docket` internals directly.

For service-delivered failures:

```sh
docket service status
docket service logs
docket events --json
```

## Low-level coordination

```sh
docket events [--since N] --json
docket watch [--from-start]
docket inbox [--actor ACTOR] [--all] [--mark-read] --json
```

These are diagnostics or integration primitives. Configured handlers are the normal durable event mechanism.
