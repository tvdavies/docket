# Docket as an agent CLI

Audit and plans, 30 September 2026, against `0af2dda0aad26e8578ded011797761fe16f73f62`. Scope: remove the web application and its platform machinery while retaining file-backed tasks, agent CLI operations, hooks, events, inbox, and an optional background event runner. No source code or installed services were changed.

The recommended first cut removes browser execution and HTTP hosting, while preserving the headless plugin functions on which the installed Dispatch workspace currently depends. A subsequent removal of plugin installation and composition would require a separate migration of that workspace. Sal can use ordinary executable or Lua hooks without becoming a Docket plugin.

## Plans

| Order | Plan | Priority | Effort | Depends on | Status |
| --- | --- | --- | --- | --- | --- |
| 001 | [Remove web hosting and retain a headless event runner](001-cli-and-headless-events.md) | P1 | L | None | Implemented; PR open for review |
| 002 | [Make inbox reads safe for durable consumers](002-durable-inbox-consumption.md) | P1 | M | None; required before Sal relies on polling | Implemented; PR open for review |

These are independent implementation changes. Plan 002 can land first. Keep the removal diff separate from the inbox correctness change so regressions are easier to isolate.

Implementation (30 September 2026): both plans are implemented on branch `t3code/simplify-docket-headless-runner`, based on `0af2dda` (no drift from the planned commit). Plan 002 is a separate commit from the plan 001 removal. See each plan's *Implementation notes* for decisions and deviations. Nothing is merged, released or rolled out; the operational rollout steps in plan 001 remain to be done.

## Findings

| Finding | Category | Impact | Effort | Change risk | Confidence | Evidence |
| --- | --- | --- | --- | --- | --- | --- |
| Event processing and web hosting share one manager and entry point | Architecture | Deleting the service wholesale would remove asynchronous hooks and recovery | M | Medium | High | `internal/cli/service.go:16`, `internal/service/manager.go:330` |
| Two browser apps, an iframe SDK, and their build/test chain ship with the CLI | Dependencies/DX | Additional assets, frontend tooling, CI work, and maintenance unrelated to agent jobs | M | Medium for HTTP consumers | High | `internal/service/http.go:16`, `web/embed.go:9`, `Makefile:10`, `.github/workflows/ci.yml:21` |
| Plugin hosting and browser features extend into core workspace loading | Architecture | Removing plugin support indiscriminately would disable current Dispatch hooks and CLI access | L for whole-system removal | High without migration | High | `internal/plugin/manifest.go:32`, `internal/workspace/workspace.go:144`, `internal/cli/passthrough.go:35` |
| Inbox acknowledgement can pass an event it never returned | Correctness | Sal could permanently miss a wake event | S for race fix; M for explicit acknowledgement | Medium | High by code inspection | `internal/events/inbox.go:25`, `internal/events/inbox.go:36` |
| Useful validated settings writes exist only behind HTTP | CLI compatibility | Removing the API would leave some configuration scopes without equivalent safe CLI updates | M | Medium | High | `internal/service/api.go:586`, `internal/service/api.go:664`, `internal/cli/plugins.go:19` |
| Full task reads include presentation records and duplicate comment bodies | Performance/CLI | Removing the board alone does not fix oversized agent startup context | M, follow-up | Medium for output compatibility | High | `internal/bundle/bundle.go:129`, `internal/bundle/bundle.go:155`, `internal/cli/tasks.go:185` |

Tracked `web/` and `docs/design/live-sessions/` together contain 132 files and about 4.15 MB at the inspected commit, including tests, generated assets, and screenshots. This is a repository inventory, not a predicted binary-size or startup-time improvement. Runtime binary size and latency were not benchmarked.

## Sal integration

- A durable hook should enqueue an event in Sal and return once Sal has stored a receipt. Docket retries failed delivery; Sal deduplicates receipts and decides whether reasoning is needed.
- Heartbeats can use inbox polling with acknowledgement after durable intake, following plan 002. Current `--mark-read` is not a crash-safe queue consumer.
- `watch` remains useful for live observation; it is not a substitute for durable intake and replay.
- Notification hooks can enqueue or send under their configured policy. Keeping hooks does not require Docket to own a chat interface or a model scheduler.
- A future separate UI can call the CLI and consume JSON/events through its own adapter. It should use Docket mutations rather than editing task files behind its locks.

## Verification and inspection limits

`go test ./...` and `go vet ./...` passed on the inspected checkout. Browser/Bun tests were not run; this was a dependency and removal audit, not a frontend behaviour review. The service lifecycle, event delivery, inbox, CLI, bundle, plugin, build, and documentation boundaries were inspected. This is not a comprehensive security or performance audit.

An independent read-only review checked the plugin/UI removal boundary and plan. Its reminder to retain the real manager/config-watcher handoff test is included in plan 001.

A read-only metadata inventory of the current machine registry found one installed plugin, Dispatch. It contributes four service-delivered hooks and a CLI, with no `service.command`. The Dispatch workspace uses plugin wiring; a second test workspace uses one plain handler. No configuration values or credentials were copied. Repeat the inventory at rollout, since this is a point-in-time observation.

## Considered and deferred

- Delete all of `internal/service/`: rejected; watcher setup, recovery, config reload, and hook draining must survive.
- Delete all plugins in the first cut: deferred; preserve installed Dispatch's hook ownership and checkpoints. Existing reverse handoff is useful prior work for a later migration.
- Keep HTTP solely for a possible future UI: rejected for this direction. A separate adapter can be introduced when there is a concrete consumer.
- Remove Lua: rejected; the user wants hooks retained, and the installed Dispatch handlers use Lua.
- Rewrite or purge widget history: rejected; existing summaries and references remain useful evidence.
- Change `show` defaults while removing UI: deferred. Add a bounded current-state view, history pagination, and structured CLI errors in a focused follow-up after this cut. Preserve requirements and decision references in that design.
