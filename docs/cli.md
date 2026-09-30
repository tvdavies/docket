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

`show` returns the complete context bundle: description, active wait, references, attachments, project, assignee, labels, resolved relationships, sessions, and a chronological activity timeline.

## Choosing output for agent sessions

A task's full bundle grows with its whole history. `show` takes a view that
bounds what it returns without dropping current state:

| View | Returns | Use when |
|---|---|---|
| `--view current` | State only: status, labels, assignee, project, description, active wait, references, resolved relationships, attachment metadata | You only need to know where the task stands |
| `--view agent` | Current state plus the newest 20 activity items; comments and sessions appear only in the timeline | Resuming work; the usual agent read |
| `--view full` (default) | Everything, in the established JSON contract | Audits, scripts that already parse `show --json` |

Every view keeps the active wait and decision references, because they are
state, not history. The current view reports how much activity it left out.

History limits apply to the chronological activity timeline, which merges
comments, task events, and session attach/detach audits:

- `--activity N` returns the newest N timeline items (the agent view defaults
  to 20; the full view is unbounded unless you pass it).
- `--activity-before POS` returns items older than timeline position POS.
  Positions count items oldest-first from 0 and stay stable as new activity
  arrives, so paging is resumable.
- `--comments N` keeps only the newest N comments, in both `comments` and the
  timeline. Keep it the same between pages, since it changes positions.

When a limit applies, JSON includes `activity_page`
(`{total, start, end, truncated, next_before}`) and `comments_omitted`; pass
`next_before` as `--activity-before` to read the previous page. Markdown output
prints the equivalent command. Limits only shape output: they never change
stored history or acknowledge inbox events. Repeated reads of unchanged history
return identical output.

```sh
docket show TASK-0007 --view agent                          # state + newest 20 items
docket show TASK-0007 --view agent --activity-before 107    # the 20 before position 107
docket show TASK-0007 --view current --json --compact       # smallest machine read
docket show TASK-0007 --json                                # complete bundle, unchanged contract
```

Markdown (the default) is the most compact form for a model to read. Use
`--json` when a program needs fields; add the global `--compact` flag to print
single-line JSON. The full JSON bundle also repeats comments, sessions, and
legacy widget records outside `activity`; the `agent` and `current` views
include each fact once.

Measured on a task with 60 comments and 127 activity items (plus a wait, a
decision reference, and a relationship):

| Command | Bytes |
|---|---|
| `show --json` | 59,061 |
| `show --json --compact` | 43,767 |
| `show` (Markdown) | 21,594 |
| `show --view agent --json --compact` | 4,953 |
| `show --view agent` | 3,719 |
| `show --view current --json --compact` | 806 |
| `show --view current` | 451 |

Start with a filtered `list` to find the relevant task. `list --json` returns
task summaries without descriptions or history. The table shortens long titles
only on a terminal; piped output keeps them whole. Read a specific embedded topic
with `docs TOPIC` instead of loading every guide.

## Combining with grep, head, tail, and jq

Docket's flags cover what shell tools can't do well: choosing which task state
to show (`--view`) and cutting Markdown history on item boundaries
(`--activity`). Plain text filtering, searching, and field selection are left to
`grep`, `head`, `tail`, `cut`, and `jq`. Docket has no `--search`, `--limit`, or
event-type filter flags for this reason.

The output is designed for this:

- Diagnostics go to stderr and failures exit non-zero, so stdout is always safe
  to pipe and `&&` chains stop on errors.
- `list` prints an aligned table with shortened titles on a terminal. When
  stdout is not a terminal (a pipe, a file, or an agent harness) it prints full
  titles in tab-separated columns: `ID STATUS TITLE LABELS WAITING`.
- `events` prints one event per line; `show` starts each activity item with a
  `[time] actor · type` line.
- Every `--json` output is a single JSON document; the order of lists and
  activity is stable between reads.

```sh
docket list | grep -i cache                                    # search titles
docket list --status ready | cut -f1 | tail -n +2 | head -n 5  # first five ready IDs
docket list --json | jq -r '.[] | select(.wait) | .id'         # tasks that are waiting
docket show TASK-0001 | grep -A1 'task.moved'                  # status changes
docket show TASK-0001 --json | jq '{status, wait, references}' # selected fields
docket show TASK-0001 --json | jq -r '.activity[] | select(.kind == "comment") | .body' | tail -n 2
docket events | grep TASK-0001 | tail -n 3                     # recent events for a task
docket events --json | jq -c '.[] | select(.type == "task.moved")'
```

