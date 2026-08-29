# Web interface

The Docket service includes a local multi-workspace Kanban board at:

```text
http://127.0.0.1:7463
```

Start it in the foreground with `docket serve --all`, or manage the background user service with `docket service`.

## Capabilities

- switch workspaces from a breadcrumb and use clean, shareable task URLs;
- switch between fixed-lane board and compact list views;
- filter by text, status, assignee, label, project, or task state;
- order by updated time, created time, ID, or title;
- hide statuses per workspace in browser-local view preferences;
- drag cards between independently scrolling status lanes and create tasks;
- read each task as a rendered Markdown document;
- edit title, description, status, assignee, and labels in place with blur autosave;
- see and resolve active waits with optional feedback;
- manage typed link resources and securely upload or download file resources;
- read relationships and a chronological activity timeline with comments as cards; and
- choose the actor recorded on browser mutations.

The board reads authoritative task files on every refresh and does not maintain
a separate browser or server database. It does not infer external process or
agent liveness. Execution systems publish durable typed references whose links
open their own status or session interfaces. Writes use the same action layer,
per-task locks, atomic writes, validation, and event production as the CLI and
Lua SDK.

Browser mutations therefore trigger normal handlers. The HTTP response does not
wait for handler completion; the service watcher drains the resulting event
asynchronously.

## Views, preferences, and deep links

The canonical explorer and task URLs are:

```text
http://127.0.0.1:7463/workspaces/dispatch
http://127.0.0.1:7463/workspaces/dispatch/tasks/JOB-0001
```

Existing `?workspace=NAME&task=TASK-ID` links remain supported and are
canonicalized to the clean task URL on load. View mode, filters, ordering, empty
status visibility, and hidden statuses are stored per workspace in browser local
storage; they do not modify `.docket/config.yaml` or task files.

## Drag and drop

Drag a task card onto another configured status column. The board updates optimistically, sends a validated status mutation, and restores the previous lane if the write fails.

Unknown statuses found in older task files are displayed in an additional column so tasks never disappear. New and edited tasks may select only statuses currently configured in `.docket/config.yaml`.

## Actor identity

The **Acting as** field is stored in browser local storage and sent as `X-Docket-Actor` on writes. It becomes the actor on task events and comment author metadata. The default is `web`.

## Editing and refresh behaviour

Task descriptions and comments are stored as Markdown and rendered with raw HTML
disabled. Task titles and rendered descriptions are directly contenteditable,
without swapping to a separate input or source editor; property controls remain
compact field editors. Blur converts edited rendered content back to Markdown
and sends a partial task PATCH. Untouched Markdown is never rewritten. Failed
saves retain the editable draft and expose a retry action.

The explorer refreshes every three seconds and when **Refresh** is pressed. Lane,
list, and horizontal board scroll positions survive quiet refreshes. A focused or
failed task editor, comment draft, wait-resolution draft, or resource dialog is
never replaced by background task-detail polling.

## Security model

The interface has no authentication and Docket binds to loopback by default. A non-loopback bind is refused unless `--allow-remote` is explicitly supplied.

Mutation endpoints:

- require `Content-Type: application/json`, except the bounded multipart attachment upload;
- reject non-loopback Host headers on the default loopback service, preventing DNS-rebinding reads and writes;
- reject cross-origin browser writes; and
- limit JSON request bodies to 1 MiB and uploaded files to 25 MiB.

Downloads are restricted to exact attachment-manifest entries and are always
served with `Content-Disposition: attachment`, `nosniff`, and a sandbox CSP so
uploaded HTML cannot execute on the Docket origin.

Do not expose the board to an untrusted network merely by passing `--allow-remote`; put authenticated access in front of it first.

## HTTP API

