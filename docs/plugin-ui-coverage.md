# JOB-0093 implementation coverage

Approved source: plan v1 https://plans.myslop.app/p/0015dbcff3 (SHA-256 `24ebd41dd93600e829149af57447e2716558772c77cbba3ef3ce38146b6f4f56`). Base `a01905339ee55d3e18946a7035c0f94a52ac4be2`. This map is an implementation checklist, not a claim of completed validation.

| Requirement | Code | Required evidence |
|---|---|---|
| One API, legacy ABI, build-time-only loading | `packages/plugin-ui`, `web/src/registry/catalogue.ts` | generated declarations, compiling unchanged v1 and v2 examples, no fixture in production |
| Workspace declaration order, locations, service base | `internal/plugin/manifest.go`, `registry.ts` | two-workspace registry tests, reload/disable tests |
| Go/RE2 reference matching, scoped late-safe resolver cache | `widget_projection.go`, `ReferenceRegistry`, `ResolvedReference.tsx` | order, Go-only regexp, generation and timeout tests |
| Durable idempotent lifecycle, unchanged task dossier | `internal/widget`, `actions/widgets.go`, `service/widgets.go` | concurrent retries, append failure, restart, conflict, one typed activity entry |
| Compact saved records and plain/disabled fallback | `bundle.go`, `widget_projection.go`, `BoardWidgets`, `Activity` | API and browser recovery tests |
| Bounded previews, TTL, revision fencing, no ledger writes | `stream.go`, `widgets.go`, `widget-state.ts` | 1,000 preview ledger byte/count assertion, rejected heartbeat TTL tests |
| One detail lease, receipt/display separation, gap/reset/revoke | `detail-controller.ts` | fake-provider/fake-clock tests and browser connection counters |
| Wrapper, held reader content, keyed references, signal-first cleanup | `widget-host.ts` | failure and late-callback tests; focused/selected body and outage browser checks |
| Kit, real custom element, semantic tokens, duplicate definitions | `kit.ts`, `custom-element.ts`, `tokens.css` | independent progress/chart fixtures, themes/mobile/keyboard/reduced motion |
| Production integration and budgets | `App`, `BoardStore`, `TaskDetail`, `BoardView` | production-host browser tests, bundle/mount/payload/performance counters |
| Public handoff and trust limits | `docs/plugin-ui.md` | exact types/wire examples and compatibility guidance |

Manual NVDA/VoiceOver pass remains an explicit outstanding gate unless a human supplies actual evidence. Automated browser checks are not that pass.
