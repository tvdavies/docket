# Plugins

Docket plugins are trusted local packages that declare extension points in a
strict `docket-plugin.yaml` manifest. Installing a plugin is equivalent to
trusting scripts from that directory or repository; v1 has no sandbox,
marketplace, signature verification, or remote runtime loading.

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
docket plugin disable dispatch
```

A normal enable seeds each missing plugin-handler cursor at the current event
log end, so historical events do not replay. `--from-start` explicitly opts into
replay. `--adopt-cursors` copies checkpointed same-named legacy cursors, removes
matching legacy handler declarations and contributed status pins, then publishes
one atomic config change. Legacy cursor files remain for rollback.

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

service:
  url: http://127.0.0.1:9000
  healthz: /healthz
  auth: none

cli:
  run: bin/docket-example

ui:
  cards:
    - {type: example/session, title: Live session}
  reference_resolvers:
    - id: example/session
      kinds: [session]
      pattern: "^https?://127\\.0\\.0\\.1:9000/sessions/"
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

### Service proxy

One optional loopback HTTP service is exposed at `/plugins/<name>/` while the
plugin is enabled in at least one registered workspace. Docket strips the prefix,
rewrites outbound `Host`, sets `X-Forwarded-Prefix`, and supports HTTP Upgrade /
WebSockets. Inbound `X-Docket-*` headers are removed so future board-edge identity
headers cannot be spoofed. `service.auth` is reserved and must be absent or
`none` in v1.

Authentication for remote board access remains a board-edge concern; plugin
services should continue binding loopback and trust only the local proxy.

### CLI passthrough

`docket <name> <args...>` executes an installed plugin's `cli.run`. Builtin
commands always win. If no installed plugin resolves the name, Docket searches
for `docket-<name>` on `PATH`, matching git-style command discovery. Arguments,
stdio, exit status, and signals pass through; plugin/workspace environment is
injected when available.

## Hot reload

The service fingerprints the machine registry and installed manifest files.
Registry or manifest changes restart only the in-process workspace runtimes,
recompose contributions, and are visible to the dynamic proxy/API without a
service process restart. Handler identities do not include a generation, so
existing cursor checkpoints carry across reloads and events do not replay.
Build-time card/resolver implementations still require a Docket board release;
manifest metadata reloads immediately.

See [Plugin UI registry contract](plugin-ui.md) for the stable card, resolver,
and generated-settings interfaces.