Use `--view` and `--activity` when reading Markdown: `tail -n` counts lines,
so it can split a multi-line comment. For JSON, `jq` slicing
(`.activity[-20:]`) and `--activity 20` are equivalent. `--compact` matches
`jq -c .` for callers without `jq`.

## Task commands

| Command | Purpose |
|---|---|
| `docket new --title TITLE` | Create a task and print its ID |
| `docket list` | List and filter tasks |
| `docket show [TASK-ID]` | Read task context (`--view current\|agent\|full`) |
| `docket edit [TASK-ID]` | Change title, description, or assignee |
| `docket move [TASK-ID] STATUS` | Change workflow status |
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
| `docket inbox [--mark-read]` | Poll unread events using an actor cursor; `--mark-read` acknowledges immediately |
| `docket inbox --peek` / `docket inbox ack CHECKPOINT` | Durable consumers: read, record, then acknowledge |

Configured handlers are preferred for durable event-driven automation. See [Lua hooks and SDK](lua-hooks.md) and [Inbox consumers](inbox.md).

## Workspaces and the event runner

`docket init` creates and registers a workspace, so manual registry commands are uncommon.

```sh
docket workspace check                 # validate current config/store
docket workspace list
docket workspace add ~/dev/other --name other
docket workspace remove other          # files remain untouched
```

`run` is the headless event runner. It delivers `delivery: service` hooks and
opens no network listener:

```sh
docket run                             # current workspace, foreground
docket run --all                       # every registered workspace, foreground
docket run --once --all                # drain pending hooks once and exit (heartbeat/cron)
```

`--once` exits non-zero when any handler fails; failed events stay pending for
the next run. With `--all`, a registered workspace whose directory is gone is
reported as `missing` without failing the run; the long-running `run --all`
prunes such registrations after `prune_after`. `docket serve` is a deprecated alias for `docket run` and
rejects the removed `--listen`/`--allow-remote` flags.

`service` optionally installs `docket run --all` as a systemd user unit:

```sh
docket service install                 # (re)write the unit
docket service start
docket service status
docket service logs
docket service restart
docket service uninstall
```

There is one runner per user/machine and any number of registered workspaces.

## Optional derived index

`docket reindex [--json]` writes `.docket/.index/tasks.json` from current task
summaries. No built-in command reads this cache. Run it only for an external
consumer that explicitly uses that file; normal task operations need no indexing.

## Documentation and plugins

The reference docs ship inside the binary, so they always match the installed version:

```sh
docket docs                            # list topics
docket docs plugins/authoring          # print one (Markdown)
docket docs inbox --json               # {name, title, content}
```

### Agent guide

`docket skill` prints a short entry guide to load into agent sessions. It
covers workspace discovery, identity, explicit task IDs, reading and updating
tasks, waits and handoff, output choices, and where to find exact flags. It
points to one `docket docs TOPIC` for anything else instead of including it.
`docket skill --full` prints the complete guide (topic `agent-guide`), which
also includes the Lua SDK, handler configuration, and plugin commands.

| Guide | Size | Approx. tokens |
|---|---|---|
| Previous all-in-one `docket skill` | 8.7 KB | ~2,200 |
| `docket skill` | 3.8 KB | ~950 |
| `docket skill --full` | 9.0 KB | ~2,300 |

Keep the entry guide under 4 KB (about 1,000 tokens); a test enforces this.
A typical session then loads the guide, one `show --view agent` read (usually
under 5 KB), and at most one reference topic when it needs one. Tests also
check every `docket` command and flag in the guides' shell examples against the
real command definitions.

Plugin commands:

```sh
docket plugin add ~/dev/my-plugin                 # install (link) a local plugin
docket plugin enable my-plugin --set key=value    # enable in this workspace
docket plugin validate ~/dev/my-plugin            # manifest + referenced files; --json for tooling
docket plugin config get my-plugin                # schemas and stored settings
docket plugin config set my-plugin key=value      # workspace scope (default)
docket plugin config set my-plugin --scope instance key=value
docket plugin config set my-plugin --status merge agent=merger
```

## Environment variables

| Variable | Purpose |
|---|---|
| `DOCKET_HOME` | Explicit project or `.docket` path; bypasses upward discovery |
| `DOCKET_ACTOR` | Authorship identity; otherwise Git user, then `unknown` |
| `DOCKET_SESSION` | Optional session pointer ID |
| `DOCKET_CONFIG` | Override machine registry path |

The global flags `--json`, `--compact`, and `--session` override output/session behaviour for one invocation.
