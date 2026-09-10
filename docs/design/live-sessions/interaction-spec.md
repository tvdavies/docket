# Live session widgets — interaction specification

Reviewed specification for how a Dispatch session appears in Docket at the board, task-activity and full-session levels. Source: plan v2 (approved 10 September 2026) and the JOB-0091 owner architecture decision. Types referenced here are the *proposed* shapes in `contracts.proposed.ts`; production names belong to JOB-0093.

Terminology follows the shipped `docs/plugin-ui.d.ts` where it exists (`mount`/`update`/`destroy`, `workspace`, `task`, `pluginBase` → here `serviceBase`). Everything else is proposed.

## 1. Anatomy

Docket creates the outer container only for enabled workspace declarations. It owns placement, stable identity, width/overflow limits, lifecycle, generic failure and the saved fallback. The plugin supplies bounded presentation values through a display adapter and chooses a standard composition **or** one custom-element body. Neither option changes the wrapper or grants mutation authority.

```text
Docket-owned wrapper — board contribution or one activity entry
  Header — avatar, plugin label (persona · stage), execution status; separate freshness
  Persistent notices — error/input callouts and availability notices, outside any clipping
  Declared body slot — standard themed composition OR one custom Web Component
    Ordered, bounded text/tool preview, visible immediately in task activity
    Optional disclosure/detail: the same content expands in place, no duplicate transcript
  Controls — Expand/Collapse activity, New activity (reader-owned)
  Footer — session reference, known start/duration, safe reference anchors, Open session
  Generic fallback — replaces an unavailable/failed body; the saved record stays readable
```

Prototype: `prototype/components/wrapper.ts` (wrapper), `bodies.ts` (standard body and `<demo-session-body>`), `kit.ts` (roles), `tokens.css` / `kit.css`.

### Themed component roles

| Role | Shared behaviour | Prototype |
| --- | --- | --- |
| Status and metadata | Text plus tone; execution and freshness are separate elements; wraps on narrow layouts; no colour-only state, no invented metrics | `status()`, `freshness()`, `meta()` |
| Ordered activity/steps | Stable keyed text/tool rows in event order; a tool updates its original row; step checklists only when supplied | `renderEntries()` |
| Disclosure | Native `<button aria-expanded aria-controls>`; visible focus; reader-owned expansion; works inside a shadow root | `disclosure()` |
| Safe text/code | Plain text or bounded `<pre>`; no raw HTML, remote images or hidden private data | `safeText()`, `code()` |
| Notice | Persistent labelled callout; `role="alert"` for errors, `role="status"` for input/availability; no focus grab, no action controls | `notice()` |
| Reference | Validated real anchor with meaningful label; independent of disclosures and board drag targets | `reference()`, `links.ts#safeHref` |
| Summary | Durable outcome, known duration, safe links; readable without plugin code | `summary()` |

### Semantic tokens

Namespaced `--widget-*` variables, aliased from Docket `web/src/styles.css` values copied into `tokens.css`. Custom bodies inherit them through the shadow boundary and must not define app-wide selectors.

| Group | Tokens |
| --- | --- |
| Colour | `surface`, `surface-raised`, `surface-sunken`, `text`, `text-muted`, `border`, `border-strong`, `accent`, `focus`; `positive/warning/danger/info` `-fg`/`-bg` pairs. Light `#ffffff`/`#161922`; dark `#14171e`/`#f2f4f8`. Text ≥ 4.5:1, controls/focus ≥ 3:1 are checked in `tests/contrast.test.ts`; three light-theme roles were darkened because Docket's raw accents are not text colours on raised surfaces. |
| Typography | Inherited system stack, `text-size` 14px, `meta-size` 12px, mono for code, `line-height` 1.5, tabular numerals for durations. No external fonts. |
| Spacing / density | `space-1..4` (4/8/12/16px). Density changes `pad-x`, `pad-y`, `gap` only — never preview budgets or the 44px phone target. |
| Borders / focus | 1px border, 7px inner / 10px outer radius, 2px focus ring with 2px offset. |
| Motion | `pulse-duration`, `scroll-behavior`; reduced motion sets the pulse to none and scrolling to `auto`. No height transition during streaming. |

