# Plugin UI reference

Plugins contribute browser UI as static files that Docket serves from the
plugin's `ui.dir` and loads into sandboxed iframes. Nothing is compiled into
Docket's web build, and plugin code never runs in the Docket page. Editing a
file in `ui.dir` produces a new asset generation; open views pick it up without
a Docket release.

For a walkthrough, see [Authoring plugins](authoring.md). For the widget ledger
and live previews that feed widget cards, see [Widgets](../plugin-ui.md).

## Manifest

```yaml
ui:
  dir: ui                                  # static assets, relative to the plugin root
  capabilities: [task.read, task.comment]  # bridge methods the frames may call
  widgets:
    - type: my-plugin/job                  # namespaced <plugin>/<id>
      title: Build job
      entry: job.html                      # optional; opened when expanded in activity
      slots: [board, activity]             # default: both
  panels:                                  # tabs in task detail
    - {id: notes, title: Notes, entry: panel.html}
  pages:                                   # /workspaces/:ws/p/<plugin>/<id>
    - {id: overview, title: Overview, entry: page.html}
  reference_resolvers:
    - id: my-plugin/job
      kinds: [job]
      pattern: "^https://ci\\.example\\.com/jobs/"
      endpoint: /resolve                   # optional service path
```

- `entry` paths are clean relative paths inside `ui.dir` (no `..`, query or
  fragment). Any `entry` requires `ui.dir`.
- `ui.dir` may hold at most 4,096 files.
- `capabilities` must come from the list below; unknown or duplicate values are
  manifest errors.
- `ui.cards` and `ui.api_version` are deprecated. Cards with
  `api_version: 2` are still read as presentation-only widgets.

## Where UI appears

| Slot | Rendering | Plugin code runs? |
|---|---|---|
| Board card (`board`) | Declarative `WidgetPresentation`, top widget per type | No |
| Activity timeline (`activity`) | Declarative card; **Expand** mounts `entry` | Only when expanded |
| Task panel | Tab below the task; only the selected tab is mounted | Yes |
| Workspace page | Full page with a nav link | Yes |

Board cards never mount iframes, so a busy board costs nothing per card. The
collapsed card shows `data.value.presentation` from the latest live preview
(or `data.value` itself if it is presentation-shaped), otherwise the saved
fallback from the ledger record.

## Isolation

Assets are served at `/plugin-ui/<plugin>/<hash>/<path>` with:

```
Content-Security-Policy: sandbox allow-scripts allow-forms; default-src 'none';
  script-src 'self' 'unsafe-inline' blob:; style-src 'self' 'unsafe-inline';
  img-src 'self' data: blob:; font-src 'self' data:; media-src 'self' data: blob:;
  connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'
```

The host iframe uses `sandbox="allow-scripts allow-forms"` without
`allow-same-origin`. As a result a plugin frame:

- has an opaque origin, even when its URL is opened directly in a tab;
- cannot read Docket cookies, `localStorage` or the Docket DOM;
- cannot `fetch`, `XMLHttpRequest`, `WebSocket` or `EventSource` anything —
  including Docket's API and its own service;
- cannot load scripts from other origins, submit forms or open popups.

Everything goes through the host bridge, which enforces the plugin's declared
capabilities. Inline `<script>` and `<style>` are allowed, so a single HTML file
is a complete plugin view.

## Asset generations and hot reload

The `<hash>` fingerprints `ui.dir` (paths, sizes, modification times and the
manifest version). The board metadata's `ui_base` carries the current hash.

- A request with the current hash is served `immutable`; a stale hash serves the
  current bytes with `no-store`, so a reloading frame never mixes generations.
- The service watches `ui.dir` and pushes the new `ui_base` on the workspace
  stream within a fraction of a second of an edit. Workspace runtimes are not
  restarted and live widget previews are kept.
- When `ui_base` changes, the host swaps the iframe `src` in place and re-sends
  `init` with the state the frame last saved via `setState`. Saved frame state
  is kept for the life of the page.
- `GET /api/stream` publishes each plugin's `ui_hash` and `manifest_hash` for
  tools that want to follow changes without opening a workspace stream.

## The SDK client

Docket serves a prebuilt client at `/plugin-sdk/v1/client.js`, so a plugin needs
no build step:

```html
<script type="module">
  import { connect } from "/plugin-sdk/v1/client.js";
  const docket = await connect();
  document.body.textContent = `Hello from ${docket.context.plugin}`;
</script>
```

Bundled plugins can import the same API from `@docket/plugin-sdk` (the
`packages/plugin-sdk` workspace package). Types for everything below are in
[`plugin-sdk.d.ts`](../plugin-sdk.d.ts).

