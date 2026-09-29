# Authoring plugins

This guide is for anyone — human or coding agent — extending Docket. A plugin
is a directory with a `docket-plugin.yaml` manifest. It can add hooks,
statuses, scoped settings and a CLI subcommand. Pick only the parts you need.

Plugins are headless: Docket runs their hooks and CLI, but serves no UI and
launches no long-running process. If your integration needs a server, run it
under systemd, a container or another supervisor, and let hooks talk to it.

Reference material:

- [Plugins](../plugins.md) — manifest, install/enable, handlers, config.
- [Lua hooks](../lua-hooks.md) — the handler runtime and SDK.

## 1. Lay out the directory

[`examples/plugins/hello-hooks`](../../examples/plugins/hello-hooks) is a
complete, minimal plugin:

```text
hello-hooks/
  docket-plugin.yaml
  hooks/greet.lua
  bin/hello-hooks          # executable
```

```yaml
name: hello-hooks
version: 0.1.0
requires:
  docket: ">=0.6.0"
handlers:
  greet:
    on: [task.moved]
    match: {data.to: in-review}
    lua: hooks/greet.lua
    delivery: service
statuses:
  - {name: greeted, after: in-review}
config:
  workspace:
    greeting: {type: string, default: Hello}
cli:
  run: bin/hello-hooks
```

Rules the manifest validator enforces:

- `name` is lowercase letters, digits, `-` and `_`, and must not collide with a
  builtin command.
- Handler `run`/`lua` and `cli.run` paths stay inside the plugin directory.
- Unknown fields are errors — check spelling rather than adding fields.

## 2. Write a hook

A Lua hook defines `handle(event, docket)`. Plugin hooks also get
`docket.plugin` with the resolved settings:

```lua
function handle(event, docket)
    local greeting = docket.plugin.config.greeting or "Hello"
    docket.task.comment(event.task, greeting .. " from hello-hooks")
end
```

Delivery is ordered and at least once, so hooks must be idempotent. A hook
that fails leaves its batch pending for the next drain. Keep hooks short:
enqueue or hand off long work rather than doing it inline.

## 3. Install, enable and configure

From the workspace you want the plugin in:

```sh
docket plugin validate ./hello-hooks     # manifest and referenced files
docket plugin add ./hello-hooks          # links the checkout; edits apply in place
docket plugin enable hello-hooks         # validates against this workspace
docket plugin config set hello-hooks greeting='"Hi"'
```

An enabled plugin that fails validation makes its workspace unavailable, so
fix or `disable` it promptly.

## 4. Deliver events

`delivery: service` hooks run in the event runner, not in the command that
caused the event:

```sh
docket run --once          # deliver pending events and exit
docket run                 # or keep a foreground runner going while you edit
```

With a runner going, manifest and `config.yaml` edits apply as soon as they
are saved; handler cursors carry across the reload so nothing replays. Handler
output goes to the runner's stderr.

Inline hooks (the default delivery) run synchronously after each mutating
command, which is convenient while iterating:

```sh
docket move TASK-0001 in-review    # prints hook output on stderr
docket show TASK-0001              # see the comment it wrote
```

## 5. Add a CLI command

`cli.run` makes `docket <plugin-name> ARGS...` run your executable with
`DOCKET_HOME`, `DOCKET_PLUGIN`, `DOCKET_PLUGIN_ROOT` and
`DOCKET_PLUGIN_CONFIG` set. Arguments, stdio and exit status pass through.

```sh
docket hello-hooks ping
```

## Checklist

- [ ] `docket plugin validate` reports ok with no warnings.
- [ ] Hooks are idempotent and return quickly.
- [ ] Settings have schemas; secrets come from the environment, never YAML.
- [ ] Any server your hooks depend on runs under its own supervisor.
