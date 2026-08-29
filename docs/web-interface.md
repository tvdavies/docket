# Web interface

Docket serves a lightweight React task board at:

```text
http://127.0.0.1:7463
```

Start it with `docket serve --all`, or manage the background user service with
`docket service`. The previous board remains available for one release at
`/classic`; `/next` is an alias of the current board for old preview links.

## Live event-stream architecture

The browser keeps one active workspace in memory and folds a same-origin SSE
stream rather than polling whole boards. `GET /api/workspaces/{name}/stream`
sends:

- `init` — a full task-card snapshot, workspace status/terminal/label config,
  and an opaque cursor;
- `patch` — one durable ledger event enriched with the complete current task
  summary; applying it is an idempotent keyed upsert;
- `config` — updated statuses, terminal lanes, and labels after config reload;
- `live` — bounded ephemeral data with a TTL, held only in service memory; and
- heartbeat comments roughly every 25 seconds.

SSE `id` values are opaque physical event-log cursors. EventSource reconnects
with `Last-Event-ID`; the server replays patches after a valid cursor. A stale,
truncated, replaced, or previous-process cursor receives a new `init` snapshot.
Slow clients are disconnected rather than buffered without bound and recover by
reconnecting.

Task mutations stay on the REST API. Successful mutation bundles add a `cursor`
field identifying the exact event group committed under the ledger append lock.
The browser renders an optimistic overlay immediately and retires it when that
cursor arrives on the stream. Failed mutations roll back visibly with Retry and
Dismiss actions.

## Capabilities

- switch among all registered workspaces while keeping already-open boards in
  memory;
- use clean, shareable explorer and task URLs;
- switch between virtualised fixed-lane board and compact list views;
- filter by text, status, assignee, label, project, or task state;
- order by updated time, created time, ID, or title;
- hide statuses and configure visible card fields per workspace;
- save browser-local named views and choose system, light, or dark themes;
- drag cards between lanes or use the accessible card Move menu;
- create tasks and optimistically edit title, description, status, assignee,
  and labels;
- read and resolve waits, preserving feedback when resolution fails;
- add/remove typed links and upload/download file resources;
- read relationships and a chronological, Markdown-rendered activity timeline;
- add durable comments; and
- choose the actor recorded on browser mutations.

Lanes, terminal states, and available labels are read from workspace config and
update live without code changes. Unknown statuses found in older task files
remain visible so tasks never disappear.

## Keyboard model

The board is usable without a mouse:

| Binding | Action |
|---|---|
| `Cmd/Ctrl+K` | Open command palette |
| `J` / `K`, arrows | Move card selection |
| `Enter` | Open selected task |
| `Esc` | Close the active panel/dialog |
| `M`, then lane number | Move selected task |
| `Shift+Left/Right` | Move to previous/next lane |
| `/` | Focus task filter |
| `C` | Create task |
| `A` / `L` | Open selected task for assignee/label editing |
| `W` | Toggle the waiting-task view |

The command palette exposes navigation, view, workspace, and move actions with
bindings shown beside discoverable commands.

## Views, preferences, and deep links

Canonical URLs are:

```text
http://127.0.0.1:7463/workspaces/dispatch
http://127.0.0.1:7463/workspaces/dispatch/tasks/JOB-0001
```

Existing `?workspace=NAME&task=TASK-ID` links and `/next/workspaces/...` links
are canonicalized to those paths. The classic fallback uses
`/classic/workspaces/...`.

View mode, filters, ordering, empty/hidden status visibility, card fields,
theme, and named views are stored per workspace in browser local storage. The
new board reads the classic `docket.explorer.v1.{workspace}` preferences when
migrating to its v2 shape. Preferences never modify `.docket/config.yaml` or
task files.

## Plugin registry seam

The board consumes the approved JOB-0047 v1 interfaces from
`web/src/registry/contracts.ts`: framework-neutral task cards use
`appliesTo` + `mount/update/destroy`, while URL resolvers expose
`pattern`/`kinds` + synchronous-or-async `resolve`. Build-time modules are
loaded as lazy ESM chunks and registered by their namespaced keys.

Reference chips on cards, task resources, and reference-bearing activity flow
through one `ResolvedReference` component with a safe hostname/kind fallback.
Plugin cards mount on board cards and task-detail headers. Every imperative
mount/update/destroy call is isolated so one broken plugin degrades to a local
fallback instead of breaking the board. The bundled demo plugin resolves
`plans.myslop.app` plan links and mounts a live-updating plan/status card; tests
also register a deliberately broken card to prove failure isolation.