Theme (light/dark), density and reduced motion resolve at the host and arrive in `context.preferences`; a change is delivered in the same update transaction and never remounts.

## 2. Information hierarchy

### Board

Agent → stage → labelled execution state → one meaningful action (≤ 2 wrapped lines, ≤ 120 characters) → separate freshness → links. No token text, tool output, scroll region or transcript subscription; no detail lease. With several sessions, choose awaiting input, then failed, then active, then latest terminal (ties by creation time, then ID) and show a labelled "+N sessions" link to task activity. The task title and Open session are independent anchors, never nested in a button or drag target.

### Task activity

One entry per session, keyed `(workspace, plugin, sessionId)`, positioned at session creation time. Creation and finalisation update the same entry. Header: persona, stage, execution, freshness. Body: assistant prose interleaved with tools in event order. Footer: shortened session ID (full ID in session details), known start/duration, Open session. A session pointer alone does not prove an executing agent.

### Preview and expansion

Preview: latest **4** ordered entries, ≤ **600** assistant characters, one line per tool, truncation notice when earlier entries were dropped. Error/input callouts render outside the clipped body. Expansion replaces the preview in normal page flow with ≤ **12** entries / **1,600** characters; successful tools start collapsed; a tool disclosure shows redacted arguments/result ≤ **2,000** characters. "More in the full session" is a link. Counts, characters and payload bytes (≤ 16 KiB) are bounded independently (`PROPOSED_BUDGETS`).

### Full session

Breadcrumb (workspace / originating task), task title, persona/stage, full session ID, execution + freshness, then one ordered transcript. User messages may appear here only. Successful tools collapse; failed tool detail expands by default. Long output uses explicit bounded "Show more" chunks. There is never a second transcript.

## 3. Navigation and keyboard

- Open session is a real anchor to `/plugins/dispatch/sessions/:id` (ID as a path segment). Normal click navigates in-tab; modifier-click and the link menu work. Off-origin, `javascript:` and `file:` destinations are rejected; plan/PR references may be safe external HTTPS.
- Session metadata carries the originating workspace/task, so direct links and refresh restore the breadcrumb without guessing. Unknown IDs render an honest not-found page with a workspace link.
- "Back to TASK" is a canonical anchor. Browser Back follows history and, when the entry exists, restores the expanded card, the focused Open session link and the scroll position (`history.state` in `app.ts`). Fresh navigation focuses the destination heading once; streaming never moves focus.
- Tab/Shift+Tab follow document order (body precedes controls and footer). Enter activates anchors; Space/Enter toggle disclosures. Disclosure names include session/tool label, visible status and expanded state. Collapsing a region containing focus first returns focus to its toggle. Link activation never toggles a disclosure or starts a board drag.
- Targets ≥ 44px high on phones, visible focus outlines, no hover-only information. One small polite live region per card announces a transition once; no live region around streamed tokens.

## 4. Responsive layout and streaming

Desktop: document column ≤ 760px with a secondary properties column at wide widths. Below 700px, metadata stacks under the header, tool summaries wrap, and the session anchor remains reachable at 320–390px. Words accompany colour and icons.

The unexpanded preview updates about once per second: text grows, a tool starts/updates/completes at its original position, later text follows. Expand and Open session are not prerequisites. Preview updates come from the shared bounded workspace snapshot (no per-widget board stream); detail is requested only on expansion. The host routes and deduplicates by identity; widgets never parse the page URL.

Preview updates never scroll the page. While the reader has selected text or expanded the card, the visible window freezes and a "New activity · show" control advances it deliberately. Full-session follow-tail holds only while the reader is at the end and not selecting/focusing transcript content; scrolling up releases it and "Jump to latest" resumes it (instant with reduced motion). Entry IDs and tool-row DOM identity survive updates, including a running tool that fails.

## 5. State mapping and finalisation

Execution, freshness and availability are orthogonal. The mappings are Dispatch publisher responsibilities (`prototype/adapter.ts` illustrates), not Docket core special cases.