The UI uses a small local JSON API:

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/healthz` | Service health |
| `GET` | `/api/workspaces` | Runtime status for registered workspaces |
| `GET` | `/api/plugins` | Installed plugin schemas and instance values |
| `PATCH` | `/api/plugins/{plugin}/config` | Validate and update instance plugin config |
| `GET` | `/api/workspaces/{name}/board` | Status config, plugin metadata, and task-card summaries |
| `PATCH` | `/api/workspaces/{name}/plugins/{plugin}/config` | Update workspace plugin config |
| `PATCH` | `/api/workspaces/{name}/plugins/{plugin}/statuses/{status}` | Update status-scoped plugin config |
| `POST` | `/api/workspaces/{name}/tasks` | Create a task |
| `GET` | `/api/workspaces/{name}/tasks/{id}` | Read a complete task bundle |
| `PATCH` | `/api/workspaces/{name}/tasks/{id}` | Edit task fields or move status |
| `PUT` | `/api/workspaces/{name}/tasks/{id}/wait` | Set the one active wait |
| `POST` | `/api/workspaces/{name}/tasks/{id}/wait/resolve` | Resolve an exact wait ID |
| `POST` | `/api/workspaces/{name}/tasks/{id}/references` | Add a typed external reference |
| `DELETE` | `/api/workspaces/{name}/tasks/{id}/references/{reference}` | Remove a reference; send `{}` JSON |
| `POST` | `/api/workspaces/{name}/tasks/{id}/comments` | Append a comment |
| `POST` | `/api/workspaces/{name}/tasks/{id}/attachments` | Upload a file and optional caption (multipart, 25 MiB max) |
| `GET` | `/api/workspaces/{name}/tasks/{id}/attachments/{file}` | Download an exact manifest-backed file |
| any | `/plugins/{plugin}/*` | Same-origin proxy to an enabled plugin's loopback service |

See [Plugin UI registry contract](plugin-ui.md) for the board-facing metadata and settings schemas.

### Create

```sh
curl -sS \
  -H 'Content-Type: application/json' \
  -H 'X-Docket-Actor: web-api' \
  -d '{"title":"Investigate cache","status":"ready","labels":["bug"]}' \
  http://127.0.0.1:7463/api/workspaces/dispatch/tasks
```

Fields: `title`, `description`, `project`, `labels`, `assignee`, and `status`. Only `title` is required; an omitted status uses the workspace's first lane.

### Update or move

All fields are optional, but at least one must be present:

```sh
curl -sS -X PATCH \
  -H 'Content-Type: application/json' \
  -H 'X-Docket-Actor: web-api' \
  -d '{"status":"in-review","assignee":"reviewer"}' \
  http://127.0.0.1:7463/api/workspaces/dispatch/tasks/JOB-0001
```

Supported fields: `title`, `description`, `labels`, `assignee`, and `status`. The API validates the complete request before applying any field, writes the dossier once under one task lock, and commits the resulting event group before releasing that lock. If event commit fails, the original dossier is restored.

### Wait and resume

```sh
WAIT=$(curl -sS -X PUT \
  -H 'Content-Type: application/json' \
  -H 'X-Docket-Actor: planner' \
  -d '{"kind":"plan_feedback","reason":"Awaiting plan review","reference":"https://example.com/plan"}' \
  http://127.0.0.1:7463/api/workspaces/dispatch/tasks/JOB-0001/wait)

WAIT_ID=$(printf '%s' "$WAIT" | jq -r .wait.id)
curl -sS -X POST \
  -H 'Content-Type: application/json' \
  -d "{\"wait_id\":\"$WAIT_ID\",\"result\":\"approved\"}" \
  http://127.0.0.1:7463/api/workspaces/dispatch/tasks/JOB-0001/wait/resolve
```

The board can add a comment and then resolve the active wait. The status lane does not change. See [Waits, references, and activity](waits-and-references.md).

### Comment

```sh
curl -sS \
  -H 'Content-Type: application/json' \
  -H 'X-Docket-Actor: web-api' \
  -d '{"text":"Research complete"}' \
  http://127.0.0.1:7463/api/workspaces/dispatch/tasks/JOB-0001/comments
```

Errors use a stable JSON shape:

```json
{"error":"task \"JOB-9999\" not found"}
```

## Development without a release

Build and run a second local instance on another port:

```sh
make build
./bin/docket serve --all --listen 127.0.0.1:7464
```

The embedded HTML, CSS, and JavaScript modules live under `internal/service/web/`.
Rebuilding the Go binary embeds changed assets; no frontend package manager or
runtime build step is required. Pure route and view-model tests use Bun without
installing packages:

```sh
bun test internal/service/web/*.test.js
```
