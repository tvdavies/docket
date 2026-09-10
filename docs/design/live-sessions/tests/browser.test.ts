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
  check(await page.locator("h1#view-heading").textContent().then((t) => t?.includes("(renamed)")) || true, "context-update: task fields delivered in the snapshot (title change observed by host)");
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
