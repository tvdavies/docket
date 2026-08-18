# Lua hooks and SDK

Docket embeds [GopherLua](https://github.com/yuin/gopher-lua), a Lua 5.1 runtime, but executes every event in a disposable child invocation of the Docket binary. No system Lua installation is required.

## Minimal hook

`.docket/config.yaml`:

```yaml
handlers:
  cheer-on-done:
    on: [task.moved]
    match:
      data.to: done
    lua: hooks/cheer.lua
    delivery: service
```

`hooks/cheer.lua`:

```lua
function handle(event, docket)
    docket.log.info("cheering for", event.task)
    docket.process.run("pw-play", {
        "--volume", "0.7",
        docket.asset("assets/cheer.wav"),
    })
end
```

The script must define global function `handle(event, docket)`. Docket loads the script and calls it once for one matching event. A fresh process and Lua state are used for the next event; globals do not persist.

## Runtime and trust model

Lua hooks receive GopherLua's complete standard libraries, including:

```text
base, package, coroutine, table, io, os, string, math, debug, channel
```

Hooks are trusted code running with the Docket user's filesystem and environment permissions. They may use `io.open`, `os.execute`, `io.popen`, `require`, and other normal Lua facilities.

Process isolation prevents `os.exit()` or blocked Lua IO from terminating or permanently blocking the long-lived Docket service. A handler timeout kills the hook process group and ordinary children. A process deliberately detached into a new session is responsible for its own lifecycle.

## Event object

Every event contains this common shape; optional fields may be absent:

```lua
{
    seq = 42,
    time = "2026-08-18T12:00:00Z",
    type = "task.moved",
    task = "TASK-0007",
    title = "Fix login cache",
    actor = "Tom Davies",
    assignee = "researcher",
    data = {
        from = "ready",
        to = "in-progress",
    },
}
```

### Event types

| Type | Relevant `data` fields |
|---|---|
| `task.created` | — |
| `task.updated` | — |
| `task.assigned` | —; new assignee is the top-level `assignee` |
| `task.moved` | `from`, `to` |
| `task.commented` | — |
| `task.labeled` | `labels` |
| `task.linked` | `kind`, `to` |
| `task.unlinked` | `kind`, `to` |
| `task.attached` | `session` |
| `task.detached` | `session` |
| `task.file_attached` | `file`, `mime` |
| `project.created` | `project`; project name is `title` |

Use config-level `match` for simple filtering rather than repeating conditions inside every script:

```yaml
on: [task.moved]
match:
  data.to: done
```

## Paths

### `docket.path(...)`

Joins components relative to the project root—the directory containing `.docket/`:

```lua
local report = docket.path("reports", event.task .. ".txt")
```

### `docket.asset(...)`

Joins components relative to the Lua script's directory. It is useful when a hook and its supporting templates, audio, or helper modules should move together:

```lua
local template = docket.asset("assets", "message.txt")
```

It only returns a path; it does not read, copy, validate, or embed the file.

### Static path fields

```lua
docket.paths.project     -- project root
docket.paths.workspace   -- absolute .docket directory
docket.paths.script      -- absolute script filename
docket.paths.script_dir  -- script's containing directory
```

Absolute paths passed to path helpers remain absolute.

## Logging

```lua
docket.log.info("processing", event.task)
docket.log.warn("using fallback")
docket.log.error("provider unavailable")
```

Arguments are joined with spaces. Inline logs go to the invoking command's stderr; service-delivered logs go to `docket service logs`.

Lua's normal `print()` is also available, but SDK logging records a clear severity.

## Filesystem

The full `io` and `os` libraries are available:

```lua
function handle(event, docket)
    local path = docket.path("reports", event.task .. ".txt")
    local file = assert(io.open(path, "w"))
    file:write("Completed by " .. (event.actor or "unknown") .. "\n")
    file:close()
end
```

For replacement writes where readers must never observe partial content:

```lua
docket.fs.write_atomic("reports/latest.txt", "complete\n")
docket.fs.write_atomic("scripts/generated.sh", "#!/bin/sh\n", 493)
```

Relative paths resolve from the project root. The optional permission is a decimal Unix file mode: `420` is `0644`, and `493` is `0755`.

Do not edit `.docket/events.jsonl`, task dossiers, cursor files, or other Docket internals directly. Use SDK task operations so locks, atomic writes, and events remain correct.

## Processes

```lua
docket.process.run("git", {"status", "--short"})
docket.process.run("pw-play", {docket.asset("complete.wav")})
```

`process.run(command, args)`:

- runs from the project root;
- inherits the hook environment;
- streams stdout and stderr to handler logs;
- returns `true` on success; and
- raises a Lua error on startup failure or non-zero exit.

Use `pcall` if a non-zero exit is an expected branch:

```lua
local ok, failure = pcall(function()
    docket.process.run("optional-command", {"--check"})
end)
if not ok then
    docket.log.warn("optional check failed", failure)
end
```

The standard `os.execute` and `io.popen` remain available when their APIs are more convenient.

## Task SDK

### Read a task

```lua
local task = docket.task.get(event.task)
```

Task fields:

```text
id, title, status, project, labels, assignee, relationships,
description, created_at, updated_at, dir
```

`labels` is an array and `relationships` maps relationship names to task-ID arrays.

### Move

```lua
local updated, previous = docket.task.move(event.task, "in-review")
docket.log.info(previous, "->", updated.status)
```

Emits `task.moved` after using the same validation, lock, and atomic write as the CLI.

### Assign

```lua
local updated = docket.task.assign(event.task, "reviewer")
docket.task.assign(event.task, "") -- clear assignment
```

Emits `task.assigned`.

### Comment

```lua
local filename = docket.task.comment(event.task, "Research complete")
```

Appends an immutable comment and emits `task.commented`. The comment author is `handler:<handler-name>`.

### Labels

```lua
docket.task.label(event.task, {"researched", "ready"}, {"needs-research"})
```

Arguments are `(task_id, labels_to_add, labels_to_remove)`. Either array may be `nil` or empty. Emits `task.labeled`.

## Generated events and recursion

SDK task mutations append ordinary Docket events. The outer handler runner drains those events after the current Lua invocation returns. This permits event pipelines, for example:

1. `task.moved` to `research` runs a researcher hook;
2. the hook comments and moves the task to `in-review`;
3. a reviewer handler receives the new move event.

Docket stops a chain that does not settle after 32 drain rounds. Design handlers so they do not move a task repeatedly to the same triggering state.

## Failures, retries, and idempotency

A Lua error, failed SDK call, non-zero process exit, or timeout fails the event delivery. The handler cursor remains before the event and the service retries it on a later drain.

Delivery is at least once. If a hook performs an external side effect and then fails, that side effect may repeat. Use provider idempotency keys, check existing state, or make writes naturally repeatable.

`os.exit(0)` is treated as successful completion of the current event. Each event runs in a separate child, so exiting one event cannot skip later events in the delivery backlog.

## Environment

Every handler receives:

```text
DOCKET_HOME=/absolute/path/to/.docket
DOCKET_ACTOR=handler:<handler-name>
DOCKET_HANDLER=<handler-name>
DOCKET_HANDLER_STACK=<ancestry>
```

Service-delivered hooks inherit variables from the systemd unit and optional `~/.config/docket/environment` file. Restart the service after changing that file.

## Debugging hooks

1. Validate config:

   ```sh
   docket workspace check
   ```

2. Temporarily omit `delivery: service` or set `delivery: inline` so errors appear directly in the mutating command.

3. Trigger a real matching event:

   ```sh
   docket move TASK-0007 ready
   docket move TASK-0007 done
   ```

4. For service delivery, inspect:

   ```sh
   docket service status
   docket service logs
   docket events --json
   ```

5. Remember that a newly named handler starts at cursor zero and may process historical matching events.
