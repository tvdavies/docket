# Plan 001: Make Docket a CLI with a headless event runner

> Follow the steps and verification gates in an isolated implementation worktree. This document is a proposal, not authority to change installed services or publish a release. Update this plan's status in `plans/README.md` when implementation is complete.
>
> Drift check: `git diff --stat 0af2dda0aad26e8578ded011797761fe16f73f62..HEAD -- internal web docs examples scripts Makefile package.json bun.lock go.mod go.sum .github .goreleaser.yaml .gitignore`. Recheck the cited code if these paths have changed.

## Status

- Priority: P1; effort: L (multiple days); change risk: medium.
- Category: architecture, dependencies, CLI design.
- Depends on: none. Inbox correctness has a separate plan.
- Planned at: `0af2dda`, 30 September 2026.

## Background

Docket is a Go/Cobra CLI storing tasks, comments, references, waits, and events in `.docket/`. It also ships two browser applications, HTTP mutation endpoints, SSE projections, iframe plugins, widget records, plugin service hosting, and frontend development tools. The user wants an agent-oriented CLI, retaining hooks/events/inbox for Sal and leaving any future UI to a separate project. Current Dispatch uses the headless parts of Docket's plugin contract, so deleting all plugin code would disrupt unrelated execution.

**Proposed outcome:** remove Docket's web application and application-hosting responsibilities. Preserve storage and event contracts, a foreground/background event runner, and the headless plugin features needed by existing callers. Sal runs independently and can integrate through plain hooks.

## Current state and evidence

- `internal/cli/service.go:16` defines `serve`; it starts `NewManager`, follows workspaces/plugins, and calls the HTTP `Serve` function.
- `internal/service/manager.go:330` owns workspace open/retry, watcher setup, handler draining, cancellation, and config changes. Browser projections are interleaved with those responsibilities:

  ```go
  failures := handlers.DrainAll(fresh, handlers.Options{Context: ctx, Scope: handlers.ScopeAll, Output: m.output, RefreshConfig: true})
  // Later, the watcher setup mixes projections and durable delivery:
  running.stream.observe(cursor, reset)
  running.stream.setConfig(configForStream(fresh))
  if err := drain(); err != nil { return err }
  ```

- `internal/service/http.go:16` imports `github.com/tvdavies/docket/web`; line 19 also embeds the classic `web/*` assets. `stream.go`, `widgets.go`, `widget_projection.go`, `plugins.go`, and `api.go` provide the browser/API layer.
- `internal/service/api.go:586` and `:664` contain validated, locked instance/workspace/status configuration mutations. Preserve these behaviours outside HTTP before deleting their handlers.
- `internal/plugin/manifest.go:203` uses `decoder.KnownFields(true)`. Removing the `ui` or `service` fields outright would reject existing manifests during ordinary workspace loading.
- `internal/bundle/bundle.go:129` folds persisted widget events and exposes their summaries in CLI history. Their producers can retire without erasing old evidence.
- `Makefile:10` runs Go, classic JS, React, and TypeScript tests; CI installs Bun and rebuilds committed assets. `goldmark` is used by the web Markdown renderer.

The inspected Dispatch checkout at `https://github.com/tvdavies/dispatch/blob/632ff553d06e1326312e19daabc05faba7da532d/docket-plugin.yaml` contributes four Lua hooks, a `merge` status, scoped config, CLI passthrough, and UI. Its service declaration has a URL/health check but no launch command. `lib/service.mjs:42` supports `DISPATCH_WIDGETS=0`. A point-in-time machine inventory found only this installed plugin and confirmed the workspace uses plugin wiring. Recheck at rollout; do not publish registry values.

## Scope and boundaries

In scope: `internal/service/`, relevant `internal/cli/` commands and help, plugin manifest compatibility and authoring, extracted plugin configuration operations in `internal/pluginmgr/`, workspace/registry compatibility, widget compatibility readers, `internal/actions/widgets.go`, `internal/bundle/`, associated tests, `web/`, UI examples/docs/prototypes/screenshots, `docs/embed.go`, build/release/CI manifests and lockfiles.

Preserve task IDs, task files, comments, attachments, relations, waits/references, event logs, cursor semantics, native hook runtimes, JSON task fields needed by current callers, and existing handler ownership. Do not alter live `.docket/` stores, installed units, registries, credentials, or Dispatch source as part of this repository change. Do not modify the untracked `docs/research/` artifacts. Inbox acknowledgement and compact context design are separate work.

