# Session attachment

Session attachment is an optional convenience for interactive agent harnesses. Docket does not require it.

## What attaching does

```sh
export DOCKET_SESSION="agent-turn-42"
docket session attach TASK-0007
```

This operation:

1. verifies that `TASK-0007` exists;
2. serialises changes to the named session pointer;
3. when that session was attached to another task, records its detach there;
4. stores a machine-local pointer from `agent-turn-42` to `TASK-0007`;
5. appends an attach record to the task's `sessions.jsonl` audit;
6. emits `task.attached`; and
7. prints the task's context bundle.

It does **not**:

- assign the task;
- claim or lease work;
- lock the task;
- change its status;
- launch an agent; or
- prevent another session from attaching.

Assignment is task metadata (`docket edit TASK-ID --assignee ACTOR`). Agent launching belongs in an event handler.

## Why it exists

Some harnesses give every conversation or worker turn a stable session ID. Attaching once lets later commands omit the repeated task ID:

```sh
docket session attach TASK-0007 --session agent-turn-42
docket comment "Confirmed the failing cache key"
docket move in-review
docket session current
docket session detach
```

Commands that support an omitted task ID include `show`, `edit`, `move`, `label`, `comment`, `attach-file`, and `files`.

The pointer is resolved in this order:

1. `--session ID`;
2. `DOCKET_SESSION`;
3. `_global`.

The `_global` fallback is shared by every terminal or agent that does not provide a session ID. It is convenient for one-person interactive use but unsafe for concurrent automation.

## When not to use it

Prefer explicit IDs when:

- a hook already receives `event.task`;
- a Dispatch wake payload identifies the task;
- several agents run concurrently;
- commands are generated independently; or
- auditability matters more than saving one argument.

For Dispatch-style event-driven workers, the recommended pattern is:

```sh
docket show "$TASK_ID" --json
docket comment "$TASK_ID" --file findings.md
docket move "$TASK_ID" in-review
```

No attachment is necessary.

## Storage and cleanup

Pointers live under the machine-local, gitignored path:

```text
.docket/.sessions/<sanitised-session-id>.current
```

Attach/detach audit entries are exposed in `docket show --json` under `sessions`
and interleaved with task events and comments under `activity`. Unmatched attach
records are also exposed as `active_sessions`; the local board uses that derived
view for its live working badge. They live with the task at:

```text
.docket/tasks/TASK-.../sessions.jsonl
```

Clear a pointer with:

```sh
docket session detach --session agent-turn-42
```

Legacy flat commands (`docket attach`, `docket detach`, and `docket current`) remain compatible but are hidden from root help. New integrations should use `docket session ...`.
