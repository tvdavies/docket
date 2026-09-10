// Bounded browser checks for the JOB-0092 fixture prototype.
//
//   CHROMIUM_PATH=/usr/bin/chromium bun tests/browser.test.ts
//
// Starts the loopback fixture server in-process, intercepts every request and
// fails on any non-demo origin, /api path, WebSocket or mutating method. Each
// check block is named after the requirements-to-fixtures identifier it proves.
// Screen-reader usability is NOT claimed here; see README.md for the manual check.

import { chromium, type Route } from "playwright";
import { startServer } from "../prototype/serve";

const server = startServer(0);
const base = `http://127.0.0.1:${server.port}`;
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || undefined, headless: true, args: ["--no-sandbox"] });
const context = await browser.newContext({ viewport: { width: 1440, height: 1100 } });

const requests: string[] = [];
const violations: string[] = [];
await context.route("**/*", (route: Route) => {
  const request = route.request();
  const url = new URL(request.url());
  requests.push(`${request.method()} ${url.pathname}`);
  if (url.origin !== base) violations.push(`off-origin ${request.url()}`);
  if (url.pathname.startsWith("/api") || url.pathname.endsWith("/stream")) violations.push(`api ${url.pathname}`);
  if (request.method() !== "GET" && request.method() !== "HEAD") violations.push(`mutating ${request.method()} ${url.pathname}`);
  if (request.resourceType() === "websocket" || request.resourceType() === "eventsource") violations.push(`stream ${url.pathname}`);
  if (violations.length) return route.abort();
  return route.continue();
});

const page = await context.newPage();
page.on("websocket", (socket) => violations.push(`websocket ${socket.url()}`));
const pageErrors: string[] = [];
page.on("pageerror", (error) => pageErrors.push(error.message));
page.on("console", (message) => { if (message.type() === "error") pageErrors.push(message.text()); });

let passed = 0; let failed = 0;
const check = (ok: unknown, label: string) => { if (ok) { passed += 1; console.log("PASS", label); } else { failed += 1; console.log("FAIL", label); } };
const counter = (name: string) => page.locator(`#counter-${name}`).textContent().then((text) => Number(text));
const advance = () => page.evaluate(() => window.__demo.advance());
const scenario = (id: string, frames = 0) => page.evaluate(([scenarioId, count]) => window.__demo.scenario(scenarioId as string, count as number), [id, frames]);
const task = `${base}/workspaces/demo/tasks/DEMO-0042`;
const widget = () => page.locator('.widget[data-location="activity"][data-instance="demo-s01"]');
const entryTexts = () => widget().locator(".wk-steps > li").evaluateAll((items) => items.map((item) => `${(item as HTMLElement).dataset.type}:${(item as HTMLElement).dataset.status ?? ""}:${item.textContent?.trim()}`));

