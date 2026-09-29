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

## 1. Start from the example

[`examples/plugins/hello-widget`](../../examples/plugins/hello-widget) is a
complete UI plugin with no service and no build step: a widget, a task panel and
a workspace page. Copy it and rename:

```sh
cp -r examples/plugins/hello-widget ~/dev/my-plugin
cd ~/dev/my-plugin
# edit docket-plugin.yaml: name, widget type prefix, titles
```

Rules the manifest validator enforces:

- `name` is lowercase letters, digits, `-` and `_`.
- Widget types are namespaced `<name>/<id>`.
- Every `entry` is a clean relative path inside `ui.dir`.
- Unknown fields are errors — check spelling rather than adding fields.

## 2. Install, enable and look

```sh
docket plugin add ~/dev/my-plugin        # links the checkout; edits apply in place
docket plugin enable my-plugin           # in the current workspace
docket plugin list
```

`enable` validates the manifest; an error names the field at fault. An enabled plugin that fails
validation makes its workspace unavailable, so fix or `disable` it promptly.

With the Docket service running (`docket serve`, default
`http://127.0.0.1:7463`):

- the page is at `/workspaces/<ws>/p/my-plugin/<page-id>` and in the header nav;
- panels are tabs in any task's detail view;
- widgets appear once a record is published (next step).

## 3. Publish a widget

Widgets are records in a task's ledger, published over HTTP by something you
control: a CLI command, a hook or your service. The hello example's
`bin/hello-widget` shows the minimal call:

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

To debug a frame, open the browser dev tools and select the plugin iframe's
context. You can also open `/plugin-ui/<name>/<hash>/<entry>` directly: it runs
with the same sandbox, but `connect()` rejects because there is no host.

## 6. Check

```sh
docket plugin enable my-plugin      # re-validates the manifest against this workspace
curl -s localhost:7463/api/workspaces/<ws>/board | jq '.plugins[] | {name, ui_base, widgets, pages, panels}'
```

In the browser, from the frame's console, `fetch("/api/workspaces")` must fail —
if it does not, the frame is not sandboxed.

## Checklist

- [ ] Manifest validates; widget types are `<name>/...`.
- [ ] `ui.capabilities` lists exactly the bridge methods you call.
- [ ] Frames use `connect()` and design tokens; no absolute URLs to other hosts.
- [ ] Widget records carry a useful `fallback`, so cards read well with the plugin disabled.
- [ ] Services authorise requests themselves; the proxy is not an auth boundary.
