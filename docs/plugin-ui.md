# Workspace plugin widgets

Docket hosts trusted, **build-time** plugin modules in compact board contributions
and stable task-activity entries. An installed manifest does not load JavaScript.
The production catalogue is empty by default. A plugin-less workspace stays plain;
saved widget history remains readable when a plugin is disabled or absent.

## Public source and integration

`packages/plugin-ui/src` is the canonical `@docket/plugin-ui` contract. The web
application imports it directly; `web/src/registry/contracts.ts` only re-exports it.
[`plugin-ui.d.ts`](plugin-ui.d.ts) is a generated, self-contained declaration for
readers and external type checks. Do not maintain a second copy of these types.

```sh
bun web/scripts/generate-plugin-ui.ts
bun web/scripts/generate-plugin-ui.ts --check
cd web && bunx tsc -b
```

Public entry points are `.`, `./contracts`, `./legacy`, `./kit`,
`./custom-element`, and `./tokens.css`. The package is local/private; this task
publishes no registry package. JOB-0095 packages these exports and the runnable
examples without forking their implementation.

An integration author imports a reviewed module into
`web/src/registry/catalogue.ts`, keyed by manifest plugin name, and rebuilds the
board. There are no URL-based imports, remote scripts, marketplace or supported
hot replacement of module definitions. Fixture imports live only in
`web/tests/fixtures/widgets`; the old demo is a test-only v1 fixture.

```yaml
name: my-plugin
version: 1.0.0
ui:
  api_version: 2
  cards:
    - type: my-plugin/job
      title: Job progress
      locations: [board, activity]
```

`ui.api_version` and `locations` are additive manifest fields introduced by this
change. A release number has not been assigned. Older strict manifest decoders
reject these fields; remove them or disable the declaration before a binary
downgrade. Absent API version means v1. V2 requires explicit nonempty placements.
Unknown API versions remain metadata, but cannot execute a module.

Every selection starts with enabled workspace declarations, in workspace order
and then declaration order. The compiled name, type/ID, API and location must
match. `service_base` is used exactly when present and valid under the owning
`/plugins/<name>` prefix; the host never fabricates a missing service. The proxy
is instance-wide, so workspace UI isolation is **not** an authorization boundary.

## Context, snapshot and lifecycle

`DocketPluginUIV2` exports `apiVersion: 2`, `name`, optional `widgets` and
`referenceResolvers`. Each `WidgetModule` has a namespaced `type`, supported
`dataVersions`, a synchronous side-effect-free `present(snapshot, context)`,
`mount(body, context)`, and an optional read-only `detail` provider.

`mount` returns `WidgetInstance { update(snapshot, context, view), destroy() }`.
The first update includes the current accepted preview immediately. A widget
must not wait for disclosure or navigation to show activity.

| Input | Meaning |
|---|---|
| `WidgetContext.identity` | `{ workspace, taskId, widgetType, instanceId }`, supplied by the host, never inferred from URLs or DOM |
| `location` | `board` or `activity`; never arbitrary host placement |
| `serviceBase` | Optional validated same-origin prefix |
| `preferences` | Resolved light/dark, compact/comfortable, reduced motion |
| `signal` | Host aborts before cleanup; authored asynchronous work must honor it |
| `helpers.refreshTask()` | Instance-bound, coalesced task read; not task mutation |
| `helpers.hrefFor(reference)` | Safe URL or `null`; use real anchors, not click-only navigation |
| `helpers.requestDetail(listener, onRevoked)` | Activity-only, view-owned lease; no board detail capability |
| `WidgetSnapshot.task` | Current task, retaining supplied `active_sessions` and session audit context; attachments do not prove execution |
| `data` | `{version, revision, value: unknown}`; only supported versions reach authored presentation/body hooks |
| `freshness` | Connection, monotonic receipt/expiry, stale, awaiting rehydration, optional publisher activity time; separate from execution |
| `availability` | Available, missing service, disabled, missing module, unsupported, error |
| `fallback` | The latest durable generic record, not a transcript |
| `WidgetRenderState` | Reader-owned expansion/hold, displayed presentation/data and pending indication |

Bodies must render `view.displayed` and `view.data` in the reading region, not
bypass a hold by rendering the current `snapshot.data`. The latter remains
available for metadata. The wrapper always updates header, persistent notices
and keyed reference links. Expansion, selection and focus inside light or Shadow
DOM hold body content; **New activity** deliberately advances it. Terminal
transitions preserve a held reader until that action or collapse.

