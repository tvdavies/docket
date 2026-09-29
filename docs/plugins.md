# Plugins

Docket plugins are trusted local packages that declare extension points in a
strict `docket-plugin.yaml` manifest. Installing a plugin is equivalent to
trusting its handlers, CLI and service with your user account. Plugin browser
UI is the exception: it runs in sandboxed iframes with only the capabilities it
declares (see [Plugin UI reference](plugins/ui.md)). There is no marketplace or
signature verification. To write one, start with
[Authoring plugins](plugins/authoring.md).

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

service:
  url: http://127.0.0.1:9000
  healthz: /healthz
  auth: none
  command: [bin/example-server, --port, "9000"]
  watch: ["server/**", "bin/example-server"]

cli:
  run: bin/docket-example

ui:
  dir: ui
  capabilities: [task.read, service.fetch, service.stream]
  widgets:
    - {type: example/session, title: Live session, entry: session.html}
  pages:
    - {id: sessions, title: Sessions, entry: sessions.html}
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

A `string` or `number` field may name a service path in `options_from` instead
of a fixed `enum`. The settings page fetches it through the plugin proxy
(`GET /plugins/<name><path>`) and offers the result as a choice list, refreshed
when the plugin's manifest or service state changes and on **Refresh options**:

```yaml
config:
  instance:
    model:
      type: string
      options_from: /options/models   # needs a service; absolute, no query
```

The response is a JSON array of values or `{"value": ..., "label": "..."}`
objects (at most 1000) whose values match the field type. Choices are a
convenience, not validation: a stored value the service no longer lists stays
selectable, and when the request fails the page falls back to a text input.
`options_from` cannot be combined with `enum` or `secret`.

### Service proxy

One optional loopback HTTP service is exposed at `/plugins/<name>/` while the
plugin is enabled in at least one registered workspace. Docket strips the prefix,
rewrites outbound `Host`, sets `X-Forwarded-Prefix`, and supports HTTP Upgrade /
WebSockets. Inbound `X-Docket-*` headers are removed so future board-edge identity
headers cannot be spoofed. `service.auth` is reserved and must be absent or
`none` in v1.

Authentication for remote board access remains a board-edge concern; plugin
services should continue binding loopback and trust only the local proxy.

### Supervised services

Without `service.command`, the plugin runs its service itself (for example as a
systemd unit) and Docket only proxies it. With `service.command`, `docket serve`
runs it while the plugin is enabled in at least one registered workspace:

- `command[0]` containing `/` is plugin-relative; otherwise it is looked up on
  `PATH`. The working directory is the plugin root, and the environment adds
  `DOCKET_PLUGIN`, `DOCKET_PLUGIN_ROOT`, `DOCKET_PLUGIN_CONFIG` (JSON with the
  resolved instance `config`), `DOCKET_PLUGIN_SERVICE_URL` and, when
  `service.url` has a port, `PORT`.
- The process runs in its own process group. Stopping sends `SIGTERM` to the
  group and `SIGKILL` five seconds later.
- A crash restarts it with exponential backoff (1 s doubling to 30 s, reset
  after ten seconds of uptime).
- With `service.healthz`, Docket probes `service.url + healthz` every ten
  seconds; three consecutive failures (connection errors or non-2xx) restart it.
- `service.watch` globs are plugin-relative; `*` stays within a path segment
  and `**` spans directories. A matching create, write, rename or delete
  restarts the process immediately. Dot-directories and `node_modules` are not
  watched.
- The process is replaced when the service section, the plugin path or its
  instance config changes, and stopped when the plugin is disabled everywhere,
  removed, or the Docket service exits.

Output goes to `${XDG_STATE_HOME:-~/.local/state}/docket/plugins/<name>/service.log`
(rotated to `.1` at 5 MiB; `DOCKET_STATE_DIR` overrides the base). Read it with
`docket plugin logs <name> [-f] [-n LINES]`. Each plugin on `GET /api/stream`
carries a `service` object with `state` (`starting`, `running`, `healthy`,
`unhealthy`, `backoff`), `pid`, `restarts`, `started_at`, `last_error` and
`log`.

### CLI passthrough

`docket <name> <args...>` executes an installed plugin's `cli.run`. Builtin
commands always win. If no installed plugin resolves the name, Docket searches
for `docket-<name>` on `PATH`, matching git-style command discovery. Arguments,
stdio, exit status, and signals pass through; plugin/workspace environment is
injected when available.

## Hot reload

The service watches each installed plugin's manifest and `ui.dir` with
fsnotify, and still polls the registry every two seconds as a fallback. What a
change does depends on what changed:

| Change | Effect |
|---|---|
| Files under `ui.dir` | New `ui_base`; open frames swap in place and keep their saved state |
| Only the manifest's `ui` section | Board config is republished; runtimes keep running |
| Anything else in the manifest, or the registry entry | Workspace runtimes restart and recompose contributions |

None of these restart the Docket service, and the proxy/API see the change
immediately. A supervised plugin service restarts only for its own `service`
section or `service.watch` matches (see [Supervised services](#supervised-services)). Handler identities do not include a generation, so cursor
checkpoints carry across runtime restarts and events do not replay. Plugin UI
is never part of the Docket build.

`GET /api/stream` is an instance-level SSE stream. Each `plugins` event carries
the full installed set — `name`, `version`, `manifest_hash`, `ui_hash`,
`ui_base`, `service` for supervised services and, for a manifest that fails to
load, `error` — first on connect and
again after every change. The settings page uses it to reload plugin schemas.

See [Plugin UI reference](plugins/ui.md) for frames and the bridge, and
[Plugin widgets](plugin-ui.md) for the widget ledger, resolvers and
generated settings.