| UX state | Input | Presentation |
| --- | --- | --- |
| Pending | `reserved` / `starting` | "Queued" / "Starting"; no fabricated progress |
| Running | `working` | current tool, else latest text, else "Running; no activity yet." |
| Awaiting input | `blocked` (+ `attention.kind: "input"`) | persistent callout; generic "Input required; details unavailable" when absent; a plan-feedback wait is task context, not session execution |
| Completed | authoritative `completed` | durable outcome, known duration, published links; no approval inference |
| Failed | `error` | prominent sanitized error, failed detail expanded; a failed tool alone ≠ failed session |
| Cancelled | `cancelled` | explicit label with last useful outcome; never green |
| Connecting / live / disconnected | connection state + receipt | separate transport label; disconnected keeps last-known content |
| Stale | TTL expired | "Last known running · stale"; no inferred failure |
| Missing service | availability `missing-service` | saved summary stays; details unavailable; read-only explicit Retry |
| Removed/disabled plugin | declaration absent | generic host renders the durable record with no plugin code |
| Unknown version/state | unsupported schema/enum | generic saved record, "Unsupported session data"; never assume running |

Freshness defaults for review: summaries renew ≤ 1/s when changing, heartbeat every 10s, live TTL 30s, measured from host receipt time. An empty restored live cache means awaiting rehydration. Terminal saved summaries do not expire.

Finalisation writes one idempotent durable snapshot for the same identity (last explicit assistant outcome, else "Session completed; no summary was published"). Duplicate same-revision writes are no-ops and lower revisions never overwrite newer final state. A completed card defaults to its summary on the next visit. During a live transition it collapses only if the reader has not expanded, selected or focused inside it; otherwise it keeps the reading position and offers Collapse. Failure and input callouts stay visible even after collapsing.

## 6. Contracts and lifecycle

See `contracts.proposed.ts`. Context (identity, location, service base, preferences, abort signal, narrow read-only helpers) and snapshot (task fields, versioned opaque data, freshness, availability, durable fallback) are separate inputs; the context never carries a second copy of the task or live payload.

| Input change | Outcome |
| --- | --- |
| Task fields, newer data, freshness/availability, final fallback | update in place; preserve wrapper/body identity, focus, selection, disclosure; validate version, unknown → generic fallback |
| Theme, density, reduced motion | same transaction; tokens update without remount |
| Identity, location or service base | abort, release lease/listeners/timers, destroy body, mount a new instance only if enabled |
| Disable/removal, body failure, incompatible version | retire the body; keep the wrapper's fallback; no remount loop; no stale response may resurrect it |
| Async result after scope change | ignore via abort + host generation + revision checks; cleanup idempotent |

`requestDetail()` is a bounded, read-only, instance-scoped lease released on collapse, selection change or retirement. Preview messages are full replacement snapshots (skipped revisions safe; duplicates/older ignored). Detail gaps pause application and request an owner snapshot; a reset replaces the projection without a second card or regressing a final record.

## 7. Data minimisation

Filtered before publication (`prototype/publisher.ts`): no reasoning, credentials, environment dumps, absolute paths, raw prompts, raw arguments or results in previews or durable records; project-relative tool summaries; no raw HTML or remote images; no synthetic percentages or metrics. Unknown means absent, not zero. The prototype contains only hand-authored synthetic data.

## 8. Prior art

| Source | Adopt | Reject |
| --- | --- | --- |
| Slack Thinking Steps | task cards, collapsed groups, progressive disclosure, open/append/finalise | Block Kit components, mandatory checklist, exposing private reasoning |
| Linear agent interaction | session identity/lifecycle, explicit session URL, replacing ephemeral progress | activity-inferred lifecycle, stale-as-execution, 10s responsiveness policy |
| Dispatch PR #8 (`sessionProjection.ts`, `SessionView.tsx`, `useFollowAtEnd.ts`) | seq-ordered entries, updates at original tool position, bounded details, error expansion, scroll-lock release | app-wide imports, Stop/permission actions, remounting focused failed tools, token-wide announcements, unbounded "Show full", unconditional smooth scroll. JOB-0050 reuses the projection across the plugin boundary instead of writing a second one. |

## 9. Non-goals

No steer/stop/reply/wait/permission/approval controls. No production SDK, kit, loader, element registry, second projection, JSON layout engine, arbitrary placement or persistent panels. No change to production code, manifests, `docs/plugin-ui.d.ts`, live workspace state, loading architecture or approval gates. Session completion never implies plan approval or task completion.
