# Authoring plugins

This guide is for anyone — human or coding agent — extending a running Docket.
A plugin is a directory with a `docket-plugin.yaml` manifest. It can add Lua
handlers, statuses, config, a proxied HTTP service, a CLI subcommand and
browser UI. Pick only the parts you need.

Reference material:

- [Plugins](../plugins.md) — manifest, install/enable, handlers, config, proxy.
- [Plugin UI reference](ui.md) — sandbox, bridge API, limits, theming.
- [Widgets](../plugin-ui.md) — the widget ledger and live previews.
- [Lua hooks](../lua-hooks.md) — the handler runtime.
- [`plugin-sdk.d.ts`](../plugin-sdk.d.ts) — generated types for the UI SDK.

## 1. Scaffold

```sh
docket plugin new my-plugin              # widget, task panel and workspace page
docket plugin new my-plugin --widget --service   # pick views; --service adds a Node server
```

This writes `./my-plugin` (`--dir` to choose) with a valid manifest, frames
that use the SDK, a CLI command that publishes a widget and, with `--service`,
a supervised server exposing `/healthz`, `/hello` and an `options_from` source
for its settings. The files are the starting point: rename, delete and edit
freely.

[`examples/plugins/hello-widget`](../../examples/plugins/hello-widget) is a
finished example of the same shape without a service.

Rules the manifest validator enforces:

- `name` is lowercase letters, digits, `-` and `_`.
- Widget types are namespaced `<name>/<id>`.
- Every `entry` is a clean relative path inside `ui.dir`.
- Unknown fields are errors — check spelling rather than adding fields.

## 2. Run it with `plugin dev`

From the workspace you want the plugin in:

```sh
docket plugin dev ./my-plugin
```

`plugin dev` links the directory (edits apply in place), enables it in the
current workspace, starts the Docket service if nothing is listening, and then
stays in the foreground printing one line per event: manifest validation on
every save, UI reloads, service starts, restarts and failures, and the
service's own output prefixed `[service]`. Leave it running in a terminal or a
background job while you edit; stop it with Ctrl-C (the plugin stays enabled).

The same steps by hand:

```sh
docket plugin add ./my-plugin            # links the checkout
docket plugin enable my-plugin           # validates, in the current workspace
docket serve                             # if the service is not already running
```

An enabled plugin that fails validation makes its workspace unavailable, so
fix or `disable` it promptly.

With the service running (default `http://127.0.0.1:7463`):

- the page is at `/workspaces/<ws>/p/my-plugin/<page-id>` and in the header nav;
- panels are tabs in any task's detail view;
- widgets appear once a record is published (next step).

## 3. Publish a widget

Widgets are records in a task's ledger, published over HTTP by something you
control: a CLI command, a hook or your service. The scaffold's
`bin/my-plugin` shows the minimal call:

```sh
docket my-plugin TASK-0001
```

Docket renders the board and collapsed activity cards itself from the record's
`fallback` (label, status, priority, summary, references). To show progress
while work runs, post live previews whose `data.value.presentation` is a
`WidgetPresentation`. See [Widgets](../plugin-ui.md) for the record lifecycle,
revision rules and the preview envelope.

Give the widget an `entry` to let users **Expand** it in the activity timeline
into your own iframe.

## 4. Write the frame

A frame is plain HTML. Import the SDK from Docket itself:

```html
<!doctype html>
<meta charset="utf-8">
<link rel="stylesheet" href="style.css">
<div id="app">Connecting…</div>
<script type="module">
  import { connect } from "/plugin-sdk/v1/client.js";
  const docket = await connect();
  const render = (ctx) => {
    document.getElementById("app").textContent =
      `${ctx.view.kind} ${ctx.view.id} in ${ctx.workspace}` + (ctx.taskId ? ` for ${ctx.taskId}` : "");
  };
  docket.onUpdate(render);
  render(docket.context);
</script>
```

Things to know:

- **No network.** `fetch`, WebSockets and EventSource are blocked by CSP. Call
  your service with `docket.fetch`/`docket.json`/`docket.stream`, which need
  `service.fetch`/`service.stream` and a `service` in the manifest.
- **No storage.** The frame has an opaque origin, so `localStorage` and cookies
  are unavailable. Use `docket.setState(value)`; it comes back as
  `context.state` after reloads.
- **Ask for capabilities.** Add the methods you call to `ui.capabilities`.
  Undeclared calls fail with `capability_denied`.
- **Size and theme are automatic.** `connect()` applies `--docket-widget-*`
  tokens and reports the document height. Use the tokens in your CSS.
- **Bundlers are fine.** Build React, Svelte or anything else into `ui.dir`
  (for example `ui: {dir: ui/dist}`) and import `connect` from
  `@docket/plugin-sdk`, or keep importing `/plugin-sdk/v1/client.js` as an
  external. Keep asset URLs relative.

## 5. Iterate

Edit files under `ui.dir` and save. The service watches the directory, so
open frames swap to the new files in place and keep the state saved with
`setState`; there is no page reload, Docket rebuild or restart. Each edit
changes the plugin's UI hash, so the browser can never serve a stale mix of
files. A bundler writing into `ui.dir` in watch mode works the same way.

Manifest changes apply as soon as the file is saved. Edits confined to the `ui`
section (new views, titles, capabilities) republish board config without
touching handlers; any other change (handlers, statuses, config) reloads the
workspace runtimes, still without restarting the service.

To follow reloads from a script or agent, watch the instance stream:

```sh
curl -sN localhost:7463/api/stream   # event: plugins, one per change
```

A manifest that stops validating shows up there with an `error`.

### Services

Give the service a `command` and Docket runs it for you while the plugin is
enabled in any workspace, restarting it when watched files change:

```yaml
service:
  url: http://127.0.0.1:9000
  healthz: /healthz
  command: [node, server/index.mjs]   # cwd is the plugin root
  watch: ["server/**/*.mjs", "package.json"]
```

Save a file under `server/` and the process is restarted within a second.
Follow its output, including Docket's own start, restart and health lines,
with:

```sh
docket plugin logs my-plugin -f
```

The service's current state (`running`, `healthy`, `unhealthy`, `backoff`)
and restart count are on `/api/stream` under each plugin's `service`.

To debug a frame, open the browser dev tools and select the plugin iframe's
context. You can also open `/plugin-ui/<name>/<hash>/<entry>` directly: it runs
with the same sandbox, but `connect()` rejects because there is no host.

### Settings with dynamic choices

Config fields can take their choices from the service with `options_from` (see
[Plugins](../plugins.md#scoped-config)); the settings page fetches them through
the proxy and refetches when the service restarts.

## 6. Check

```sh
docket plugin validate ./my-plugin  # manifest, plus handler, CLI, service and UI files exist
docket plugin enable my-plugin      # re-validates the manifest against this workspace
curl -s localhost:7463/api/workspaces/<ws>/board | jq '.plugins[] | {name, ui_base, widgets, pages, panels}'
```

In the browser, from the frame's console, `fetch("/api/workspaces")` must fail —
if it does not, the frame is not sandboxed.

## Checklist

- [ ] `docket plugin validate` reports ok; widget types are `<name>/...`.
- [ ] `ui.capabilities` lists exactly the bridge methods you call.
- [ ] Frames use `connect()` and design tokens; no absolute URLs to other hosts.
- [ ] Widget records carry a useful `fallback`, so cards read well with the plugin disabled.
- [ ] Services authorise requests themselves; the proxy is not an auth boundary.
- [ ] A supervised service binds the port from `service.url` (also in `$PORT`) and exits on `SIGTERM`.