Match existing conventions: shared mutations through `internal/actions/tasks.go`, error returns with context, stdout reserved for requested output, diagnostics on stderr, locks and atomic writes in `internal/store/`. Use temporary workspaces and isolated config paths in tests, following `internal/handlers/runner_test.go` and `internal/service/service_test.go`.

## Target command behaviour

- Proposed `docket run [--all]`: run workspace watchers and drain handlers, without opening a network listener. Keep `serve` as a documented deprecated alias for one transition so existing unit files can still run.
- Proposed `docket run --once [--all]`: perform one bounded drain cycle and exit; useful for a heartbeat or a scheduler. Leave failed or unprocessed events pending, return nonzero on drain failure, and never imply the originating task mutation was rolled back.
- Retain `docket service install/start/stop/restart/status/logs` as optional systemd conveniences. New units execute `run --all`; containers invoke the foreground runner directly.
- Retain `events`, `watch`, `inbox`, and the existing task commands. The runner delivers events; it does not schedule agent jobs or invoke reasoning itself.
- Keep headless plugin install/enable/disable/update/validate, CLI passthrough, handlers, statuses, scoped config, and cursor handoff for compatibility. Remove plugin UI scaffolding/development and process-hosting commands.

## Implementation sequence

### 1. Extract the event runner from the browser lifecycle

Refactor `internal/service/manager.go` so the retained path only loads/reloads workspace and hook configuration, manages watcher cancellation/retry, drains durable handlers, and reports operational status. Remove dependence on `workspaceStream`, widget projections, UI hashes, proxy state, or browser subscribers. Keep watcher registration before initial draining so startup cannot miss events; retain failure backoff and recovery after workspace/log replacement.

Add `run` and `--once` in `internal/cli/service.go`; preserve a compatibility `serve` alias with no HTTP listener. Removed listen flags should report the new behaviour clearly, not silently bind or pretend to serve. Update `internal/service/systemd.go` and help. Keep all current registry workspace and handler cursor paths unchanged.

Preserve/rehome `TestManagerWatchesWorkspaceAndDrainsHandlers`, `TestManagerCancellationStopsRunningHandler`, `TestManagerPinsRegisteredWorkspaceAndRecoversAfterRecreation`, `TestManagerReloadsHandlerConfigWithoutNewEvent`, registry reconciliation/pruning tests, and `TestWatchWithSetupDoesNotMissEventsAppendedDuringSetup`. Also retain `TestHandoffServiceAndSupervisorContinuity` in `internal/pluginmgr/handoff_service_test.go`: despite its name, it verifies manager/config watching and pending hook delivery across ownership changes, not the plugin process host being removed. Add CLI integration coverage for one-shot success/failure and foreground cancellation without HTTP.

**Verify:** `go test ./internal/service ./internal/events ./internal/handlers ./internal/cli` exits 0. Tests must still cover backlog delivery, config reload without a new event, cancellation of handler children, and no lost event during startup.

### 2. Preserve headless configuration and manifest compatibility

Move `patchInstancePluginConfig`, `patchWorkspacePluginConfig`, and their shared validation/locking helpers from `internal/service/api.go` into non-HTTP operations, for example `internal/pluginmgr/config.go`. Expose the necessary read/update operations through `docket plugin config`, with explicit scope, JSON/file input, and the existing validation semantics. Keep literal values distinct from defaults; omit secrets from diagnostic output and redact secret fields from ordinary reads.

Retain legacy manifest `ui`, `service`, and `options_from` parsing during transition, with no UI serving or option-fetching behaviour. Stop validating UI asset availability as a prerequisite for headless task access. Ordinary hook/CLI schemas must remain validated. Do not silently ignore a `service.command` that an operator expects to be running: the runner's preflight must identify this unsupported hosting requirement and report migration to an OS/container supervisor. It must not prevent unrelated ordinary task reads.

Remove `plugin new/dev/logs` and UI scaffold templates, while retaining `plugin validate` and headless plugin management. Move validation out of the combined authoring file as needed. Remove UI tree hashing and asset watching from `plugin_watch.go`; retain config/manifest change detection affecting hook composition. Preserve plugin-handler names and checkpoints exactly.

**Verify:** `go test ./internal/plugin/... ./internal/pluginmgr ./internal/workspace ./internal/registry ./internal/cli` exits 0. Port the persistence/rejection/concurrent settings assertions from `plugin_settings_test.go` and `service_test.go`; preserve passthrough and handoff tests. Add a fixture with Dispatch-shaped legacy UI/options metadata whose headless hooks and CLI still work with its UI directory absent.