## Ephemeral live data

External local services can publish high-frequency display state without
writing the durable ledger:

```sh
curl -sS \
  -H 'Content-Type: application/json' \
  -d '{"kind":"dispatch/session","task":"JOB-0001","session":"abc","payload":{"state":"working"},"ttl_ms":30000}' \
  http://127.0.0.1:7463/api/workspaces/dispatch/live
```

`kind` is required, payload must be valid JSON no larger than 64 KiB, and TTL is
1–600000 ms. At most 2048 current items are retained per workspace. Data and
expiry are in memory only; there is no call path from this endpoint to
`events.jsonl`.

## Actor identity

The **Acting as** field is stored in local storage and sent as
`X-Docket-Actor` on writes. It becomes the event actor and comment author. The
default is `web`.

## Security model

The interface has no authentication and binds to loopback by default. A
non-loopback bind is refused unless `--allow-remote` is explicitly supplied.
Use an authenticated tailnet/reverse-proxy edge before remote exposure.

All routes, including the stream, reject non-loopback Host headers on the
default service. Mutations:

- require JSON content type except bounded multipart upload;
- reject cross-origin browser writes;
- limit JSON bodies to 1 MiB, live payloads to 64 KiB, and uploads to 25 MiB;
- validate complete PATCH requests before mutation; and
- use the same action layer, task locks, atomic writes, and events as the CLI.

Downloads resolve exact attachment-manifest entries and always use attachment
disposition, `nosniff`, and a sandbox CSP. Markdown is rendered server-side
with raw HTML disabled and unsafe links removed. The client uses same-origin
relative URLs and cookie-compatible EventSource, so future edge authentication
does not require a transport redesign.

## HTTP API

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/healthz` | Service health |
| `GET` | `/api/workspaces` | Runtime status for registered workspaces |
| `GET` | `/api/plugins` | Installed plugin schemas and current scoped values |
| `PATCH` | `/api/plugins/{plugin}/config` | Validate and update instance plugin config |
| `GET` | `/api/workspaces/{name}/board` | Compatibility board snapshot with plugin metadata |
| `GET` | `/api/workspaces/{name}/stream` | SSE snapshot + resumable live tail, including plugin metadata |
| `POST` | `/api/workspaces/{name}/live` | Publish in-memory TTL data |
| `PATCH` | `/api/workspaces/{name}/plugins/{plugin}/config` | Update workspace plugin config |
| `PATCH` | `/api/workspaces/{name}/plugins/{plugin}/statuses/{status}` | Update status-scoped plugin config |
| `POST` | `/api/workspaces/{name}/tasks` | Create a task |
| `GET` | `/api/workspaces/{name}/tasks/{id}` | Read a complete task bundle |
| `PATCH` | `/api/workspaces/{name}/tasks/{id}` | Edit task fields or move status |
| `PUT` | `/api/workspaces/{name}/tasks/{id}/wait` | Set the active wait |
| `POST` | `/api/workspaces/{name}/tasks/{id}/wait/resolve` | Resolve an exact wait ID |
| `POST` | `/api/workspaces/{name}/tasks/{id}/references` | Add a typed reference |
| `DELETE` | `/api/workspaces/{name}/tasks/{id}/references/{reference}` | Remove a reference; send `{}` JSON |
| `POST` | `/api/workspaces/{name}/tasks/{id}/comments` | Append a comment |
| `POST` | `/api/workspaces/{name}/tasks/{id}/attachments` | Upload a file and optional caption (multipart, 25 MiB max) |
| `GET` | `/api/workspaces/{name}/tasks/{id}/attachments/{file}` | Download an exact manifest-backed file |
| any | `/plugins/{plugin}/*` | Same-origin proxy to an enabled plugin's loopback service |

See [Plugin UI registry contract](plugin-ui.md) for the board-facing metadata and settings schemas.

Errors use `{"error":"..."}`. Existing endpoint fields remain compatible;
mutation bundles only add the optional `cursor` field.

## Development and committed build

Frontend source lives under `web/`. Vite emits the committed `web/dist` tree,
which a small Go package embeds so `go build`, `go install`, and release builds
remain self-contained without Bun at runtime.

```sh
cd web && bun install
make web          # type-check, build, and enforce ≤170 KiB gzip
make web-check    # rebuild and fail if web/dist differs
make test         # Go, classic Bun helpers, and React/Vitest tests
make build
```

The classic source remains under `internal/service/web/` during the fallback
window. CI installs the locked Bun dependencies, verifies dist drift and bundle
size, then runs Go tests, both frontend suites, vet, and formatting checks.
