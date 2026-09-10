# Requirements → fixtures → checks

Each JOB-0092 acceptance requirement (plan v2, "Requirements-to-fixtures and downstream handoff") maps to a fixture scenario in `prototype/fixtures.ts`, an automated check named with the same identifier, and the downstream owner. Browser check labels are the strings printed by `tests/browser.test.ts`; unit tests are in `tests/publisher.test.ts` and `tests/contrast.test.ts`.

Identifiers: `task-live-unexpanded`, `preview-bounds`, `reader-intent`, `state-matrix`, `terminal-fallback`, `anatomy`, `context-update`, `body-error`, `history`, `theme-density`, `no-live-network`.

The review of `d48e751` (PR #3, review 5168753116) added rows below for reader-owned display state, accepted-payload storage, view-scoped detail selection, the bounded full-session window and the strict context-update title check. Each of the reviewer's fourteen reproductions now has a committed regression under the matching identifier.

## Live before expansion — `task-live-unexpanded`

Fixture: `live` (12 frames, 1s ticks; autoplay on task entry, `paused=1` to pin).

| Requirement | Automated check | Manual script step |
| --- | --- | --- |
| Assistant text grows visibly in the unexpanded entry | `assistant text grows in place` | 2 |
| A tool starts after the text, updates, then completes at its original position | `a tool starts in order after the text`, `the tool updates at its original position`, `the tool completes at its original position` | 2 |
| DOM identity of the tool row survives updates | `tool row keeps DOM identity across updates` | — |
| Expand stays closed and URL stays on the task | `disclosure starts collapsed`, `still collapsed, URL unchanged, no navigation`, `one-shot query stripped; URL is the canonical task route` | 2 |
| No detail lease before expansion | `zero detail leases while previewing` | 2 (strip: `activeLeases 0`) |
| Freshness separate from execution | `freshness label is separate from execution status` | 2 |

Owner: JOB-0050 publisher/widget over JOB-0093 shared snapshot routing.

## Bounds and data minimisation — `preview-bounds`

Fixtures: `live` (frame 10+ exceeds the preview window), raw events carrying `SECRET-MARKER-*`, `PREVIEW-EXCLUDED-USER-MARKER`, absolute paths and raw arguments.

| Requirement | Automated check |
| --- | --- |
| Preview ≤ 4 entries / 600 assistant characters, truncation notice | unit `preview holds at most four entries`, `preview assistant text is capped at 600 characters…`; browser `truncation notice shown for earlier activity` |
| Expanded ≤ 12 entries / 1,600 characters, tool detail ≤ 2,000 characters | unit `expanded detail is capped at 12 entries / 1,600 characters / 2,000-char tool detail` |
| Preview payload ≤ 16 KiB; oversize rejected | unit `preview payload stays under 16 KiB for every fixture frame`, `an oversized payload is rejected rather than published` |
| One detail lease per task view, none per board card | browser `exactly one detail lease when expanded`, `collapsing releases the lease`; `anatomy: board holds no detail lease` |
| Private data filtered before publication, not CSS-hidden | unit `no user prompt, reasoning, private paths or raw arguments in the preview payload`, `detail carries only the redacted input/output…`; browser `private markers absent from the task DOM (filtered, not CSS-hidden)`, `private markers absent from expanded detail` |
| Successful tools start collapsed; disclosure shows redacted args/result | browser `successful tools start collapsed in detail`, `tool disclosure shows redacted arguments/result` |
| Collapse returns focus to its toggle | browser `collapse returns focus to its toggle` |
| Detail selection is view-scoped: expanding three cards keeps one; reselection collapses the previous card with a note | browser `expanding three cards keeps one detail selection per task view`, `reselection collapses the previously selected cards`, `revoked cards explain why their detail closed`, `selecting back moves the single selection` (fixture `multi-session`) |
| Unsupported version releases the body and its selection without a body failure | browser `unsupported version releases the body and its detail selection`, `unsupported data is a release, not a body failure` (fixture `unknown-version`, expanded before the unsupported frame) |
| Missing service releases transport without hiding an expanded last-known view; Collapse stays usable and recovery never auto-reacquires | browser `missing service releases transport but retains last-known expanded detail and usable Collapse`, `last-known detail and availability notice remain while unavailable`, `explicit Collapse keeps the saved summary and cannot reopen unavailable detail`, `service return re-enables Expand without re-acquiring detail on its own`, `detail can be reselected after the service returns` (fixture `missing-service`, expanded before the outage) |

Owner: JOB-0050 shared filtered projection; JOB-0093 enforced budgets and view-scoped selection.

## Reader intent — `reader-intent`

Fixture: `live`.

| Requirement | Automated check | Manual |
| --- | --- | --- |
| Selection freezes the preview; "New activity" advances deliberately | `selection freezes the visible preview window`, `New activity control appears instead of evicting entries`, `New activity advances deliberately` | 5 |
| Streaming never scrolls the page | `streaming never scrolls the page` | 5 |
| Focus inside the body survives streaming | `focus inside the body survives streaming updates`, `Enter toggles the disclosure` | 6 |
| Explicit expansion survives finalisation; unattended completion collapses | `explicit expansion survives finalisation; Collapse offered`, `Collapse to summary shows the durable summary`, `unattended completion collapses to the durable summary` | 8 |
| Input notice renders independently of the held preview (both bodies) | `reader-intent[standard|custom-element]: input notice appears while preview text is selected`, `selection survives the input transition (entries unchanged, nothing withheld)`, `resolved input notice clears while the window is still held` (fixture `awaiting-input`) | 5 |
| Open session keeps focus through streaming; footer focus does not hold the window | `Open session keeps focus through a streaming update`, `focus on a footer link does not hold the preview window` | 6 |
| Selected preview survives finalisation: body stays visible, selection intact, explicit summary control | `selected preview text survives finalisation (no hidden body, no cleared selection)`, `status updates and an explicit control offers the summary`, `deliberate control collapses to the durable summary` | 8 |
| Explicit expansion freezes the reading window; New activity applies detail | `explicit expansion freezes the reading window; New activity offered`, `New activity applies the withheld detail and stays expanded` | 6 |
| Selected tool detail and focused links inside the body (light DOM and shadow root) survive streaming | `selected tool detail inside the body survives streaming`, `focused link inside the body survives streaming` | 6 |

Additional regressions from review of `918de9f`, in both standard and custom-element bodies:

- `contiguous detail stays held without false gap recovery or lease churn`, `latest held detail is reachable after several contiguous frames`: receipt sequence is independent of the shown window.
- `outage releases the lease without clearing selected detail`, `service return preserves selection without silently reacquiring detail`, `explicit reselection reconnects after an outage`: transport cleanup preserves the reading window.

Owner: JOB-0093 kit/conformance; JOB-0050 integration.

## Execution / freshness / availability — `state-matrix`

Fixtures: `pending`, `live`, `awaiting-input`, `tool-failure`, `cancelled`, `stale`, `unknown-version`, `duplicate-older`, `detail-gap-reset`. Unit `the scenario set covers every required execution/freshness/availability state` asserts coverage.

| UX state | Fixture | Check |
| --- | --- | --- |
| Pending (queued / starting) | `pending` | `queued`, `starting` |
| Running, no activity | `pending` frame 3 | `running with honest 'no activity yet'` |
| Awaiting input → running | `awaiting-input` | `awaiting input callout outside the clipped body`, `no approval or reply controls`, `awaiting input → running clears the callout` |
| Failed tool then failed session | `tool-failure` | `a failed tool alone leaves the session running`, `failed with prominent sanitized error` (unit: `a failed tool alone does not make the session failed`) |
| Cancelled | `cancelled` | `cancelled is explicit and never green` |
| Completed | `live` end | `completed status label`, `known duration shown, no invented metrics`, `published plan reference rendered as a safe https link` |
| Disconnected → stale → rehydrated | `stale` | `disconnected keeps last-known execution and content`, `stale after TTL, no inferred failure`, `empty live cache means awaiting rehydration`, `rehydration replaces the projection without a second card` |
| Duplicate / older / newer revision | `duplicate-older` | `duplicate and older revisions ignored`, `newer revision applied` |
| Accepted payload is stored apart from incoming frames: remount and preference updates never replay a rejected frame | `duplicate-older` (advance to 5, 5, 3, then switch body/theme) | `the accepted payload stays at revision 5 after duplicate and older frames`, `remount is served from the accepted payload, not the rejected older frame`, `preference update is served from the accepted payload` |
| Task fields and availability are delivered with an unchanged data revision | `live` frame 4 plus a synthetic same-revision `missing_service` frame with a new task title (in-memory only) | `availability change is delivered even when the data revision is unchanged`, `task field change is delivered with an unchanged data revision`, `duplicate data counted as ignored while metadata is applied`, `content stays at the accepted revision during the metadata-only update` |
| Unknown version | `unknown-version` | `unknown version shows saved record, never assumes running` (unit: `the adapter never presents an unknown version or enum as running`) |
| Detail gap and reset | `detail-gap-reset` | `detail gap pauses and requests a snapshot`, `reset snapshot resumes detail` |

Owner: JOB-0093 generic durable/live boundary; JOB-0050 authoritative session semantics.

## Terminal fallback — `terminal-fallback`

Fixtures: `missing-service`, `plugin-removed`.

| Requirement | Check |
| --- | --- |
| Saved summary survives missing service; Retry read-only | `missing service keeps the saved summary` |
| Removed plugin renders the durable record with no plugin code | `removed plugin renders the durable record with no plugin body` |
| Open session link comes from the durable record | `Open session link remains from the durable record` |

## Common anatomy and custom body — `anatomy`

Fixtures: `anatomy`, `multi-session` (board), `body=custom-element`.

| Requirement | Check |
| --- | --- |
| Standard and custom bodies share header/notices/body slot/controls/footer | `standard body in the wrapper slot`, `custom-element body shares the identical wrapper…`, `namespaced <demo-session-body> occupies the body slot` |
| Custom body renders the same ordered entries and inherits tokens | `custom body renders ordered entries in its shadow root`, `shadow body inherits the widget text token` |
| No global style injection | `custom body injects no global styles` |
| Board: one meaningful action, no body/transcript/scroll/lease | `board shows one current action`, `board mounts no body, no transcript, no scroll region`, `board holds no detail lease` (unit: `board picks awaiting input, then failed, then active, then latest terminal`) |

Owner: JOB-0093 host/kit/adapter; JOB-0095 consumes.

## Context / snapshot lifecycle — `context-update`, `body-error`

Fixtures: `context-update` (task title change → theme change → identity switch → late result), `body-error`.

| Requirement | Check |
| --- | --- |
| Task fields and preferences update in place, same transaction, no remount | `task title change from the snapshot is visible in the page heading` (strict; the earlier unconditional pass was removed), `document title follows the task, widget label untouched`, `preference change applied in the same transaction`, `data/preference updates preserve the instance and DOM identity` |
| Identity switch retires the instance, releases the lease, removes DOM | `identity switch retires the instance and releases its lease`, `lease had been acquired before retirement`, `retired instance removed from DOM` |
| Late result for a retired generation ignored | `late result for the retired generation is ignored` (strip: `lateResultsIgnored`) |
| Body failure → retire, keep generic fallback, no remount loop | `throwing body is retired and the generic fallback shown`, `body resources released on failure`, `later snapshot does not resurrect the body (no remount loop)`, `footer links remain from the durable record` |

Owner: JOB-0093 versioned API, compatibility, element registry/reload rules.

## Navigation — `history`

Fixture: `live`; routes `/workspaces/demo`, `/workspaces/demo/tasks/DEMO-0042`, `/plugins/dispatch/sessions/demo-s01`, unknown IDs.

| Requirement | Check | Manual |
| --- | --- | --- |
| Open session is a real same-origin anchor, not inside a button | `Open session is a real same-origin anchor`, `the anchor is not nested in a button` | 7 |
| Full session shows originating task context, ordered transcript, user message only here | `full session shows originating task context`, `full session renders the ordered transcript`, `user message appears in the full session only` | 7 |
| Refresh restores context; direct navigation focuses heading once | `refresh on the session URL restores task context and transcript`, `fresh navigation focuses the destination heading once` | 7 |
| Back restores route, expanded card, focus, scroll | `browser Back returns to the task route`, `Back restores the expanded card`, `Back restores focus to the Open session link`, `Back restores scroll position` | 7 |
| Canonical anchors; honest not-found | `Back to task anchor works`, `breadcrumb anchor navigates to the board`, `unknown session ID is an honest not-found…` | 11 |
| Full session mounts one explicit ≤ 200-entry window; earlier entries reachable; pinned window stays bounded during streaming | `long transcript mounts one 200-entry window ending at the latest entry`, `Show earlier offered, Show later hidden at the latest window`, `Show earlier reaches Entry 1 and offers Show later`, `an earlier window stays pinned and bounded while new entries stream`, `Show later returns to the latest window including the streamed entry`, `the latest window follows growth without exceeding 200 mounted entries` (fixture `long-session`; unit `the long-session fixture exceeds the full-session window…`) | 10 |
| Long tool output is revealed in bounded Show more chunks that survive streaming | `long tool output starts at one 2,000-character chunk with Show more`, `Show more reveals the next bounded chunk`, `revealed output survives streaming`, `the final chunk reveals the whole result and ends the Show more path` | 10 |
| Streaming protects selected/focused full-session content and pins a scrolled-up window | `latest-window growth never evicts a selected leading row`, `Jump to latest explicitly releases the held window`, `scrolling up pins the latest window before streaming can evict earlier rows`, `growing assistant text preserves the full-session selection`, `Jump to latest applies the withheld assistant text` | 10 |
| Changing long output does not rebuild the focused Show more button until explicitly requested | `updating long output preserves the focused Show more button until a reader action`, `Show more explicitly applies newer output in a bounded chunk` | 10 |

Owner: JOB-0050, retaining the plugin-prefix routing work.

## Accessible, responsive kit — `theme-density`

Fixtures: `live` with `theme`, `density`, `motion`, `narrow`, `body=custom-element`.

| Requirement | Check | Manual |
| --- | --- | --- |
| Session anchor reachable, ≥44px at 390px; no overflow at 320px or 200% zoom | `session anchor reachable and ≥44px high at 390px`, `no horizontal overflow at 320px`, `no horizontal overflow at 200% zoom` | 4 |
| Dark tokens apply, including inside the shadow root | `dark theme applies Docket dark tokens`, `shadow body inherits dark text token` | 3 |
| Reduced motion removes the pulse | `reduced motion removes the live pulse` | 3 |
| Keyboard use inside Shadow DOM with visible focus | `Shift+Tab from Expand enters the shadow root at its last link`, `keyboard reaches a tool disclosure inside the shadow root in document order`, `Space toggles the shadow-root disclosure`, `focus is visible inside the shadow root` | 6 |
| Text ≥ 4.5:1 and controls/focus ≥ 3:1 in both themes | `tests/contrast.test.ts` (33 pairs) | — |
| Screen-reader usability | **Not automated.** See README "Manual accessibility check". | — |

## Isolation — `no-live-network`

| Requirement | Check |
| --- | --- |
| Static demo traffic only; every request intercepted | request interceptor in `browser.test.ts` fails the run on any off-origin URL, `/api`, WebSocket/EventSource or non-GET method; final `violations` assertion |
| Page cannot connect anywhere | `CSP connect-src 'none' blocks fetch from the page` |
| Server rejects mutation and API routes | `server rejects mutating methods and API/stream paths` |
| No live stores read | `serve.ts` has no filesystem access outside the allowlisted static files; no env vars read except `PORT` |

Coordinator receipt (cross-links into JOB-0050/JOB-0093) is a cockpit-owned acceptance item, recorded on the task, not claimed here.