try {
  // --- task-live-unexpanded ------------------------------------------------
  await page.goto(`${task}?scenario=live&frame=1&paused=1`);
  await widget().waitFor();
  check(page.url() === task, "task-live-unexpanded: one-shot query stripped; URL is the canonical task route");
  const expand = widget().locator(".widget-expand");
  check(await expand.getAttribute("aria-expanded") === "false", "task-live-unexpanded: disclosure starts collapsed");
  const t0 = await entryTexts();
  await advance(); const t1 = await entryTexts();
  await advance(); const t2 = await entryTexts();
  await advance(); const t3 = await entryTexts();
  await advance(); const t4 = await entryTexts();
  check(t0.length === 1 && t1[0].length > t0[0].length && t1[0].startsWith("assistant"), "task-live-unexpanded: assistant text grows in place");
  check(t2.length === 2 && t2[1].startsWith("tool:running"), "task-live-unexpanded: a tool starts in order after the text");
  check(t3[1].startsWith("tool:running") && t3[1].includes("Reading"), "task-live-unexpanded: the tool updates at its original position");
  check(t4[1].startsWith("tool:completed"), "task-live-unexpanded: the tool completes at its original position");
  const toolNodeBefore = await widget().locator('[data-tool-call="t1"]').evaluate((el) => { (el as HTMLElement & { __m?: number }).__m = 42; return true; });
  await advance();
  const toolNodeAfter = await widget().locator('[data-tool-call="t1"]').evaluate((el) => (el as HTMLElement & { __m?: number }).__m === 42);
  check(toolNodeBefore && toolNodeAfter, "task-live-unexpanded: tool row keeps DOM identity across updates");
  check(await expand.getAttribute("aria-expanded") === "false" && page.url() === task, "task-live-unexpanded: still collapsed, URL unchanged, no navigation");
  check(await counter("activeLeases") === 0 && await counter("leasesAcquired") === 0, "task-live-unexpanded: zero detail leases while previewing");
  check(await widget().locator(".wk-freshness").textContent().then((t) => t?.includes("Live")), "task-live-unexpanded: freshness label is separate from execution status");

  // --- preview-bounds ----------------------------------------------------------
  await scenario("live", 10);
  await widget().waitFor();
  const previewCount = await widget().locator(".wk-steps > li").count();
  check(previewCount <= 4, `preview-bounds: preview shows ${previewCount} ≤ 4 entries`);
  check(await widget().locator(".wk-truncation").isVisible(), "preview-bounds: truncation notice shown for earlier activity");
  const html = await page.content();
  check(!html.includes("SECRET-MARKER") && !html.includes("PREVIEW-EXCLUDED-USER-MARKER"), "preview-bounds: private markers absent from the task DOM (filtered, not CSS-hidden)");
  await expand.click();
  check(await expand.getAttribute("aria-expanded") === "true", "preview-bounds: Expand opens detail");
  const expandedCount = await widget().locator(".wk-steps > li").count();
  check(expandedCount <= 12 && expandedCount >= previewCount, `preview-bounds: expanded shows ${expandedCount} ≤ 12 entries`);
  check(await counter("activeLeases") === 1 && await counter("leasesAcquired") === 1, "preview-bounds: exactly one detail lease when expanded");
  check(await widget().locator('.wk-tool-row[aria-expanded="false"]').count() > 0, "preview-bounds: successful tools start collapsed in detail");
  await widget().locator('.wk-tool-row[data-status="completed"]').first().click();
  check(await widget().locator(".wk-tool-detail:not([hidden]) .wk-code").count() > 0, "preview-bounds: tool disclosure shows redacted arguments/result");
  check(!(await page.content()).includes("SECRET-MARKER"), "preview-bounds: private markers absent from expanded detail");
  await expand.click();
  check(await counter("activeLeases") === 0, "preview-bounds: collapsing releases the lease");
  check(await page.evaluate(() => document.activeElement?.classList.contains("wk-disclosure")), "preview-bounds: collapse returns focus to its toggle");

  // --- reader-intent -------------------------------------------------------------
  await scenario("live", 4);
  await widget().waitFor();
  await widget().locator(".wk-steps > li").first().evaluate((el) => { const range = document.createRange(); range.selectNodeContents(el); const selection = window.getSelection()!; selection.removeAllRanges(); selection.addRange(range); });
  const frozen = await entryTexts();
  await advance(); await advance();
  const afterFrozen = await entryTexts();
  check(JSON.stringify(frozen) === JSON.stringify(afterFrozen), "reader-intent: selection freezes the visible preview window");
  check(await widget().locator(".widget-new-activity").isVisible(), "reader-intent: New activity control appears instead of evicting entries");
  await page.evaluate(() => window.getSelection()?.removeAllRanges());
  await widget().locator(".widget-new-activity").click();
  check((await entryTexts()).length > frozen.length, "reader-intent: New activity advances deliberately");
  await page.evaluate(() => window.scrollTo(0, 400));
  const scrollBefore = await page.evaluate(() => window.scrollY);
  await advance();
  check(await page.evaluate(() => window.scrollY) === scrollBefore, "reader-intent: streaming never scrolls the page");
  await expand.focus(); await page.keyboard.press("Enter");
  check(await expand.getAttribute("aria-expanded") === "true", "reader-intent: Enter toggles the disclosure");
  await widget().locator(".wk-tool-row").first().focus();
  await advance();
  check(await page.evaluate(() => document.activeElement?.classList.contains("wk-tool-row")), "reader-intent: focus inside the body survives streaming updates");
  // finalisation with reader engaged: keep expanded, offer Collapse
  await scenario("live", 10); await widget().waitFor();
  await expand.click();
  await advance();
  check(await expand.getAttribute("aria-expanded") === "true" && await widget().locator(".widget-collapse").isVisible(), "reader-intent: explicit expansion survives finalisation; Collapse offered");
  await widget().locator(".widget-collapse").click();
  check(await widget().locator(".widget-summary").isVisible(), "reader-intent: Collapse to summary shows the durable summary");
  // finalisation without engagement: collapse to summary
  await scenario("live", 10); await widget().waitFor();
  await advance();
  check(await widget().locator(".widget-summary").isVisible() && await widget().locator(".widget-summary").textContent().then((t) => t?.includes("Documented the route behavior")), "reader-intent: unattended completion collapses to the durable summary");
  check(await widget().locator(".wk-status.widget-status").textContent().then((t) => t?.trim() === "Completed"), "state-matrix: completed status label");
  check(await widget().locator(".widget-footer-meta").textContent().then((t) => t?.includes("2m 18s")), "state-matrix: known duration shown, no invented metrics");
  check(await widget().locator('.wk-reference[data-ref-kind="plan"]').count() === 1, "state-matrix: published plan reference rendered as a safe https link");

  // --- state-matrix ------------------------------------------------------------
  const statusText = () => widget().locator(".wk-status.widget-status").textContent().then((t) => t?.trim());
  await scenario("pending", 0); await widget().waitFor();
  check(await statusText() === "Queued", "state-matrix: queued");
  await advance(); check(await statusText() === "Starting", "state-matrix: starting");
  await advance(); check(await statusText() === "Running" && await widget().locator(".wk-empty").isVisible(), "state-matrix: running with honest 'no activity yet'");
  await scenario("awaiting-input", 1); await widget().waitFor();
  check(await statusText() === "Awaiting input" && await widget().locator('.wk-notice[data-tone="warning"]').isVisible(), "state-matrix: awaiting input callout outside the clipped body");
  check(await widget().locator("button").filter({ hasText: /approve|reply|allow/i }).count() === 0, "state-matrix: no approval or reply controls");
  await advance(); await advance();
  check(await statusText() === "Running" && await widget().locator('.wk-notice[data-tone="warning"]').count() === 0, "state-matrix: awaiting input → running clears the callout");
  await scenario("tool-failure", 3); await widget().waitFor();
  check(await statusText() === "Running" && await widget().locator('.wk-step[data-status="failed"]').count() === 1, "state-matrix: a failed tool alone leaves the session running");
  await advance(); await advance();
  check(await statusText() === "Failed" && await widget().locator('.wk-notice[data-tone="danger"]').isVisible(), "state-matrix: failed with prominent sanitized error");
  await scenario("cancelled", 2); await widget().waitFor();
  check(await statusText() === "Cancelled" && await widget().getAttribute("data-tone") !== "positive", "state-matrix: cancelled is explicit and never green");
  await scenario("stale", 1); await widget().waitFor();
  check(await statusText() === "Running" && await widget().locator(".wk-freshness").textContent().then((t) => t?.includes("Disconnected")), "state-matrix: disconnected keeps last-known execution and content");
  await advance();
  check(await widget().locator(".wk-freshness").textContent().then((t) => t?.includes("Stale")) && await statusText() === "Running", "state-matrix: stale after TTL, no inferred failure");
  await advance();
  check(await widget().locator(".wk-notice").filter({ hasText: "Awaiting rehydration" }).isVisible(), "state-matrix: empty live cache means awaiting rehydration");
  await advance();
  check(await statusText() === "Running" && await page.locator('.widget[data-instance="demo-s01"]').count() === 1, "state-matrix: rehydration replaces the projection without a second card");
  await scenario("duplicate-older", 0); await widget().waitFor();
  const applied = await counter("snapshotsApplied");
  await advance(); await advance();
  check(await counter("duplicatesIgnored") === 1 && await counter("olderIgnored") === 1 && await counter("snapshotsApplied") === applied, "state-matrix: duplicate and older revisions ignored");
  await advance();
  check(await counter("snapshotsApplied") === applied + 1, "state-matrix: newer revision applied");
  await scenario("unknown-version", 1); await widget().waitFor();
  check(await widget().locator(".wk-notice").filter({ hasText: "Unsupported session data" }).isVisible() && await statusText() !== "Running", "state-matrix: unknown version shows saved record, never assumes running");
  await scenario("detail-gap-reset", 0); await widget().waitFor();
  await expand.click();
  await advance();
  check(await widget().locator(".wk-notice").filter({ hasText: "Detail stream paused" }).isVisible(), "state-matrix: detail gap pauses and requests a snapshot");
  await advance();
  check(await widget().locator(".wk-notice").filter({ hasText: "Detail stream paused" }).isHidden(), "state-matrix: reset snapshot resumes detail");

  // --- terminal-fallback -------------------------------------------------------
  await scenario("missing-service", 1); await widget().waitFor();
  check(await widget().locator(".wk-notice").filter({ hasText: "Plugin service unavailable" }).isVisible() && await widget().locator(".widget-summary").textContent().then((t) => t?.includes("Documented the route behavior")), "terminal-fallback: missing service keeps the saved summary");
  await scenario("plugin-removed", 1); await widget().waitFor();
  check(await widget().locator(".widget-fallback").isVisible() && await widget().locator(".widget-fallback-summary").textContent().then((t) => t?.includes("Documented the route behavior")) && await widget().locator('[data-body]').count() === 0, "terminal-fallback: removed plugin renders the durable record with no plugin body");
  check(await widget().locator('.wk-reference[data-ref-kind="session"]').count() === 1, "terminal-fallback: Open session link remains from the durable record");

  // --- anatomy -----------------------------------------------------------------
  await scenario("anatomy", 0); await widget().waitFor();
  const standardShape = await widget().evaluate((el) => [...el.children].map((child) => child.className.split(" ")[0]));
  check(await widget().locator('[data-body="standard"]').count() === 1, "anatomy: standard body in the wrapper slot");
  await page.selectOption("#body", "custom-element");
  await widget().waitFor();
  const customShape = await widget().evaluate((el) => [...el.children].map((child) => child.className.split(" ")[0]));
  check(JSON.stringify(standardShape) === JSON.stringify(customShape), "anatomy: custom-element body shares the identical wrapper (header/notices/body/controls/footer)");
  check(await widget().locator("demo-session-body[data-body='custom-element']").count() === 1, "anatomy: namespaced <demo-session-body> occupies the body slot");
  check(await widget().locator("demo-session-body").evaluate((el) => Boolean(el.shadowRoot) && el.shadowRoot!.querySelectorAll(".wk-steps > li").length > 0), "anatomy: custom body renders ordered entries in its shadow root");
  check(await widget().locator("demo-session-body").evaluate((el) => getComputedStyle(el.shadowRoot!.querySelector(".wk-step[data-type='assistant']")!).color === getComputedStyle(document.body).color), "anatomy: shadow body inherits the widget text token");
  check(await page.evaluate(() => document.querySelectorAll("style").length === 0 && document.adoptedStyleSheets.length === 0), "anatomy: custom body injects no global styles");
  await page.goto(`${base}/workspaces/demo`); await page.locator('.widget[data-location="board"]').waitFor();
  check(await page.locator('.widget[data-location="board"] .widget-action').textContent().then((t) => t?.includes("Inspect · task navigation")), "anatomy: board shows one current action");
  check(await page.locator('.widget[data-location="board"] .wk-steps').count() === 0 && await page.locator('.widget[data-location="board"] [data-body]').count() === 0, "anatomy: board mounts no body, no transcript, no scroll region");
  check(await counter("leasesAcquired") === 0 || await counter("activeLeases") === 0, "anatomy: board holds no detail lease");
  await page.selectOption("#body", "standard");

  // --- context-update ----------------------------------------------------------
  await page.goto(`${task}?scenario=context-update&paused=1`); await widget().waitFor();
  const mounted = await counter("instancesMounted");
  await widget().locator(".wk-steps > li").first().evaluate((el) => (el as HTMLElement & { __id?: string }).__id = "keep");
  await advance();
  check(await page.locator("h1#view-heading").textContent().then((t) => t?.includes("(renamed)")), "context-update: task title change from the snapshot is visible in the page heading");
  check(await widget().locator(".widget-label").textContent().then((t) => t?.includes("Planner")) && await page.evaluate(() => document.title.startsWith("DEMO-0042")), "context-update: document title follows the task, widget label untouched");
  await advance();
  check(await page.evaluate(() => document.documentElement.dataset.theme === "dark" && document.documentElement.dataset.density === "compact"), "context-update: preference change applied in the same transaction");
  check(await counter("instancesMounted") === mounted && await widget().locator(".wk-steps > li").first().evaluate((el) => (el as HTMLElement & { __id?: string }).__id === "keep"), "context-update: data/preference updates preserve the instance and DOM identity");
  await expand.click();
  const leases = await counter("leasesAcquired");
  await page.evaluate(() => window.__demo.host.advance());
  check(await counter("instancesRetired") >= 1 && await counter("activeLeases") === 0, "context-update: identity switch retires the instance and releases its lease");
  await page.evaluate(() => window.__demo.flushLate());
  check(await counter("lateResultsIgnored") >= 1, "context-update: late result for the retired generation is ignored");
  check(leases >= 1, "context-update: lease had been acquired before retirement");
  check(await page.locator('.widget[data-instance="demo-s01"]').count() === 0, "context-update: retired instance removed from DOM");
  await page.selectOption("#theme", "light"); await page.selectOption("#density", "comfortable");

  // --- body-error --------------------------------------------------------------
  await scenario("body-error", 0); await widget().waitFor();
  const cleanups = await counter("bodyCleanups");
  await advance();
  check(await counter("bodyErrors") === 1 && await widget().locator(".widget-fallback").isVisible(), "body-error: throwing body is retired and the generic fallback shown");
  check(await counter("bodyCleanups") === cleanups + 1, "body-error: body resources released on failure");
  await advance();
  check(await counter("bodyErrors") === 1 && await widget().locator("[data-body]").count() === 0, "body-error: later snapshot does not resurrect the body (no remount loop)");
  check(await widget().locator('.wk-reference[data-ref-kind="session"]').count() === 1, "body-error: footer links remain from the durable record");

  // --- history -----------------------------------------------------------------
  await page.setViewportSize({ width: 1440, height: 700 }); // short enough that the task page scrolls
  await scenario("live", 8); await widget().waitFor();
  await expand.click();
  const open = widget().locator('.widget-footer .wk-reference[data-ref-kind="session"]');
  check(await open.getAttribute("href") === "/plugins/dispatch/sessions/demo-s01", "history: Open session is a real same-origin anchor");
  check(await open.evaluate((el) => el.tagName === "A" && !el.closest("button")), "history: the anchor is not nested in a button");
  await page.evaluate(() => window.scrollTo(0, 250));
  await open.focus(); await page.keyboard.press("Enter");
  await page.waitForURL(`${base}/plugins/dispatch/sessions/demo-s01`);
  check(await page.locator("h1#view-heading").textContent().then((t) => t?.includes("Make session progress")), "history: full session shows originating task context");
  check(await page.evaluate(() => document.activeElement?.id === "view-heading"), "history: fresh navigation focuses the destination heading once");
  check(await page.locator(".transcript > li").count() >= 5, "history: full session renders the ordered transcript");
  check(await page.locator('.transcript [data-type="user"]').count() === 1, "history: user message appears in the full session only");
  await page.reload(); await page.locator(".transcript > li").first().waitFor();
  check(await page.locator("h1#view-heading").textContent().then((t) => t?.includes("Make session progress")) && await page.locator(".transcript > li").count() >= 5, "history: refresh on the session URL restores task context and transcript");
  await page.goBack(); await widget().waitFor();
  check(page.url() === task, "history: browser Back returns to the task route");
  check(await expand.getAttribute("aria-expanded") === "true", "history: Back restores the expanded card");
  check(await page.evaluate(() => document.activeElement?.classList.contains("wk-reference")), "history: Back restores focus to the Open session link");
  check(Math.abs(await page.evaluate(() => window.scrollY) - 250) < 5, "history: Back restores scroll position");
  await page.locator(".crumb a").first().click(); await page.locator(".task-card").first().waitFor();
  check(page.url() === `${base}/workspaces/demo`, "history: breadcrumb anchor navigates to the board");
  await page.goto(`${base}/plugins/dispatch/sessions/nope`);
  check(await page.locator("h1#view-heading").textContent().then((t) => t?.includes("not found")) && await page.locator('#app a[href="/workspaces/demo"]').count() >= 1 && !(await page.locator("#app").textContent())?.includes("DEMO-0042"), "history: unknown session ID is an honest not-found with a workspace link, no guessed task");
  await page.goto(`${base}/plugins/dispatch/sessions/demo-s01`); await page.locator(".transcript > li").first().waitFor();
  await page.locator(".back-to-task").click(); await widget().waitFor();
  check(page.url() === task, "history: Back to task anchor works");
  await page.setViewportSize({ width: 1440, height: 1100 });

  // --- theme-density -----------------------------------------------------------
  const narrowContext = await browser.newContext({ viewport: { width: 390, height: 844 } });
  const narrow = await narrowContext.newPage();
  await narrow.goto(`${task}?scenario=live&frame=8&paused=1`);
  await narrow.locator(".widget").waitFor();
  const overflow = await narrow.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  check(overflow <= 0, `theme-density: no horizontal overflow at 390px (${overflow}px)`);
  check(await narrow.locator('.widget-footer .wk-reference[data-ref-kind="session"]').evaluate((el) => { const box = el.getBoundingClientRect(); return box.height >= 44 && box.right <= window.innerWidth; }), "theme-density: session anchor reachable and ≥44px high at 390px");
  await narrow.setViewportSize({ width: 320, height: 700 });
  check(await narrow.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth) <= 0, "theme-density: no horizontal overflow at 320px");
  await narrowContext.close();
  await page.goto(`${task}?scenario=live&frame=8&paused=1&theme=dark&body=custom-element&motion=reduced`); await widget().waitFor();
  check(await page.evaluate(() => document.documentElement.dataset.theme === "dark" && getComputedStyle(document.body).backgroundColor === "rgb(13, 15, 20)"), "theme-density: dark theme applies Docket dark tokens");
  check(await widget().locator("demo-session-body").evaluate((el) => getComputedStyle(el.shadowRoot!.querySelector(".wk-step[data-type='assistant']")!).color === "rgb(242, 244, 248)"), "theme-density: shadow body inherits dark text token");
  check(await widget().locator(".wk-freshness").evaluate((el) => getComputedStyle(el.querySelector("i")!).animationDuration === "0s"), "theme-density: reduced motion removes the live pulse");
  const zoomed = await browser.newContext({ viewport: { width: 720, height: 1100 }, deviceScaleFactor: 2 });
  const zoomPage = await zoomed.newPage();
  await zoomPage.goto(`${task}?scenario=live&frame=8&paused=1`); await zoomPage.locator(".widget").waitFor();
  await zoomPage.evaluate(() => { document.documentElement.style.fontSize = "200%"; document.body.style.zoom = "2"; });
  check(await zoomPage.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth) <= 0, "theme-density: no horizontal overflow at 200% zoom");
  await zoomed.close();

  // --- keyboard inside the shadow root -----------------------------------------
  await page.goto(`${task}?scenario=live&frame=10&paused=1&body=custom-element`); await widget().waitFor();
  await expand.click();
  await expand.focus();
  const shadowActive = () => page.evaluate(() => { const host = document.querySelector("demo-session-body"); return document.activeElement === host ? host?.shadowRoot?.activeElement?.className ?? "" : "outside"; });
  await page.keyboard.press("Shift+Tab"); // body precedes controls in document order
  check((await shadowActive()).includes("wk-reference"), "theme-density: Shift+Tab from Expand enters the shadow root at its last link");
  let steps = 0; while (steps < 6 && !(await shadowActive()).includes("wk-tool-row")) { await page.keyboard.press("Shift+Tab"); steps += 1; }
  check((await shadowActive()).includes("wk-tool-row"), "theme-density: keyboard reaches a tool disclosure inside the shadow root in document order");
  await page.keyboard.press("Space");
  check(await page.evaluate(() => document.querySelector("demo-session-body")?.shadowRoot?.activeElement?.getAttribute("aria-expanded") === "true"), "theme-density: Space toggles the shadow-root disclosure");
  check(await page.evaluate(() => { const el = document.querySelector("demo-session-body")?.shadowRoot?.activeElement as HTMLElement; return getComputedStyle(el).outlineStyle !== "none"; }), "theme-density: focus is visible inside the shadow root");

  // --- reader-intent (review findings 1 and 2) -----------------------------------
  const selectFirstAssistant = async (shadow = false) => {
    if (shadow) await widget().locator("demo-session-body").evaluate((host) => { const el = host.shadowRoot!.querySelector('.wk-step[data-type="assistant"]')!; const range = document.createRange(); range.selectNodeContents(el); const selection = window.getSelection()!; selection.removeAllRanges(); selection.addRange(range); });
    else await widget().locator('.wk-step[data-type="assistant"]').first().evaluate((el) => { const range = document.createRange(); range.selectNodeContents(el); const selection = window.getSelection()!; selection.removeAllRanges(); selection.addRange(range); });
  };
  const selectedText = () => page.evaluate(() => window.getSelection()?.toString() ?? "");
  for (const body of ["standard", "custom-element"] as const) {
    // Input notice independent of the held preview
    await page.goto(`${task}?scenario=awaiting-input&frame=0&paused=1&body=${body}`); await widget().waitFor();
    await selectFirstAssistant(body === "custom-element");
    const before = await selectedText();
    await advance();
    check(before.length > 0 && await widget().locator('.widget-notices .wk-notice[data-tone="warning"]').isVisible() && await widget().locator(".wk-status.widget-status").textContent().then((t) => t?.trim() === "Awaiting input"), `reader-intent[${body}]: input notice appears while preview text is selected`);
    check(await selectedText() === before && (await entryTexts()).length === 1, `reader-intent[${body}]: selection survives the input transition (entries unchanged, nothing withheld)`);
    await advance(); await advance();
    check(await widget().locator('.widget-notices .wk-notice[data-tone="warning"]').count() === 0 && await widget().locator(".wk-status.widget-status").textContent().then((t) => t?.trim() === "Running"), `reader-intent[${body}]: resolved input notice clears while the window is still held`);
    // Open session focus survives streaming
    await page.goto(`${task}?scenario=live&frame=4&paused=1&body=${body}`); await widget().waitFor();
    await widget().locator('.widget-footer a[data-ref-kind="session"]').focus();
    await advance();
    check(await page.evaluate(() => document.activeElement?.id) === "w-activity-demo-s01-open", `reader-intent[${body}]: Open session keeps focus through a streaming update`);
    check(await widget().locator(".widget-new-activity").isHidden() && (await entryTexts())[1]?.startsWith("tool:completed"), `reader-intent[${body}]: focus on a footer link does not hold the preview window`);
    // Selected preview survives finalisation
    await page.goto(`${task}?scenario=live&frame=10&paused=1&body=${body}`); await widget().waitFor();
    await selectFirstAssistant(body === "custom-element");
    const selectedBefore = await selectedText();
    await advance();
    check(await widget().locator(".widget-body-slot").isVisible() && await selectedText() === selectedBefore && selectedBefore.length > 0, `reader-intent[${body}]: selected preview text survives finalisation (no hidden body, no cleared selection)`);
    check(await widget().locator(".wk-status.widget-status").textContent().then((t) => t?.trim() === "Completed") && await widget().locator(".widget-new-activity").textContent().then((t) => t?.includes("show summary")), `reader-intent[${body}]: status updates and an explicit control offers the summary`);
    await page.evaluate(() => window.getSelection()?.removeAllRanges());
    await widget().locator(".widget-new-activity").click();
    check(await widget().locator(".widget-summary").isVisible() && await widget().locator(".widget-new-activity").isHidden(), `reader-intent[${body}]: deliberate control collapses to the durable summary`);
    // Explicit expansion freezes the reading window
    await page.goto(`${task}?scenario=live&frame=4&paused=1&body=${body}`); await widget().waitFor();
    await expand.click();
    const rowsBefore = await entryTexts();
    await advance();
    check(JSON.stringify(await entryTexts()) === JSON.stringify(rowsBefore) && await widget().locator(".widget-new-activity").isVisible(), `reader-intent[${body}]: explicit expansion freezes the reading window; New activity offered`);
    await widget().locator(".widget-new-activity").click();
    check(JSON.stringify(await entryTexts()) !== JSON.stringify(rowsBefore) && (await entryTexts())[1]?.startsWith("tool:completed") && await expand.getAttribute("aria-expanded") === "true" && await widget().locator(".widget-new-activity").isHidden(), `reader-intent[${body}]: New activity applies the withheld detail and stays expanded`);
    // Selected tool output / link inside the expanded body
    await page.goto(`${task}?scenario=live&frame=6&paused=1&body=${body}`); await widget().waitFor();
    await expand.click();
    await widget().locator('.wk-tool-row[data-status="completed"]').first().click();
    const selectCode = body === "custom-element"
      ? widget().locator("demo-session-body").evaluate((host) => { const el = host.shadowRoot!.querySelector(".wk-tool-detail:not([hidden]) .wk-code")!; const range = document.createRange(); range.selectNodeContents(el); const selection = window.getSelection()!; selection.removeAllRanges(); selection.addRange(range); })
      : widget().locator(".wk-tool-detail:not([hidden]) .wk-code").first().evaluate((el) => { const range = document.createRange(); range.selectNodeContents(el); const selection = window.getSelection()!; selection.removeAllRanges(); selection.addRange(range); });
    await selectCode;
    const codeBefore = await selectedText();
    await advance(); await advance();
    check(codeBefore.includes("sessionProjection.ts") && await selectedText() === codeBefore, `reader-intent[${body}]: selected tool detail inside the body survives streaming`);
    await page.evaluate(() => window.getSelection()?.removeAllRanges());
    const moreLink = body === "custom-element" ? widget().locator("demo-session-body") : widget().locator(".wk-more a");
    if (body === "custom-element") await moreLink.evaluate((host) => (host.shadowRoot!.querySelector(".wk-more a") as HTMLElement).focus()); else await moreLink.focus();
    await advance();
    check(await page.evaluate(() => { const host = document.querySelector("demo-session-body"); const active = host && document.activeElement === host ? host.shadowRoot?.activeElement : document.activeElement; return active?.classList.contains("wk-reference") && active.closest(".wk-more") !== null; }), `reader-intent[${body}]: focused link inside the body survives streaming`);
  }

  // --- state-matrix: accepted payload storage (review finding 3) ----------------
  await page.goto(`${task}?scenario=duplicate-older&frame=0&paused=1`); await widget().waitFor();
  await advance(); // duplicate revision 5
  await advance(); // older revision 3
  const acceptedAfterOlder = await page.evaluate(() => { const s = window.__demo.host.snapshotFor("demo-s01"); return { revision: s?.data?.revision, action: s?.data?.value.currentAction }; });
  check(acceptedAfterOlder.revision === 5 && acceptedAfterOlder.action === "Read · web/src/sessionProjection.ts", "state-matrix: the accepted payload stays at revision 5 after duplicate and older frames");
  await page.selectOption("#body", "custom-element"); await widget().waitFor();
  check(await widget().locator("demo-session-body").evaluate((el) => el.shadowRoot!.querySelectorAll('.wk-step[data-type="tool"]').length) === 1, "state-matrix: remount is served from the accepted payload, not the rejected older frame");
  await page.selectOption("#theme", "dark");
  check(await widget().locator("demo-session-body").evaluate((el) => el.shadowRoot!.querySelectorAll('.wk-step[data-type="tool"]').length) === 1, "state-matrix: preference update is served from the accepted payload");
  await page.selectOption("#theme", "light"); await page.selectOption("#body", "standard"); await widget().waitFor();
  await page.goto(`${task}?scenario=live&frame=4&paused=1`); await widget().waitFor();
  const metadataBefore = await counter("metadataApplied"); const appliedBefore = await counter("snapshotsApplied");
  await page.evaluate(() => { const host = window.__demo.host; const state = host.sessions.get("demo-s01")!; state.fixture.frames[state.frameIndex + 1] = { ...state.fixture.frames[state.frameIndex], revision: state.revision, availability: "missing_service", taskTitle: "Unchanged-revision title" }; host.advance(); });
  check(await widget().locator(".wk-notice").filter({ hasText: "Plugin service unavailable" }).isVisible(), "state-matrix: availability change is delivered even when the data revision is unchanged");
  check(await page.locator("h1#view-heading").textContent().then((t) => t?.includes("Unchanged-revision title")), "state-matrix: task field change is delivered with an unchanged data revision");
  check(await counter("duplicatesIgnored") >= 1 && await counter("snapshotsApplied") === appliedBefore && await counter("metadataApplied") > metadataBefore, "state-matrix: duplicate data counted as ignored while metadata is applied");
  check((await entryTexts()).length === 2 && (await entryTexts())[1]?.startsWith("tool:running"), "state-matrix: content stays at the accepted revision during the metadata-only update");

  // --- preview-bounds: view-scoped detail selection (review finding 4) ------------
  await page.goto(`${task}?scenario=multi-session&frame=0&paused=1`); await page.locator(".widget").nth(2).waitFor();
  const expands = page.locator('.widget[data-location="activity"] .widget-expand');
  await expands.nth(0).click(); await expands.nth(1).click(); await expands.nth(2).click();
  check(await counter("activeLeases") === 1 && await counter("leasesRevoked") === 2, "preview-bounds: expanding three cards keeps one detail selection per task view");
  check(await expands.evaluateAll((els) => els.map((el) => el.getAttribute("aria-expanded"))).then((values) => JSON.stringify(values) === JSON.stringify(["false", "false", "true"])), "preview-bounds: reselection collapses the previously selected cards");
  check(await page.locator(".widget-control-note:not([hidden])").count() === 2 && await page.locator(".widget-control-note:not([hidden])").first().textContent().then((t) => t?.includes("one session at a time")), "preview-bounds: revoked cards explain why their detail closed");
  await expands.nth(0).click();
  check(await counter("activeLeases") === 1 && await expands.nth(2).getAttribute("aria-expanded") === "false" && await expands.nth(0).getAttribute("aria-expanded") === "true", "preview-bounds: selecting back moves the single selection");
  await page.goto(`${task}?scenario=unknown-version&frame=0&paused=1`); await widget().waitFor();
  await expand.click();
  check(await counter("activeLeases") === 1, "preview-bounds: supported data can be expanded before the unsupported frame");
  await advance();
  check(await counter("activeLeases") === 0 && await widget().locator("[data-body]").count() === 0 && await widget().locator(".widget-summary").isVisible(), "preview-bounds: unsupported version releases the body and its detail selection");
  check(await counter("bodyCleanups") >= 1 && await counter("bodyErrors") === 0, "preview-bounds: unsupported data is a release, not a body failure");
  await page.goto(`${task}?scenario=missing-service&frame=0&paused=1`); await widget().waitFor();
  await expand.click();
  check(await counter("activeLeases") === 1 && await expand.getAttribute("aria-expanded") === "true", "preview-bounds: completed card can be expanded while the service is available");
  await advance();
  check(await counter("activeLeases") === 0 && await expand.getAttribute("aria-expanded") === "true" && await expand.isEnabled(), "preview-bounds: missing service releases transport but retains last-known expanded detail and usable Collapse");
  check(await widget().locator(".widget-body-slot").isVisible() && await widget().locator(".wk-notice").filter({ hasText: "Plugin service unavailable" }).isVisible(), "preview-bounds: last-known detail and availability notice remain while unavailable");
  await expand.click();
  check(await expand.isDisabled() && await widget().locator(".widget-summary").textContent().then((t) => t?.includes("Documented the route behavior")), "preview-bounds: explicit Collapse keeps the saved summary and cannot reopen unavailable detail");
  await advance();
  check(await expand.isEnabled() && await counter("activeLeases") === 0, "preview-bounds: service return re-enables Expand without re-acquiring detail on its own");
  await expand.click();
  check(await counter("activeLeases") === 1 && await counter("leasesDeclined") === 0, "preview-bounds: detail can be reselected after the service returns");

  // --- history: bounded, navigable full-session window (review finding 5) --------
  await page.goto(`${base}/plugins/dispatch/sessions/demo-s01?scenario=long-session&frame=0&paused=1`); await page.locator(".transcript > li").first().waitFor();
  const rows = () => page.locator(".transcript > li").count();
  const firstRow = () => page.locator(".transcript > li").first().textContent();
  check(await rows() === 200 && await page.locator(".transcript-window").textContent().then((t) => t?.includes("7–206 of 206")), "history: long transcript mounts one 200-entry window ending at the latest entry");
  check(await page.locator(".show-earlier").isVisible() && await page.locator(".show-later").isHidden(), "history: Show earlier offered, Show later hidden at the latest window");
  await page.getByRole("button", { name: "Show earlier", exact: true }).click();
  check(await firstRow().then((t) => t?.startsWith("Entry 1") && t !== "Entry 10") && await rows() <= 200 && await page.locator(".show-earlier").isHidden() && await page.locator(".show-later").isVisible(), "history: Show earlier reaches Entry 1 and offers Show later");
  await advance();
  check(await rows() <= 200 && await firstRow().then((t) => t?.startsWith("Entry 1")), "history: an earlier window stays pinned and bounded while new entries stream");
  await page.getByRole("button", { name: "Show later", exact: true }).click();
  check(await rows() === 200 && await page.locator(".transcript > li").last().textContent().then((t) => t?.includes("Entry 206")), "history: Show later returns to the latest window including the streamed entry");
  await page.locator(".transcript-viewport").focus(); // leave the earlier/later destination row before following
  await advance();
  check(await rows() === 200 && await page.locator(".transcript > li").last().textContent().then((t) => t?.includes("Entry 207")), "history: the latest window follows growth without exceeding 200 mounted entries");
  await page.locator('.transcript [data-tool-call="t-long"] .wk-tool-row').click();
  const codeLength = () => page.locator('.transcript [data-tool-call="t-long"] .wk-code').last().evaluate((el) => el.textContent?.length ?? 0);
  check(await codeLength() === 2000 && await page.locator(".wk-show-more").textContent().then((t) => t?.includes("3,000 characters remaining")), "history: long tool output starts at one 2,000-character chunk with Show more");
  await page.locator(".wk-show-more").click();
  check(await codeLength() === 4000 && await page.locator(".wk-show-more").textContent().then((t) => t?.includes("1,000 characters remaining")), "history: Show more reveals the next bounded chunk");
  await advance();
  check(await codeLength() === 4000, "history: revealed output survives streaming");
  await page.locator(".wk-show-more").click();
  check(await codeLength() === 5000 && await page.locator(".wk-show-more").count() === 0 && await page.locator(".wk-output-end").isVisible(), "history: the final chunk reveals the whole result and ends the Show more path");

  // --- review follow-up: continuity is independent of the held reading window ---
  for (const body of ["standard", "custom-element"] as const) {
    await page.goto(`${task}?scenario=live&frame=4&paused=1&body=${body}`); await widget().waitFor();
    await expand.click();
    const heldRows = await entryTexts();
    const acquired = await counter("leasesAcquired");
    await advance(); await advance(); await advance();
    check(await counter("leasesAcquired") === acquired && await counter("activeLeases") === 1 && JSON.stringify(await entryTexts()) === JSON.stringify(heldRows), `reader-intent[${body}]: contiguous detail stays held without false gap recovery or lease churn`);
    await widget().locator(".widget-new-activity").click();
    check(JSON.stringify(await entryTexts()) !== JSON.stringify(heldRows), `reader-intent[${body}]: latest held detail is reachable after several contiguous frames`);

    await page.goto(`${task}?scenario=missing-service&frame=0&paused=1&body=${body}`); await widget().waitFor();
    await expand.click(); await widget().locator(".wk-tool-row").first().click();
    await widget().locator(".wk-code").first().evaluate((el) => { const r = document.createRange(); r.selectNodeContents(el); const s = window.getSelection()!; s.removeAllRanges(); s.addRange(r); });
    const selected = await selectedText();
    await advance();
    check(selected.length > 0 && await selectedText() === selected && await widget().locator(".widget-body-slot").isVisible() && await counter("activeLeases") === 0, `reader-intent[${body}]: outage releases the lease without clearing selected detail`);
    await advance();
    check(await selectedText() === selected && await counter("activeLeases") === 0, `reader-intent[${body}]: service return preserves selection without silently reacquiring detail`);
    await page.evaluate(() => window.getSelection()?.removeAllRanges());
    await expand.click(); await expand.click();
    check(await counter("activeLeases") === 1, `reader-intent[${body}]: explicit reselection reconnects after an outage`);
  }

  // Full-session engagement protects both the window's keys and its text nodes.
  await page.goto(`${base}/plugins/dispatch/sessions/demo-s01?scenario=long-session&frame=0&paused=1`);
  await page.locator(".transcript > li").first().waitFor();
  await page.locator(".transcript > li").first().evaluate((el) => { const r = document.createRange(); r.selectNodeContents(el); const s = window.getSelection()!; s.removeAllRanges(); s.addRange(r); });
  const firstSelected = await selectedText();
  await advance();
  check(firstSelected === "Entry 7" && await selectedText() === firstSelected && await firstRow() === firstSelected && await rows() === 200, "history: latest-window growth never evicts a selected leading row");
  await page.evaluate(() => window.getSelection()?.removeAllRanges());
  await page.locator(".jump-latest").click();
  check(await firstRow() === "Entry 8" && await rows() === 200, "history: Jump to latest explicitly releases the held window");
  await page.locator(".transcript-viewport").evaluate((el) => { el.scrollTop = 0; el.dispatchEvent(new Event("scroll")); });
  await advance();
  check(await firstRow() === "Entry 8" && await rows() === 200 && await page.locator(".jump-latest").isVisible(), "history: scrolling up pins the latest window before streaming can evict earlier rows");

  await page.goto(`${base}/plugins/dispatch/sessions/demo-s01?scenario=live&frame=1&paused=1`);
  await page.locator('.transcript [data-type="assistant"]').first().waitFor();
  await page.locator('.transcript [data-type="assistant"]').first().evaluate((el) => { const r = document.createRange(); r.selectNodeContents(el); const s = window.getSelection()!; s.removeAllRanges(); s.addRange(r); });
  const assistantSelected = await selectedText();
  await advance();
  check(assistantSelected.length > 0 && await selectedText() === assistantSelected, "history: growing assistant text preserves the full-session selection");
  await page.evaluate(() => window.getSelection()?.removeAllRanges()); await page.locator(".jump-latest").click();
  check((await page.locator('.transcript [data-type="assistant"]').first().textContent())!.length > assistantSelected.length, "history: Jump to latest applies the withheld assistant text");

  await page.goto(`${base}/plugins/dispatch/sessions/demo-s01?scenario=long-session&frame=0&paused=1`);
  await page.locator('.transcript [data-tool-call="t-long"] .wk-tool-row').click();
  await page.locator(".wk-show-more").focus();
  await page.evaluate(() => { const host = window.__demo.host; const s = host.sessions.get("demo-s01")!; const seq = s.fixture.source.events.length + 1; s.fixture.source.events.push({ seq, type: "tool.updated", toolCallId: "t-long", status: "completed", output: "X".repeat(6500) }); s.fixture.frames[s.frameIndex + 1] = { through: seq, execution: "running" }; host.advance(); });
  check(await page.locator(".wk-show-more").evaluate((el) => el === document.activeElement) && await codeLength() === 2000, "history: updating long output preserves the focused Show more button until a reader action");
  await page.locator(".wk-show-more").click();
  check(await codeLength() === 4000 && await page.locator(".wk-show-more").textContent().then((t) => t?.includes("2,500")), "history: Show more explicitly applies newer output in a bounded chunk");

  // --- no-live-network ---------------------------------------------------------
  check(violations.length === 0, `no-live-network: ${requests.length} requests, ${violations.length} violations ${violations.join("; ")}`);
  const allowed = new Set(requests.map((line) => line.split(" ")[1]).filter((path) => !/^\/(workspaces\/demo(\/tasks\/[A-Z0-9-]+)?|plugins\/dispatch\/sessions\/[^/]+|app\.js|demo\.css|components\/(tokens|kit)\.css)$/.test(path)));
  check(allowed.size === 0, `no-live-network: only demo routes and static assets requested (${[...allowed].join(", ") || "none unexpected"})`);
  check(pageErrors.length === 0, `no-live-network: no page errors (${pageErrors.join("; ")})`);
  const csp = await page.evaluate(async () => (await fetch("/").catch(() => null)) === null);
  check(csp, "no-live-network: CSP connect-src 'none' blocks fetch from the page");
  const post = await fetch(`${base}/api/anything`, { method: "POST" });
  const ws = await fetch(`${base}/workspaces/demo/stream`);
  check(post.status === 405 && ws.status === 404, "no-live-network: server rejects mutating methods and API/stream paths");
} finally {
  console.log(`\n${passed} passed, ${failed} failed`);
  await browser.close();
  server.stop(true);
  if (failed > 0) process.exit(1);
}
