# Interactive board and styled output

Docket has two kinds of output. Tools get plain, stable text (or JSON with
`--json`). People at a terminal get a styled view of the same data and an
interactive board. Nothing here changes what scripts and agents see.

## When output is styled

Styling applies only when stdout is a terminal and `--json` is not set.
Piped, redirected, and agent-harness output is plain text, unchanged.

| Situation | `docket` | `docket show` / `docket list` |
| --- | --- | --- |
| stdin and stdout are terminals | opens the board | styled |
| stdout is a terminal, stdin is not | prints help | styled |
| stdout is piped or redirected | prints help | plain text |
| `--json` | prints help | JSON |

`NO_COLOR` removes colour but keeps the layout. `TERM=dumb` removes every
escape sequence. Docket asks the terminal for its background colour to pick
light or dark colours. Set `DOCKET_THEME=light` or `DOCKET_THEME=dark` if your
terminal does not answer.

## Styled `show`

`docket show TASK-ID` prints a header (ID, status, title, assignee, project,
labels, and any active wait), the description rendered as markdown, then
relationships, references, attachments, and the activity timeline. The
timeline is grouped by day. Each entry has an icon, the actor, a sentence such
as "moved backlog → in-progress" or "started waiting on review: PR needs
approval", and both clock and relative times. Comments appear under their
entry as markdown. An event type docket does not recognise is shown by name
with its data as `key=value` pairs.

`--view`, `--activity`, `--activity-before`, and `--comments` work as they do
for plain output. The timeline heading shows which slice you are reading, and
a note tells you how to read older items.

When the output is taller than the terminal it goes through `$PAGER` (default
`less -R`). If `LESS` is unset, docket sets `LESS=FRX`. Set `PAGER=cat` or
pass `--no-pager` to print directly.

## Styled `list`

`docket list` prints an aligned table sized to the terminal, with coloured
statuses. Titles are shortened to fit, so use `--json` or a pipe when you need
full titles.

## The board

Open it with `docket` (no arguments), `docket board`, or `docket tui`. It shows
the workspace that `docket show` would use. It needs an interactive terminal.

Lanes follow the `statuses` order in the workspace config, the same order
`docket move` accepts. Tasks whose status is not configured get extra lanes at
the end. When the terminal is too narrow for every lane, lanes scroll
sideways to keep the selected one in view.

| Key | Board | Task detail |
| --- | --- | --- |
| `h` `l` `←` `→` | previous / next lane | previous / next tab |
| `tab` `shift+tab` | next / previous lane (wraps) | next / previous tab |
| `j` `k` `↓` `↑` | next / previous card | scroll |
| `g` `G` | first / last card | top / bottom |
| `pgup` `pgdn` `u` `d` | | page up / down |
| `1`–`4` | | Overview, Comments, Activity, Links |
| `enter` | open the task | |
| `/` | filter by ID, title, label, assignee, or status (`enter` keeps it, `esc` clears it) | |
| `m` | move the task to a status | move the task |
| `c` | comment (`ctrl+s` saves, `esc` cancels) | comment |
| `r` | reload | reload |
| `?` | all keys | all keys |
| `q` `esc` | quit (`esc` clears a filter first) | back to the board |

Moves and comments go through the same code as `docket move` and `docket
comment`: they record the same events and run inline handlers. Handler output
is captured so it cannot corrupt the screen. Handler warnings appear in the
status line.

The board watches the workspace directory and refreshes when tasks change,
including changes made by other processes.
