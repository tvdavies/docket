# Plugins

Docket plugins are trusted local packages that declare extension points in a
strict `docket-plugin.yaml` manifest: hooks, statuses, scoped configuration and
a CLI command. Installing a plugin is equivalent to trusting its hooks and CLI
with your user account. There is no marketplace or signature verification. To
write one, start with [Authoring plugins](plugins/authoring.md).

Docket does not serve plugin UI, proxy plugin services or launch plugin
processes. Manifests written for earlier releases keep loading; see
[Legacy presentation metadata](#legacy-presentation-metadata).

## Install and enable

Install a linked development checkout or a GitHub/git source instance-wide:

```sh
docket plugin add ~/dev/example-plugin
docket plugin add owner/repo
docket plugin add owner/repo@v1.2.0
docket plugin list
```

Git installs live under `${XDG_DATA_HOME:-~/.local/share}/docket/plugins/`.
Without an explicit ref Docket selects the newest semantic-version tag, falling
back to the default branch tip. The selected ref and commit are recorded in the
machine registry. `docket plugin update [NAME]` clones a candidate, validates it
against every enabling workspace, and only then replaces the active directory.
A failed candidate leaves the previous version active. Linked plugins are
updated in their own checkout.

Enablement is committable workspace state:

```yaml
plugins:
  dispatch:
    config:
      server_root: /home/me/dev/dispatch
```

```sh
docket plugin enable dispatch --set server_root=/home/me/dev/dispatch
docket plugin config set dispatch server_root=/home/me/dev/dispatch
docket plugin disable dispatch
```

A normal enable seeds each missing plugin-handler cursor at the current event
log end, so historical events do not replay. `--from-start` explicitly opts into
replay. `--adopt-cursors` transfers strictly validated same-named legacy
checkpoints under both identity sets' locks, removes matching legacy handler
declarations, preserves effective status ordering, and publishes one config
change. It refuses an already active destination and retains source cursors and
private attempt receipts.

Retained source cursors are **not a rollback mechanism**: once plugin delivery
advances, restoring legacy config replays its acknowledged suffix. Ordinary
`plugin disable` only removes the declaration; it does not transfer progress.
For opt-in reverse handoff, inspection-only retries, runtime requirements and
side-effect limits, see [Plugin handler recovery](plugin-recovery.md). No binary
or whole-config restoration is a safe automatic fallback.

An enabled but missing/invalid plugin makes that workspace unavailable. This is
intentional: silently losing a status or wake handler is less safe than a
specific validation error.

## Manifest reference

```yaml
name: example
version: 1.0.0
description: Example Docket integration
requires:
  docket: ">=0.6.0"

handlers:
  notify:
    on: [task.moved]
    match: {data.to: done}
    lua: hooks/notify.lua
    delivery: service

statuses:
  - {name: merge, after: review}

config:
  instance:
    endpoint: {type: string, default: http://127.0.0.1:9000}
  workspace:
    checkout: {type: string, required: true}
  status:
    agent: {type: string, enum: [planner, implementer]}

cli:
  run: bin/docket-example
```

Unknown manifest fields are errors. Names use lowercase letters, numbers,
hyphens, and underscores. Handler `run`/`lua` and `cli.run` paths stay inside the
plugin root. The engine requirement is a `>=` semantic-version floor; development
builds satisfy floors.

### Handlers

Plugin handlers retain Docket's per-handler, log-ordered, at-least-once delivery
and failure isolation. Identity and cursor state are namespaced
`<plugin>/<handler>`. Scripts resolve relative to the plugin root while cwd and
`docket.path()` remain the workspace project root.

The process environment adds:

- `DOCKET_PLUGIN`
- `DOCKET_PLUGIN_ROOT`
- `DOCKET_PLUGIN_CONFIG`, JSON containing `config` and `status_config`

Lua handlers also receive `docket.plugin.name`, `.root`, `.config`,
`.status_config`, and `.path(...)`. Workspace-declared handlers do not receive a
`docket.plugin` table.

There is no ordering guarantee between handlers. Each independently observes
the event log in order, and one failure does not prevent another handler from
advancing.

### Statuses

Statuses are composed in workspace plugin declaration order. A contribution is
inserted after its anchor unless the workspace already pins that status, in
which case workspace placement wins. Missing anchors and duplicate
contributions are validation errors. `terminal: true` also contributes to the
terminal list.

### Scoped config

Each scope is a flat field map. Supported types are `string`, `number`,
`boolean`, `list`, and `map`; fields may declare `required`, `default`, `enum`,
`description`, and (at instance scope only) `secret`. Unknown keys, wrong types,
missing required values, and unknown status names fail validation. Instance
values are overlaid by workspace values; status values remain a per-lane map.
Secrets are documented by the schema but should be supplied through Docket's
environment file rather than stored in YAML.

Read and change settings with `docket plugin config`:

```sh
docket plugin config get example                     # schemas and stored values; --json
docket plugin config set example --scope instance endpoint=http://127.0.0.1:9000
docket plugin config set example checkout=/srv/app   # workspace scope (default)
docket plugin config set example --status review agent=implementer
docket plugin config set example --scope instance --file settings.json
```

`set` merges into one scope: keys you do not name are kept, and a list or map
replaces the stored value whole. Values are stored literally, so an empty
string, `0` or `false` is a real value. Each `KEY=VALUE` is parsed as YAML
(`3` is a number, `'"3"'` a string). The whole candidate is validated first —
against the schema and, for instance scope, against every workspace that
enables the plugin — and a rejected update leaves every file unchanged. `get`
resolves instance defaults but never prints secret fields; workspace and
status values are shown exactly as stored. `set` echoes only key names.
Secret fields are never stored: provide them in the environment of whatever
runs the hooks (`~/.config/docket/environment` for the systemd unit, or the
environment of `docket run` otherwise).

### CLI passthrough

`docket <name> <args...>` executes an installed plugin's `cli.run`. Builtin
commands always win. If no installed plugin resolves the name, Docket searches
for `docket-<name>` on `PATH`, matching git-style command discovery. Arguments,
stdio, exit status, and signals pass through; plugin/workspace environment is
injected when available.

## Hot reload

The event runner (`docket run`) watches each installed plugin's manifest with
fsnotify, and polls the registry every two seconds as a fallback. A manifest or
registry change restarts the affected workspace watchers, which recompose
their hooks and statuses. Handler identities do not include a generation, so
cursor checkpoints carry across restarts and events do not replay. A plugin
handler that first appears through hot reload is seeded at the current log
end. Edits limited to a manifest's legacy `ui` section cause no restart.

## Legacy presentation metadata

Earlier releases also served plugin UI in a web board. Those manifest sections
still parse and are still validated, so existing manifests keep loading
unchanged, but Docket no longer acts on them:

| Field | Now |
|---|---|
| `ui` (`dir`, `capabilities`, `widgets`, `panels`, `pages`, `reference_resolvers`, `cards`) | Parsed and validated; nothing is served. The `ui.dir` directory need not exist. |
| `service.url`, `healthz`, `auth` | Parsed and validated; nothing is proxied or probed. |
| `service.command`, `service.watch` | Parsed and validated; **not launched**. The runner and `plugin validate` warn that it must run under systemd, a container or another supervisor. |
| `config.*.options_from` | Parsed and validated; options are not fetched. |

Widget records that plugins published to the event log
(`task.widget_created`, `task.widget_finalised`) remain readable history: task
bundles keep their summaries and references in `widgets` and the activity
stream. Nothing produces new ones; record new outcomes as comments, references
or attachments.
