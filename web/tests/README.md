# UI checks

From `web/`:

```sh
bun install
bun test
bun run build
bunx playwright install chromium
bun run check:ui
```

To use an existing Chromium installation instead:

```sh
CHROMIUM_PATH=/usr/bin/chromium bun run check:ui
```

`check:ui` starts a temporary, loopback fixture server and runs the production bundle in a headless browser. It does not read or change a real Docket workspace. It checks variable card heights, hover stability, same-element dragging and placeholder size, moving between lanes, task routes (including reload/back/forward), chronological activity, status grouping/collapse, themed scrollbars, and mobile layout bounds. Filter checks cover persistent multi-selection, outside-click and Escape dismissal, focus restoration, keyboard toggling, reset (including toolbar search), and mobile use with no matching tasks. Screenshots are written to `/tmp/docket-*.png`.

The Bun test preload supplies a JSDOM environment for the existing React tests. Pointer tests cover cancellation, outside drops, unmount cleanup, click suppression, and keyboard opening. Grouping and activity-order tests operate independently of browser layout.

## Plugin settings real-API checks

From `web/`, with Go, Bun and Chromium available:

```sh
bun install --frozen-lockfile
bun run build
CHROMIUM_PATH=/usr/bin/chromium bun run check:settings
CHROMIUM_PATH=/usr/bin/chromium bun run preview:settings
```

Omit `CHROMIUM_PATH` to use Playwright's installed Chromium. Preview does not
launch a browser; open the printed random loopback URL and use Ctrl+C to stop it.
The preview starts with an instance schema, two boards (`alpha`/`beta`), and all
composed lanes. Use the header, Board settings link and lane selector to inspect
each scope. It runs no automated test mutations.

Both modes build a temporary Docket binary using the production `web/dist` and
share `scripts/settings-sandbox.ts`. Before starting any CLI, the factory creates
and validates a fresh temporary root and overrides `DOCKET_HOME`, `DOCKET_CONFIG`,
`XDG_CONFIG_HOME`, `XDG_DATA_HOME` and `XDG_STATE_HOME`. It clears inherited
Docket/Dispatch task/session pointers, copies only the harmless fixture manifest,
initialises two temporary boards and validates them. The plugin has no handlers,
CLI or custom UI. Its optional service is deliberately unavailable; configuration
saves must not depend on it.

The owned service runs `serve --all --listen 127.0.0.1:0`. Neither mode accepts an
external URL, reads the live registry, installs a service, runs Dispatch handlers
or edits a live board/pipeline. Exit cleans up the owned service and temporary
files. Browser/transport faults are bounded test interceptions; normal reads,
writes and persistence assertions use the real Go API and actual temporary
config files.

`check:settings` covers all kinds and scopes, typed enums, raw invalid JSON,
required/type/enum/unknown-key rejection with unchanged file bytes, persistence
across reload, two-board/lane isolation, stale version detection, uncertain saves,
confirmed-save/read-back failure, cancelled navigation, pending cross-workspace
responses, keyboard operation, mobile bounds and degraded/empty states. The
secret environment sentinel must not appear in DOM, requests, browser storage
or screenshots.

Screenshots and `validation.txt` are written to `/tmp/docket-settings-evidence/`.
Set `SETTINGS_EVIDENCE_DIR` to choose a different artifact directory. Screenshots
include instance/board/lane desktop light views, mobile dark views and validation
errors. They contain only synthetic fixture data. On failure, `failure.png` and
`failure.txt` retain the browser state. Remove old failure artifacts before
publishing a successful run's evidence.

The associated Bun tests cover schema interpretation, client boundary checks,
raw drafts, duplicate saves, malformed schema refresh, required presence,
post-rejection focus and late responses after unmount. Go tests in
`internal/service/plugin_settings_test.go` use the same fixture manifest.
[Implementation coverage and limits](../../docs/plugin-settings-validation.md)
map the requirements to these checks.

## Components and layout

Shared Button, Badge, Input, Textarea, Popover and Checkbox components in `src/components/ui/` are adapted from the shadcn/ui New York registry; the upstream MIT license is included. Theme tokens are mapped in `src/styles.css`. Existing Radix menus and dialogs remain in use for actions and task creation; task details themselves are not a dialog.

Board rows are measured with TanStack Virtual, rather than assigned a fixed card height. The dragged row stays mounted and retains its measured height while its actual card follows the pointer. Mouse users can drag the card; touch users can use its grip while the rest of the card allows vertical scrolling. Moving through the card menu or board keyboard shortcuts remains available without dragging.
