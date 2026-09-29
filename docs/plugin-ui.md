# Plugin widgets

A widget is a typed, durable record in a task's ledger — "build job 7 is
running", "agent session finished" — published by a plugin and rendered by
Docket on the board and in the task's activity timeline. This page covers the
record and live-preview contracts. For how plugin UI is loaded, sandboxed and
talks to Docket, see the [Plugin UI reference](plugins/ui.md); for a walkthrough,
see [Authoring plugins](plugins/authoring.md).

## Declare a widget type

```yaml
ui:
  dir: ui
  widgets:
    - type: my-plugin/job          # must start with the plugin name
      title: Build job
      entry: job.html              # optional expanded view (sandboxed iframe)
      slots: [board, activity]     # default: both
```

Docket renders the card itself; no plugin code runs for the board or a
collapsed activity entry. The card shows, in order of preference:

1. `data.value.presentation` from the latest unexpired live preview (or
   `data.value` itself when it has `label` and `status.text`), unless the record
   is finalised;
2. the saved `fallback` from the ledger record.

A board card shows the highest-priority record per widget type
(`attention` → `error` → `active` → `history`, then newest), with a link to the
rest. A record whose plugin is disabled still renders from its saved fallback,
labelled as such.

### Presentation

```ts
interface WidgetPresentation {
  label: string;
  status: { text: string; tone: "neutral" | "positive" | "warning" | "danger" | "info" };
  priority: "attention" | "error" | "active" | "history";
  terminal: boolean;
  action?: string;
  notice?: { text: string; tone: WidgetTone };
  summary?: string;
  rows?: { key: string; order: number; role: "text" | "step" | "code" | "reference"; label: string; text?: string }[];
  references?: { kind: string; url: string; title: string }[];
  startedAt?: string;   // RFC 3339
  endedAt?: string;
}
```

The host bounds presentations before rendering (see [Budgets](#budgets)) and
renders only safe `http(s)` or in-app links.

## Durable publisher API

Publishers — a plugin's service, CLI or hook, never its frames — call these
JSON, origin-checked routes:

- `POST /api/workspaces/:workspace/tasks/:task/widgets/create`
- `POST /api/workspaces/:workspace/tasks/:task/widgets/finalise`

Both receive a complete `WidgetRecordV1`:

```json
{
  "version": 1,
  "widget_type": "my-plugin/job",
  "instance_id": "job-7",
  "task_id": "TASK-1",
  "created_at": "2026-09-10T10:00:00Z",
  "revision": 1,
  "phase": "created",
  "fallback": {
    "label": "Build job",
    "status_label": "Processing",
    "priority": "active",
    "summary": "Checking synthetic inputs",
    "references": []
  }
}
```

Finalisation uses `phase: "finalised"`, a higher revision and the same immutable
creation time. Priorities are generic `attention`, `error`, `active`, `history`,
not execution-state enums. Unknown times/metrics remain absent. References have
`kind`, `title`, `url`; there is no opaque body/transcript in a saved record.

The server requires an existing task and a matching `ui.widgets` declaration
from a plugin enabled in the workspace.
Under the task lock it folds lifecycle events and appends only the allowed
transition. The ledger is the sole durable authority: there is no task frontmatter,
second file, correction API or recovery daemon.

| Operation | Response / effect |
|---|---|
| First create | 201, `{record,cursor}`, one `task.widget_created` event |
| Exact create/finalise retry | 200, current record, no event; old create retry after finalise is also idempotent |
| First higher-revision finalise | 200, `{record,cursor}`, one `task.widget_finalised` event |
| Changed retry, stale finalise, changed creation time, terminal rewrite | 409 `widget_conflict` with current revision |
| Finalise/live before create | 404 `widget_not_created` |
| Disabled or missing declaration | 403 `widget_not_enabled` |
| Invalid record/identity/phase/preview | 400, stable error code |
| Preview after finalisation | 409 `widget_finalised` |
| Capacity reached | 429; publisher backs off, never assumes durability |

Mutation cursors use the existing event cursor format. Exact no-op retries do
not fabricate a new cursor. Append failures never return successful receipts.
Lost responses and process restarts recover by retrying the complete record.
There is no claim to repair arbitrary corrupt/truncated ledgers here.

Task bundles expose `widgets`, `widget_revision` and one typed `kind: "widget"`
activity entry at creation time, with `data.record` holding the latest record.
The raw creation/finalisation events stay in the ledger but are not duplicated
in rendered activity. Board/SSE summaries expose `widget_summaries` and
`widget_revision` without full body text. Widget revisions update independently
of `task.updated_at`; lifecycle refreshes do not replace unsaved drafts.

## Ephemeral previews and owner recovery

Previews use the workspace live envelope (`widgetLive(identity, payload, ttlMS)`
in the SDK builds one). Its `session` field carries the widget's `instance_id`.

```json
{
  "kind": "my-plugin/job", "task": "TASK-1", "session": "job-7",
  "ttl_ms": 30000,
  "payload": {
    "widget_version": 1, "revision": 2,
    "data": {"version": 1, "value": {"presentation": {
      "label": "Build job", "status": {"text": "Checking", "tone": "info"},
      "priority": "active", "terminal": false,
      "summary": "Checked the first inputs"}}},
    "last_activity_at": "2026-09-10T10:00:01Z"
  }
}
```

POST this to `/api/workspaces/:workspace/live`. Revisions are positive safe
integers, newer than creation. Previews are full replacements: skipped revisions
are safe. Identical same-revision heartbeats renew freshness; changed same or
older revisions conflict and cannot renew TTL. Finalisation fences every later
live write. **Intermediate frames never append ledger events.**

One authoritative owner per identity must preserve monotonic revisions across
its own restart. Publish changed previews at most once per second, heartbeat
every 10 seconds, TTL 30 seconds. Owners republish a complete snapshot after
recovery; there is no host control endpoint. The lifecycle revision alone cannot
recover lost ephemeral high-water revisions. A receipt is not durable progress
storage. Owners redact private reasoning, raw prompts, secrets and raw tool
arguments/results before publication; the host cannot infer privacy from opaque
values.

## Budgets

| Area | Bound |
|---|---|
| Widget preview | 16 KiB UTF-8 JSON; generic live payloads remain 64 KiB |
| Durable record | 8 KiB; label/status 120 characters; summary 2,000; ≤ 8 references |
| Presentation | Action 120 characters; collapsed 4 rows / 600 characters; expanded 12 rows / 1,600 |
| Preview cache | 2,048 identities, 32 MiB encoded payloads per browser |
| References | 5-second resolver timeout; 8 string-only metadata keys, 256 characters each; 512 cached results per workspace |

The service and the browser reject over-cap frames before presentation.

## Reference resolvers

`ui.reference_resolvers` match task references by kind and an RE2 `pattern`.
Go selects the first enabled resolver and annotates the reference with
`resolver_id` and `resolver_generation`. When a resolver declares an `endpoint`,
the browser POSTs `{workspace, task_id, reference}` to
`/plugins/<name><endpoint>` and expects `{label, icon?, meta?, href?}`. A missing
endpoint, stale generation, timeout, error or malformed answer falls back to the
reference's own title. Unsafe links render as text.

## Settings endpoints

Generated settings endpoints remain unchanged: `GET /api/plugins`,
`PATCH /api/plugins/:plugin/config`, and the existing workspace/status config
PATCH routes accept `{ "values": { ... } }`. Plugin frames cannot call
them: the bridge exposes no settings methods.

## Generated settings forms

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
