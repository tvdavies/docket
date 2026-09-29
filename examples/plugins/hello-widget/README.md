# hello-widget

The smallest useful Docket plugin UI. It has no service and no build step. It shows:

- **A task widget** (`hello-widget/greeting`). Docket renders the collapsed card itself from the ledger record. *Expand* in the task's activity opens `ui/widget.html` in a sandboxed iframe. Its click counter is kept by the host, so it survives hot reloads.
- **A task panel** (`ui/panel.html`). It reads the task via the `task.read` capability.
- **A workspace page** (`ui/page.html`), at `/workspaces/<ws>/p/hello-widget/overview`. It demonstrates that the frame cannot reach Docket's API directly.

```sh
docket plugin add ./examples/plugins/hello-widget
docket plugin enable hello-widget
docket hello-widget TASK-0001          # publish a widget record on a task
```

Edits under `ui/` change the plugin's UI hash; reload the page to see them. See
[docs/plugins/ui.md](../../../docs/plugins/ui.md).