Task/data/freshness/preferences update in place. Identity, location, service
base, module/declaration replacement and disable retire the old body. Host
teardown aborts first, releases host resources, attempts destroy once and removes
the body in `finally`. A failed body stays retired until explicit Retry. Newer
supported data can replace an unsupported body. Late callbacks cannot revive an
old detail lease. Errors shown to users do not include private plugin payloads.

### V1 compatibility

The existing `DocketPluginUI`, `TaskCardModule`, `CardContext`, `CardInstance`
and `ReferenceResolverModule` remain exports. `adaptLegacyPluginUI` preserves
exactly `{workspace, task, pluginBase, refresh}` and **`update(task)`**. Untagged
modules are v1, never implicitly v2. No live data or detail contract is invented
for them. A missing service produces `pluginBase: ''`.

V1 cards get one synthetic contribution per task/type, not one per session.
Omitted placements mean board and activity. The former above-description panel
moves to a labelled contribution at task creation in activity; the ABI is kept,
not the accidental former layout. Legacy listeners can only be cleaned up by
the module's `destroy()`; the host cannot enforce arbitrary authored cleanup.

## Durable publisher API

Publishers, not widget bodies, call these JSON/origin-checked routes:

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

The server requires an existing task and matching enabled v2 card declaration.
Under the task lock it folds lifecycle events and appends only the allowed
transition. The ledger is the sole durable authority: no new task frontmatter,
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

Use `widgetLive(identity, payload, ttlMS)` to construct the existing workspace
live envelope. Its legacy `session` slot carries the generic `instanceId`; it
does not acquire agent semantics.