`connect(options?)` resolves once the host sends `init`. By default it:

- applies the theme tokens to `document.documentElement` (`applyTheme: false`
  opts out) and sets `data-theme`;
- reports the document height with a `ResizeObserver` so the host sizes the
  frame (`autoResize: false` opts out).

### Context

`docket.context` is replaced on every host update; subscribe with
`docket.onUpdate(listener)` (returns an unsubscribe function).

| Field | Meaning |
|---|---|
| `plugin`, `workspace` | Identity of the view |
| `view` | `{kind: "widget" \| "panel" \| "page", id}` |
| `taskId` | Set for widget and panel views |
| `task` | The board task, only when the plugin holds `task.read` |
| `widget` | For widget views: `identity`, `record`, live `data`, `freshness`, `presentation` |
| `capabilities` | What the plugin declared |
| `preferences` | `theme`, `density`, `reducedMotion` |
| `theme` | `{scheme, tokens}`; tokens are `--docket-widget-*` CSS variables |
| `state` | Last value saved with `setState`, or `null` |

### Methods

| Method | Capability | Notes |
|---|---|---|
| `readTask(taskId?)` | `task.read` | Full task detail; defaults to the view's task |
| `comment(text, taskId?)` | `task.comment` | Adds a task comment |
| `moveTask(status, taskId?)` | `task.move` | Changes the task's status |
| `fetch(path, {method, headers, body}?)` | `service.fetch` | Request to the plugin's own service; returns `{status, headers, body}` |
| `json(path, init?)` | `service.fetch` | `fetch` + `JSON.parse`; throws on non-2xx |
| `stream(path, {message, open?, error?})` | `service.stream` | Server-sent events from the plugin's service; returns `{close()}` |
| `navigate(path)` | — | Must stay under `/workspaces/<this workspace>` |
| `openExternal(url)` | `open.external` | `http(s)` only, no credentials in the URL; the user confirms |
| `setState(value)` | — | Host-held JSON (≤ 16 KiB) returned as `context.state` after reloads |
| `call(method, params)` | — | Raw request for any of the above |

Failures reject with `BridgeError` whose `code` is one of `capability_denied`,
`invalid_request`, `unknown_method`, `too_many_requests`, `timeout`,
`request_failed` or `cancelled`.

### Service requests

`fetch` and `stream` paths are relative to the plugin's service proxy,
`/plugins/<name>/`. The host rejects paths that are not absolute, contain `.`
or `..` segments, encoded separators, backslashes, whitespace or control
characters. Requests are sent with `credentials: "omit"` and
`redirect: "error"`. Only the `accept`, `content-type` and `last-event-id`
request headers are forwarded, and only `content-type`, `etag` and
`last-modified` come back. Methods are `GET`, `HEAD`, `POST`, `PUT`, `PATCH` and
`DELETE`; `GET`/`HEAD` requests cannot have a body. A plugin without a `service` gets
`capability_denied`.

Plugin services must not trust the browser: any page can reach the proxy
directly. Authorise on the service side.

### Limits

| Limit | Value |
|---|---|
| Concurrent requests per frame | 32 |
| Open streams per frame | 4 |
| Request timeout | 30 s |
| Request body | 1 MiB |
| Response body | 4 MiB |
| Saved state | 16 KiB JSON per view |
| Frame height | 32 px up to 640 (widget), 1200 (panel), 4000 (page) |

## Protocol

The client is a thin wrapper over a postMessage protocol (`PROTOCOL_VERSION`
1). Every message carries `docket: 1`. Both sides authenticate by
`event.source`, never by origin (the frame's origin is `null`).

- Frame → host: `ready`, `resize {height}`, `request {id, method, params}`.
- Host → frame: `init {context}`, `update {context}`,
  `response {id, ok, value | error, message}`,
  `stream {stream, event: open | message | error | closed, name?, data?}`.

The host holds updates until the frame sends `ready`, and re-sends `init` after
each reload.

## Theming

Theme tokens mirror [`tokens.css`](../../packages/plugin-sdk/src/tokens.css).
Use them instead of fixed colours so frames follow light/dark and density:

```css
body {
  font: var(--docket-widget-text-size) / var(--docket-widget-line-height) var(--docket-widget-font);
  color: var(--docket-widget-text);
  background: transparent;
}
```

Roles: `surface`, `raised`, `sunken`, `text`, `muted`, `border`, `accent`,
`focus`; `positive`/`warning`/`danger`/`info` with `-fg` and `-bg`; `font`,
`mono`, `text-size`, `meta-size`, `line-height`; `space-1..4`, `pad`, `radius`;
focus width/offset and `motion`.
