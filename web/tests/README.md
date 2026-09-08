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

`check:ui` starts a temporary, loopback fixture server and runs the production bundle in a headless browser. It does not read or change a real Docket workspace. It checks variable card heights, hover stability, same-element dragging and placeholder size, moving between lanes, task routes (including reload/back/forward), chronological activity, status grouping/collapse, themed scrollbars, and mobile layout bounds. Screenshots are written to `/tmp/docket-*.png`.

The Bun test preload supplies a JSDOM environment for the existing React tests. Pointer tests cover cancellation, outside drops, unmount cleanup, click suppression, and keyboard opening. Grouping and activity-order tests operate independently of browser layout.

## Components and layout

Shared Button, Badge, Input and Textarea components in `src/components/ui/` are adapted from the shadcn/ui New York registry; the upstream MIT license is included. Theme tokens are mapped in `src/styles.css`. Existing Radix menus and dialogs remain in use for actions and task creation; task details themselves are not a dialog.

Board rows are measured with TanStack Virtual, rather than assigned a fixed card height. The dragged row stays mounted and retains its measured height while its actual card follows the pointer. Mouse users can drag the card; touch users can use its grip while the rest of the card allows vertical scrolling. Moving through the card menu or board keyboard shortcuts remains available without dragging.