```json
{
  "kind": "my-plugin/job", "task": "TASK-1", "session": "job-7",
  "ttl_ms": 30000,
  "payload": {
    "widget_version": 1, "revision": 2,
    "data": {"version": 1, "value": {"text": "Checked the first inputs"}},
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

One workspace SSE connection feeds the router. Accepted data/high-water marks
are separate from current metadata, reader-held display, and transport receipt
sequence. TTL uses remaining server TTL and a monotonic browser receipt clock.
Expired content is last-known, never inferred failure or completion. Init/reset
and declaration changes invalidate transports and mark retained data stale;
owner publication restores freshness. Releasing a workspace evicts its cache
and timers. Terminal summaries recover without any live cache.

## Selected detail

A module can supply `DetailProvider.open({identity,serviceBase,signal}, sink)`.
It returns idempotent `close()` and `requestReset()`. The provider owns its
read-only GET/SSE/WebSocket protocol; core imports no Dispatch/ACP types.
`sink.status` accepts `ready`, `unavailable`, `not_found`, `error` separately.

`DetailFrame` contains `version:1`, identity, `dataVersion`, revision, `baseSeq`,
`throughSeq`, reset and bounded opaque value. The first connection/reconnect
requires a reset. Non-reset gaps pause application and request one reset. A
5-second reset timeout closes the lease. Regressing revision/sequence or wrong
identity is ignored. Receipt sequence advances even while display is held.

The active task view grants one lease across all widgets; selecting another
revokes the old one. No board card opens a detail transport. Release, abort,
reselection, stale data, config/reset, removal and failure fence late callbacks.
Outage keeps the expanded last-known reading view but releases transport.
Recovery does not silently reacquire it: collapse and expand explicitly. Full
history/windowing and domain-specific publishable projection belong to JOB-0050.

## Kit, custom elements and styling

The fixed DOM kit exports `status`, `metadata`, `OrderedActivity`, `disclosure`,
`safeText`, `formattedText`, `code`, `notice`, `reference`, `summary` and
`standardWidgetBody`. It is not a JSON layout language. Formatting supports
paragraphs, emphasis, inline/fenced code and validated links, never raw HTML or
remote images. Ordered rows have stable keys and remain in owner-supplied order.

`defineWidgetElement` and `customElementWidget` provide a real Shadow DOM adapter.
Names are injective: `docket-widget-${hex(UTF8(widgetType))}-v2`. Context arrives
before the first render. Same hooks/build identity registration is a no-op;
a different definition at an existing name fails with `duplicate_definition`.
Definitions cannot be unregistered. Disable removes instances; a code-changing
development reload requires a full page reload. Different API majors need
separate names/contracts, not replacement of a registered constructor.

Host wrapper CSS belongs to Docket. Module CSS stays in its body shadow root,
with no portals/global selectors. Import `@docket/plugin-ui/tokens.css` when
building an independent preview. Supported `--docket-widget-*` roles are:

- `surface`, `raised`, `sunken`, `text`, `muted`, `border`, `accent`, `focus`;
- positive/warning/danger/info `-fg` and `-bg` pairs;
- `font`, `mono`, `text-size`, `meta-size`, `line-height`;
- `space-1..4` (4/8/12/16px), `pad`, `radius`, focus width/offset and `motion`.

Wrapper anatomy is header → persistent notice → body → reader controls → safe
references/saved fallback. Theme, density and reduced-motion values update in
place. Phone controls are at least 44px, text wraps at 320px and focus rings
remain visible. Shadow DOM isolates conventional styles, **not trusted same-origin
JavaScript**. It cannot sandbox malicious code, stop infinite loops, constrain
arbitrary network access or guarantee visual compliance. Authored asynchronous
callbacks must handle their own errors and cancellation.

## Budgets and verification

| Area | Bound |
|---|---|
| Widget preview | 16 KiB UTF-8 JSON; generic live remains 64 KiB |
| Durable record | 8 KiB, label/status 120 characters, summary 2,000, ≤8 references |
| Board / preview / expanded | Action 120 characters; 4 rows/600 chars; 12 rows/1,600 chars |
| Disclosed output | 2,000 characters |
| Detail | One connection/task view, zero/board; 64 KiB/frame, 5-second reset timeout |
| Rendering | Coalesced body updates ≤1/second; urgent terminal/notice/availability changes next animation frame |
| Caches | 2,048 preview identities, 32 MiB encoded payloads; 512 resolver results/workspace |
| Mounts | Virtualized board, one selected instance/type; first 20 rich activity bodies then explicit Show more |
| References | 5-second enrichment timeout; 8 string-only metadata keys, 256 chars/value |
| Bundle | Existing 170 KiB total gzip; ≤30 KiB core increase and ≤30 KiB fixture increment |
| Performance | Recorded 100-task Chromium fixture at 1 Hz: host p95 ≤16ms and maximum ≤50ms |

Service and browser reject over-cap frames before presentation. The router keeps
one accepted value, while each mounted reader keeps displayed and pending bounded
values, never a frame queue. Cache capacity fails closed rather than dropping a
revision watermark and accepting older content. Stream resets/process restarts
still depend on owner monotonic revisions.

Go/RE2 selects the first enabled resolver and annotates references with
`resolver_id` and `resolver_generation`. The browser never approximates Go with
JavaScript regex. Generation changes discard enrichment and coalesce a fresh
board/task read. Cache keys include workspace, generation, resolver, service base,
task and full reference including title. Missing/failed/timed-out code falls back
to the original reference, not a later resolver. Unsafe links render text.

Run the isolated conformance suite:

```sh
go test ./...
cd web && bun test && bun run build
CHROMIUM_PATH=/usr/bin/chromium bun scripts/verify-widgets.ts
```

The two independent fixture plugins are a standard-kit progress widget and a
custom SVG chart with a keyboard-accessible value table. Their entry uses the
production App, host, store and public API with synthetic loopback APIs only.
[`plugin-ui-coverage.md`](plugin-ui-coverage.md) maps requirements to tests;
`widget-evidence/` holds runtime captures and measured results. NVDA/VoiceOver
is an explicit outstanding manual gate, not proven by browser checks.

## Downstream handoff

- JOB-0050 consumes these durable/live/detail contracts and shared kit; it owns
  Dispatch's filtered projection and full-session page, not another host.
- Settings work consumes ordered `plugins`, `api_version`, `locations`,
  `service_base`, and config-generation invalidation. No settings form rewrite
  or live service wiring is included here.
- JOB-0095 packages these exports and the progress/chart starters without a
  competing contract, loader, kit or element registry.

Generated settings endpoints remain unchanged: `GET /api/plugins`,
`PATCH /api/plugins/:plugin/config`, and the existing workspace/status config
PATCH routes accept `{ "values": { ... } }`. No widget helper grants these
mutation capabilities. This change authorizes no rollout or service restart.
