# CLI guide

Docket commands operate on the `.docket/` workspace discovered from the current directory. Use explicit task IDs in scripts and agent prompts; session attachment is optional shorthand, not a prerequisite.

## Discovering and correcting usage

```sh
docket --help
docket move --help
docket project --help
docket workspace --help
docket plugin --help
```

When arguments or flags are invalid, Docket prints the error, exact usage line, and relevant help command. Operational failures such as a missing task report only the underlying error:

```text
docket: accepts between 1 and 2 arg(s), received 0

Usage: docket move [TASK-ID] STATUS [flags]
Run 'docket move --help' for examples and flags.
```

Data-returning commands accept `--json`. Successful JSON goes to stdout; diagnostics and handler logs go to stderr, so stdout remains safe to parse. Foreground/service-control commands may stream their native human or JSONL output instead; check that command's help.

## Normal task workflow

```sh
cd my-workspace
docket init

ID=$(docket new --title "Fix login cache" --label bug)
docket show "$ID"
docket edit "$ID" --assignee researcher
docket comment "$ID" "Root cause: cache key omits pwdVersion"
docket attach-file "$ID" ./repro.log --caption "Failing assertion"
docket move "$ID" in-review
```

A later human or agent resumes with:

```sh
docket show TASK-0001
```

`show` returns the complete context bundle: description, active wait, references, attachments, project, assignee, labels, resolved relationships, sessions, and a chronological activity timeline. Use `--comments N` to limit comment bodies included in the bundle and timeline.

## Task commands

| Command | Purpose |
|---|---|
| `docket new --title TITLE` | Create a task and print its ID |
| `docket list` | List and filter tasks |
| `docket show [TASK-ID]` | Read a complete context bundle |
| `docket edit [TASK-ID]` | Change title, description, or assignee |
| `docket move [TASK-ID] STATUS` | Change status lane |
| `docket wait set\|show\|resolve TASK-ID` | Record or resolve one external dependency |
| `docket comment [TASK-ID] TEXT` | Add immutable durable context |
| `docket label [TASK-ID]` | Add or remove labels |
| `docket attach-file [TASK-ID] PATH` | Copy an artifact into the task |
| `docket files [TASK-ID]` | List attached files |
| `docket reference add\|list\|remove TASK-ID` | Manage typed external links |
| `docket link TASK-ID --RELATIONSHIP TARGET` | Create a typed relationship |
| `docket unlink TASK-ID --RELATIONSHIP TARGET` | Remove a relationship |

Run any command with `--help` for exact flags and examples.

Installed plugins may contribute git-style commands: `docket NAME ARGS...`
executes the plugin's declared CLI, otherwise Docket searches `docket-NAME` on
`PATH`. Builtin command names always win. See [Plugins](plugins.md).

### Descriptions and comments

Use a quoted argument for short text and a file for multiline content:

```sh
docket new --title "Investigate latency" --desc-file ./brief.md
docket edit TASK-0001 --desc-file ./updated-brief.md
docket comment TASK-0001 --file ./findings.md
printf 'Generated note\n' | docket comment TASK-0001 --file -
```

### Filters

List filters are exact and combine with logical AND:

```sh
docket list --status in-review --label bug
docket list --project PROJ-0001 --assignee researcher --json
```

Valid statuses come from `.docket/config.yaml`. An unknown status error prints the configured values.

### Labels

Repeat flags to change several labels:

```sh
docket label TASK-0001 --add bug --add urgent
docket label TASK-0001 --remove urgent
```

### Waits and references

Status identifies the workflow stage; an active wait identifies the one external condition preventing that stage from continuing. Resolving requires the exact wait ID so a stale watcher cannot clear a newer condition.

```sh
docket wait set TASK-0001 --kind ci --reason "Awaiting required checks" --ref https://github.com/example/repo/pull/42
WAIT_ID=$(docket wait show TASK-0001 --json | jq -r .id)
docket wait resolve TASK-0001 --wait-id "$WAIT_ID" --result green

docket reference add TASK-0001 --kind pr --url https://github.com/example/repo/pull/42
docket reference list TASK-0001
```

See [Waits, references, and activity](waits-and-references.md) for the event and automation contract.

### Relationships

Relationship flags come from workspace configuration. Defaults include `--blocks`, `--blocked-by`, `--parent`, `--subtasks`, `--relates`, `--duplicate-of`, and `--duplicates`. Docket maintains the inverse on the target task automatically.

```sh
docket link TASK-0001 --blocks TASK-0002
docket unlink TASK-0001 --blocks TASK-0002
```

## Optional session shorthand

```sh
docket session --help
```

A session pointer allows commands with optional task IDs to omit the ID. It does not claim, assign, lock, or start a task. Explicit IDs are safer for automation. See [Session attachment](sessions.md) for semantics and guidance.

## Projects

Projects are named task groupings inside one workspace, not separate stores:

```sh
PROJECT=$(docket project new --name "Website")
docket new --title "Improve navigation" --project "$PROJECT"
docket project list
docket project show "$PROJECT"
```

## Automation and event diagnostics

| Command | Intended use |
|---|---|
| `docket events [--since N]` | Inspect the append-only event log |
| `docket watch [--from-start]` | Stream JSONL for diagnostics or a transient consumer |
| `docket inbox [--mark-read]` | Poll unread events using an actor cursor |

Configured handlers are preferred for durable event-driven automation. See [Lua hooks and SDK](lua-hooks.md).

## Workspaces and service

`docket init` creates and registers a workspace, so manual registry commands are uncommon.

```sh
docket workspace check                 # validate current config/store
docket workspace list
docket workspace add ~/dev/other --name other
docket workspace remove other          # files remain untouched
```

`serve` is the foreground process; `service` controls the background systemd user unit:

```sh
docket serve --all                     # foreground/debugging
docket service install
docket service start
docket service status
docket service logs
docket service restart
docket service uninstall
```

There is one service per user/machine and any number of registered workspaces.

## Environment variables

| Variable | Purpose |
|---|---|
| `DOCKET_HOME` | Explicit project or `.docket` path; bypasses upward discovery |
| `DOCKET_ACTOR` | Authorship identity; otherwise Git user, then `unknown` |
| `DOCKET_SESSION` | Optional session pointer ID |
| `DOCKET_CONFIG` | Override machine registry path |

The global flags `--json` and `--session` override output/session behaviour for one invocation.
