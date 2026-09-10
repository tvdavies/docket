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

### Open the generated forms

The React board's **Plugin settings** header link opens `/settings/plugins`.
**Board settings** opens `/workspaces/{workspace}/settings/plugins`; the lane
selector includes every composed status, including hidden and empty lanes.
Each configured lane's header also links directly to
`/workspaces/{workspace}/settings/plugins/statuses/{status}`. Unknown task-status
fallback columns are not configurable lanes. Instance settings do not require a
working board stream or any registered workspace.

Forms render supported manifest schemas regardless of plugin version. Strings,
numbers, booleans and typed enums use labelled controls; lists and maps use JSON
editors and replace the entire field value. Required means present, so an empty
string is valid. False, zero, `[]` and `{}` are values, not deletion. An absent
local field has a **Set value** action. **Discard changes** only removes unsaved
edits; there is no reset/delete endpoint or implied multi-scope transaction.

Instance reads contain default-resolved values. The UI labels them "stored or
default" because the API provides no raw-instance provenance. Workspace and lane
reads contain stored keys only. Each scope applies its own defaults and required
checks; resolved workspace keys (including workspace defaults) override same-key
instance values. An instance value does not satisfy a required workspace field.
Lane defaults apply independently and do not cascade from instance/board config.

Saving sends only edited non-secret keys after refetching the catalogue and
rechecking the owning board/lane. Observable schema, version or baseline changes
require review without overwriting drafts. The API has no CAS token, so a
same-field concurrent write can still race that check. A confirmed save refetches
canonical values; if that read fails, retry is read-only. Lost/malformed save
responses report an uncertain outcome and require read-back before retrying.

Secret fields are environment instructions, not inputs. Their values/defaults
are never displayed or written by these forms. The editable **Acting as** value
is attribution, not access control; these endpoints do not create task audit
events. Existing same-origin safeguards remain unchanged. Saving configuration
does not verify optional plugin-service health, and lane forms never edit a
Dispatch pipeline file.

A missing/invalid plugin, malformed catalogue or unavailable board is not an
empty list. Cached drafts remain read-only until the operator repairs the
configuration and reloads it. Unsupported schema shapes block the affected form.

For an isolated real-API preview, tests and fixture screenshots, see
[UI checks](../web/tests/README.md#plugin-settings-real-api-checks) and the
[requirements coverage map](plugin-settings-validation.md).
