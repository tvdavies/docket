# docket — agent guide

Docket is a file-backed task store that keeps durable context between sessions. Tasks live as Markdown and YAML under `.docket/`. Record anything a later session will need in the task; don't rely on memory.

## Workspace and identity

- Commands find the workspace by walking up from the current directory to `.docket/`. `DOCKET_HOME` overrides this. Run `docket init` to create one (safe to repeat).
- `DOCKET_ACTOR` sets your name as author; otherwise the Git user is used.
- Always pass an explicit TASK-ID. Session pointers (`docket session --help`) exist but are shared state; avoid them in automation.

## Find and read

```sh
docket list --status ready --label bug          # exact filters, combined with AND
docket list --assignee "$DOCKET_ACTOR" --json
docket show TASK-0007 --view agent              # state + newest 20 activity items
docket show TASK-0007 --view current            # state only, no history
docket show TASK-0007                           # full context bundle
```

Every view keeps the description, active wait, references, relationships, and attachment list. When history is cut off, the agent view prints the `--activity-before POS` command that reads older items.

## Create and update

```sh
ID=$(docket new --title "Fix login cache" --label bug --desc-file ./brief.md)
docket edit TASK-0007 --title "Key cache by pwdVersion" --assignee reviewer
docket comment TASK-0007 "Root cause: cache key omits pwdVersion"
docket label TASK-0007 --add urgent --remove triage
docket attach-file TASK-0007 ./repro.log --caption "Failing assertion"
docket reference add TASK-0007 --kind pr --url https://github.com/org/repo/pull/42
docket move TASK-0007 in-review
```

Multiline text comes from a file or stdin: `--desc-file -` for descriptions (`new`, `edit`), `--file -` for comments. Quote other multi-word text as one argument.

## Wait and hand off

Keep the status unchanged while blocked on something external; record one wait instead:

```sh
docket wait set TASK-0007 --kind review --reason "Awaiting security review"
docket wait show TASK-0007 --json               # {"id": "wait-...", ...}
docket wait resolve TASK-0007 --wait-id WAIT-ID --result approved
```

To hand off, comment with the current state and next step, assign the next owner, and move the task:

```sh
docket comment TASK-0007 --file ./handoff.md
docket edit TASK-0007 --assignee reviewer
docket move TASK-0007 in-review
```

## Output and pipes

- Markdown (the default) is the smallest form for reading. `--json` is for fields; add `--compact` for one line.
- stdout carries only data; errors go to stderr with a non-zero exit.
- Filter with shell tools instead of looking for flags. Piped `list` prints full titles as tab-separated columns:

```sh
docket list | grep -i cache
docket list --json | jq -r '.[] | select(.wait) | .id'
docket show TASK-0007 --json | jq '{status, wait}'
docket events | grep TASK-0007 | tail -n 5
```

## Anything else

Don't guess flags. On a usage error Docket prints the exact usage line; then read the command's help, or the one reference topic you need:

```sh
docket COMMAND --help            # exact flags and examples, e.g. docket link --help
docket docs                      # list topics
docket docs cli                  # workflows, output views, projects, relationships
docket docs waits-and-references # wait and reference automation contract
docket docs configuration        # statuses, relationships, event handlers
docket docs lua-hooks            # Lua hook SDK
docket docs inbox                # polling and durable event consumers
docket docs plugins/authoring    # writing plugins
docket docs plugin-recovery      # plugin handler migration and recovery
docket skill --full              # the complete agent guide
```
