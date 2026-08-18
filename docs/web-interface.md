# Web interface

The Docket service includes a local multi-workspace Kanban board at:

```text
http://127.0.0.1:7463
```

Start it in the foreground with `docket serve --all`, or manage the background user service with `docket service`.

## Capabilities

- switch between registered workspaces;
- view tasks grouped by configured status;
- drag cards between status columns;
- create tasks;
- inspect and edit title, status, assignee, labels, and description;
- see active waits directly on cards and resolve them with optional feedback;
- follow typed plan, pull-request, ticket, and session references;
- read relationships and attachment metadata;
- read a chronological activity timeline and add comments; and
- choose the actor recorded on browser mutations.

The board reads authoritative task files on every refresh. It does not maintain a separate browser or server database. Writes use the same action layer, per-task locks, atomic writes, validation, and event production as the CLI and Lua SDK.

Browser mutations therefore trigger normal handlers. The HTTP response does not wait for handler completion; the service watcher drains the resulting event asynchronously.

## Drag and drop

Drag a task card onto another configured status column. The board updates optimistically, sends a validated status mutation, and restores the previous lane if the write fails.

Unknown statuses found in older task files are displayed in an additional column so tasks never disappear. New and edited tasks may select only statuses currently configured in `.docket/config.yaml`.

## Actor identity

The **Acting as** field is stored in browser local storage and sent as `X-Docket-Actor` on writes. It becomes the actor on task events and comment author metadata. The default is `web`.

## Refresh behaviour

The board refreshes every three seconds and when **Refresh** is pressed. Polling pauses while:

- a card is being dragged;
- a task form has unsaved changes; or
- the new-task dialog is open.

This prevents background refreshes from replacing active edits.

## Security model

The interface has no authentication and Docket binds to loopback by default. A non-loopback bind is refused unless `--allow-remote` is explicitly supplied.

Mutation endpoints:

- require `Content-Type: application/json`;
- reject non-loopback Host headers on the default loopback service, preventing DNS-rebinding reads and writes;
- reject cross-origin browser writes; and
- limit request bodies to 1 MiB.

Do not expose the board to an untrusted network merely by passing `--allow-remote`; put authenticated access in front of it first.

## HTTP API

The UI uses a small local JSON API:

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/healthz` | Service health |
| `GET` | `/api/workspaces` | Runtime status for registered workspaces |
| `GET` | `/api/workspaces/{name}/board` | Status config and task-card summaries |
| `POST` | `/api/workspaces/{name}/tasks` | Create a task |
| `GET` | `/api/workspaces/{name}/tasks/{id}` | Read a complete task bundle |
| `PATCH` | `/api/workspaces/{name}/tasks/{id}` | Edit task fields or move status |
| `PUT` | `/api/workspaces/{name}/tasks/{id}/wait` | Set the one active wait |
| `POST` | `/api/workspaces/{name}/tasks/{id}/wait/resolve` | Resolve an exact wait ID |
| `POST` | `/api/workspaces/{name}/tasks/{id}/references` | Add a typed external reference |
| `DELETE` | `/api/workspaces/{name}/tasks/{id}/references/{reference}` | Remove a reference; send `{}` JSON |
| `POST` | `/api/workspaces/{name}/tasks/{id}/comments` | Append a comment |

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

The embedded HTML, CSS, and JavaScript live under `internal/service/web/`. Rebuilding the Go binary embeds changed assets; no frontend package manager or build step is required.