### 3. Remove HTTP, browser assets, widget production, and hosting

Delete both `web/` and `internal/service/web/`, HTTP handlers/server, board API/SSE streams, proxying, UI assets/SDK bridge, live-preview caches, HTML rendering, and `internal/service/supervisor.go` plugin-process hosting. Remove UI-only portions of mixed files after steps 1–2 move retained behaviour. Remove widget creation/finalisation operations from `internal/actions/widgets.go` and associated API endpoints.

Keep a small read-only legacy widget decoder where necessary to preserve current full-bundle JSON and readable historical summaries/references. Preserve all old event bytes; new session/outcome information uses normal comments, references, and artifacts. Removing historical bundle fields is a later versioned contract change, not a prerequisite for eliminating UI execution. Archive the removed visual designs through existing Git history; delete UI-only prototypes/screenshots and update links in retained docs.

Retire presentation/proxy/HTTP tests, but port task mutation, wait conflict, reference lifecycle, configuration validation, and event guarantees to the shared operations or CLI where equivalent coverage is absent. Do not make the suite pass by deleting behavioural coverage with its former HTTP transport.

**Verify:** `go test ./...` exits 0. An old workspace fixture with widget records yields its existing evidence from the CLI; new ordinary operations require no web service. `git ls-files web internal/service/web` lists no retained tracked files after staging the deletion. `go list -deps .` contains neither `github.com/tvdavies/docket/web` nor any `github.com/yuin/goldmark` packages.

### 4. Make the build and agent documentation Go-only

Remove root and UI package manifests, lockfiles, TypeScript SDK declarations, UI build/test scripts, Bun setup in CI, web asset drift checks, frontend Make targets, and UI scaffold examples. Update `docs/embed.go` to embed only retained CLI/runtime references. Remove Goldmark from module dependencies via `go mod tidy`; retain fsnotify, flock, Cobra, gopher-lua, and YAML. Update installation/build documentation and simplify the agent guide around common task operations; hooks and advanced configuration remain discoverable through `docket docs`.

Keep the static single-binary release structure and Linux/macOS targets. The normal developer path becomes `make build`, `make test`, and `make vet`, requiring Go only. Capture before/after binary size with the same compiler, target, and linker flags during implementation; report measured results, not inferred savings from source line counts.

**Verify:** `make build`, `make test`, and `make vet` exit 0; `test -z "$(gofmt -l .)"` succeeds. CI has no Node/Bun install or frontend step. `make test` runs meaningful retained Go tests and performs no dependency installation. Confirm README/help describe a CLI and headless runner and link to existing documents.

## Rollout and recovery

First validate in fixture workspaces. A separate, explicit operational rollout should disable Dispatch widget publishing (`DISPATCH_WIDGETS=0`), inventory any other HTTP consumers, update the runner unit, and restart services under the chosen owner. The audit itself must not perform that rollout. Recheck installed manifests for `service.command`; the inspected machine has none, but other installations may differ.

Publish a release note identifying removed HTTP/UI/hosting capabilities. Keep old task/event files compatible and preserve the old binary for rollback. A rollback may re-enable the browser service without rewriting job history. A later complete plugin removal can use the existing reverse cursor-handoff mechanism, after adapting native handler paths/configuration and testing replay behaviour; it is not part of this first cut.

## Done criteria

- All final verification gates above pass; a Go-only environment can build and test.
- No browser assets, HTTP listener, plugin proxy, or plugin process supervisor ship in the normal executable.
- Tasks, waits, references, events, hooks, inbox, and CLI extensions remain usable without any running web service.
- Existing Dispatch-shaped manifests retain their headless contributions and cursor identities.
- Durable hooks survive stop/restart, failed delivery, config reload, and workspace recreation.
- Existing history remains readable; migration and removed commands are documented.
- The plan index is updated only when implementation is actually complete.

## Stop conditions

Stop the affected removal and report a concrete dependency if an unaccounted consumer requires the HTTP API, a manifest depends on hosted execution that has no replacement, a refactor would rewrite cursor/event history, or preserving a retained behaviour requires deleting its tests. Do not resolve these cases by silently disabling hooks or replaying acknowledged events. User-requested implementation should use an isolated worktree; commit/push/release only within that request's authority.
