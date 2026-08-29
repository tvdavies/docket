# Configuration reference

Docket has two distinct configuration files:

1. `.docket/config.yaml` defines one workspace's task model and handlers. It belongs with the workspace and may be committed.
2. `~/.config/docket/config.yaml` is the machine-local service registry. It contains workspace paths, installed plugins and instance plugin config, and the HTTP listen address.

Plugin manifests, scoped config, and composition rules are documented in [Plugins](plugins.md).

## Validate a workspace

```sh
docket workspace check
docket workspace check /path/to/project
docket workspace check --json
```

Every workspace command also validates config while opening it. The service reloads changed workspace configuration automatically; an invalid workspace is marked unavailable rather than crashing other runtimes.

## Workspace config

A complete example:

```yaml
statuses:
  - backlog
  - ready
  - in-progress
  - blocked
  - in-review
  - done

terminal:
  - done

labels:
  - bug
  - feature
  - chore

relationships:
  - name: blocks
    inverse: blocked-by
  - name: parent
    inverse: subtasks
  - name: relates
    inverse: relates
  - name: duplicate-of
    inverse: duplicates

settings:
  id_prefix: TASK
  id_padding: 4
  project_prefix: PROJ
  project_padding: 4

handlers:
  notify-completion:
    on: [task.moved]
    match:
      data.to: done
    lua: hooks/notify.lua
    delivery: service

plugins:
  example:
    config:
      checkout: /home/me/dev/example
```

### `statuses`

Ordered task lanes. The first status is the default for new tasks. `docket move` rejects values not in this list.

Changing or removing a status does not rewrite existing task files. Migrate existing tasks before removing a value they use.

### `terminal`

Statuses declared as closed for consumers and presentation. The core CLI still lists every task unless `--status` filters it. Values should also appear in `statuses`.

### `labels`

Suggested labels. Labels are advisory and free-form values remain allowed.

### `relationships`

Relationship types and their inverse names. Linking one task updates both task files:

```yaml
- name: blocks
  inverse: blocked-by
```

A symmetric relationship repeats its own name:

```yaml
- name: relates
  inverse: relates
```

### `settings`

| Field | Default | Meaning |
|---|---:|---|
| `id_prefix` | `TASK` | Task ID prefix |
| `id_padding` | `4` | Numeric task ID width |
| `project_prefix` | `PROJ` | Project ID prefix |
| `project_padding` | `4` | Numeric project ID width |

Prefixes must contain only letters, numbers, hyphens, and underscores; padding must be positive. Treat ID settings as persistent workspace contracts. Changing a prefix after tasks or projects exist can make ID allocation and argument recognition inconsistent with stored files. Docket rejects non-canonical or path-like task and project IDs before filesystem resolution.

## Handler config

Each handler has a lowercase name containing letters, numbers, hyphens, or underscores:

```yaml
handlers:
  cheer-on-done:
    on: [task.moved]
    match:
      data.to: done
    lua: hooks/cheer.lua
    delivery: service
```

| Field | Required | Meaning |
|---|---|---|
| `on` | yes | Event types to consume; `"*"` consumes every type |
| `match` | no | Exact-value predicates; every entry must match |
| `lua` | one runtime | Lua script path relative to project root |
| `run` | one runtime | Executable path relative to project root |
| `delivery` | no | `inline` (default) or `service` |

Exactly one of `lua` and `run` is required.

### Event matching

`match` keys are dotted event paths. Supported top-level fields are:

```text
seq, time, type, task, title, actor, assignee, data
```

Only `data` supports nested paths:

```yaml
match:
  task: TASK-0007
  actor: researcher
  data.to: done
  data.metadata.priority: 1
```

Values use exact type-aware equality, with numeric YAML/JSON representations normalised. All predicates use logical AND. An absent path does not match.

Invalid roots and unsupported nesting fail config validation instead of silently discarding events.

### Delivery

`delivery: inline` is the default. The mutating CLI command runs the handler after the event is durable and waits for completion.

`delivery: service` leaves the handler cursor pending for the machine service. The CLI returns after appending the event, while the service performs durable asynchronous delivery. If the service is stopped, events remain in the log and drain after it starts.

### Cursors and retries

Each handler owns a checkpointed cursor under:

```text
.docket/.cursors/handlers/<handler>.cursor
```

Cursors are machine-local and gitignored. A newly named handler begins at cursor zero and sees matching historical events. A failed invocation does not advance its cursor. Delivery is ordered and at least once, so handlers must be idempotent.

Changing a handler's script or filters without changing its name preserves its existing cursor.

### Executable handlers

```yaml
handlers:
  legacy-notify:
    on: [task.moved, task.commented]
    run: hooks/notify
    delivery: service
```

The executable must have execute permission. It runs from the project root and receives a batch of matching JSON events, one object per stdin line.

### Lua handlers

```yaml
handlers:
  route-ready-work:
    on: [task.moved]
    match:
      data.to: ready
    lua: hooks/route.lua
    delivery: service
```

Lua scripts need no execute permission. See [Lua hooks and SDK](lua-hooks.md).

## Machine service registry

Default path:

```text
~/.config/docket/config.yaml
```

Override for tests or dedicated installations with `DOCKET_CONFIG`.

```yaml
listen: 127.0.0.1:7463
workspaces:
  - name: dispatch
    path: /home/user/dev/dispatch
  - name: client-b
    path: /home/user/dev/client-b
plugins:
  - name: dispatch
    path: /home/user/dev/docket-plugin-dispatch
    source: {type: local}
    version: 1.0.0
    config: {}
```

Use commands rather than editing registrations manually:

```sh
docket workspace list
docket workspace add /home/user/dev/client-b --name client-b
docket workspace remove client-b
docket plugin list
docket plugin add /home/user/dev/docket-plugin-dispatch
```

The UI has no authentication. Non-loopback binding is rejected unless `docket serve --allow-remote` is passed explicitly.

## Service environment

The systemd unit optionally reads:

```text
~/.config/docket/environment
```

Use `KEY=value` lines for credentials or PATH additions required by service-delivered hooks, then restart:

```sh
docket service restart
```

Do not commit credentials to workspace configuration.
