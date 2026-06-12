# tadu

**TA**sk **DU**rable — a file-backed, CLI-only task store that hands context between agent sessions.

> *to-do → ta-da.* A task starts as a to-do and ends as a ta-da; tadu is the file that carries the work — and all its context — from one agent session to the next.

Durable tasks are the whole point: plain files in a directory, **no database**, surviving across sessions, machines, and `git clone`. An agent picks up a task, does work, and hands full context to the next session by *attaching to the task*. Harness-neutral — it stores and reports; something else runs the agent.

## Status

Early design. See [`DESIGN.md`](./DESIGN.md) for the full architecture, on-disk layout, CLI surface, and open decisions.

## Develop

```sh
bun install
bun run tadu --help     # CLI entry (stub)
bun test
bun run typecheck
```
