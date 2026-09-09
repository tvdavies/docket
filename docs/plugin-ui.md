# Plugin UI registry contract

Docket v1 keeps UI implementations in the board build while loading plugin
metadata from manifests at runtime. The stable TypeScript contract is committed
at [`plugin-ui.d.ts`](plugin-ui.d.ts). Board rebuilds should vendor or import
that file rather than redefining plugin types.

## Registry keys

Card `type` and resolver `id` values are namespaced as `<plugin>/<name>`. The
board matches build-time modules against the declarations returned in the
workspace board response:

```json
{
  "plugins": [
    {
      "name": "dispatch",
      "version": "1.0.0",
      "cards": [{"type": "dispatch/session", "title": "Live session"}],
      "reference_resolvers": [
        {
          "id": "dispatch/session",
          "pattern": "^https?://127\\.0\\.0\\.1:7464/sessions/",
          "kinds": ["session"]
        }
      ],
      "service_base": "/plugins/dispatch"
    }
  ]
}
```

Card implementations use framework-neutral `mount`, `update`, and `destroy`
methods. A card receives the current board task, workspace name, and same-origin
plugin service base. Calling `refresh()` asks the board host to fetch current
task data; cards do not own the board's cache.

Resolvers are considered in workspace plugin declaration order and manifest
order. The first declaration whose optional `kinds` and RE2-compatible
`pattern` match is used. A resolver should derive a useful synchronous label
from the URL and may enrich it asynchronously through `pluginBase`.

## Future dynamic loading

The contract deliberately contains no framework-specific component types. A
future Docket release may load an ES module from a plugin service and expect its
default export to satisfy `DocketPluginUI`; that change does not alter the card
or resolver lifecycle.

## Generated settings screens

`GET /api/plugins` returns each manifest's `instance`, `workspace`, and `status`
config schemas plus current instance values. The board places each schema on the
screen that owns its storage:

- instance fields: machine/instance settings;
- workspace fields: board settings;
- status fields: lane settings.

Writes use these endpoints with an `application/json` body shaped as
`{"values": {"key": "value"}}`:

- `PATCH /api/plugins/{plugin}/config`
- `PATCH /api/workspaces/{workspace}/plugins/{plugin}/config`
- `PATCH /api/workspaces/{workspace}/plugins/{plugin}/statuses/{status}`

The server merges supplied keys into the existing scope, validates the complete
candidate, and atomically writes the owning registry or workspace config. A
validation failure leaves the prior config active.
