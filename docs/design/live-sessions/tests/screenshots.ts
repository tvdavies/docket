// Captures the committed evidence screenshots from pinned fixture frames.
//
//   CHROMIUM_PATH=/usr/bin/chromium bun tests/screenshots.ts
//
// Output goes to ../screenshots/*.png. Every capture starts from a pinned
// frame with autoplay paused, so re-running yields the same states.

import { chromium } from "playwright";
import { mkdirSync } from "node:fs";
import { resolve } from "node:path";
import { startServer } from "../prototype/serve";

const server = startServer(0);
const base = `http://127.0.0.1:${server.port}`;
const out = resolve(import.meta.dir, "../screenshots");
mkdirSync(out, { recursive: true });
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || undefined, headless: true, args: ["--no-sandbox"] });

interface Shot { name: string; path: string; width: number; height: number; before?(page: import("playwright").Page): Promise<void> }

const task = "/workspaces/demo/tasks/DEMO-0042";
const shots: Shot[] = [
  { name: "task-live-desktop-light", path: `${task}?scenario=live&frame=8&paused=1`, width: 1440, height: 1000 },
  { name: "task-live-phone-light", path: `${task}?scenario=live&frame=8&paused=1`, width: 390, height: 1100 },
  { name: "task-live-desktop-dark-custom", path: `${task}?scenario=live&frame=8&paused=1&theme=dark&body=custom-element`, width: 1440, height: 1000 },
  { name: "task-live-phone-dark-custom", path: `${task}?scenario=live&frame=8&paused=1&theme=dark&body=custom-element`, width: 390, height: 1100 },
  { name: "task-expanded-desktop", path: `${task}?scenario=live&frame=10&paused=1`, width: 1440, height: 1100, before: async (page) => { await page.locator(".widget-expand").click(); await page.locator('.wk-tool-row[data-status="completed"]').first().click(); } },
  { name: "task-held-completed-desktop", path: `${task}?scenario=live&frame=10&paused=1`, width: 1440, height: 1000, before: async (page) => {
    await page.locator('.widget .wk-step[data-type="assistant"]').first().evaluate((el) => { const range = document.createRange(); range.selectNodeContents(el); const selection = window.getSelection()!; selection.removeAllRanges(); selection.addRange(range); });
    await page.evaluate(() => window.__demo.advance());
  } },
  { name: "task-multi-session-desktop", path: `${task}?scenario=multi-session&paused=1`, width: 1440, height: 1200 },
  { name: "task-multi-session-reselected-desktop", path: `${task}?scenario=multi-session&paused=1`, width: 1440, height: 1300, before: async (page) => { for (const el of await page.locator(".widget-expand").all()) await el.click(); } },
  { name: "session-long-window-desktop", path: `/plugins/dispatch/sessions/demo-s01?scenario=long-session&frame=0&paused=1`, width: 1440, height: 1100, before: async (page) => {
    await page.getByRole("button", { name: "Show earlier", exact: true }).click();
    await page.getByRole("button", { name: "Show later", exact: true }).click();
    await page.locator('.transcript [data-tool-call="t-long"] .wk-tool-row').click();
    await page.locator(".transcript-viewport").evaluate((el) => { el.scrollTop = el.scrollHeight; });
  } },
  { name: "task-multi-session-phone", path: `${task}?scenario=multi-session&paused=1`, width: 390, height: 1500 },
  { name: "board-desktop", path: `/workspaces/demo?scenario=multi-session&paused=1`, width: 1440, height: 900 },
  { name: "board-phone", path: `/workspaces/demo?scenario=multi-session&paused=1`, width: 390, height: 900 },
  { name: "session-failed-desktop", path: `/plugins/dispatch/sessions/demo-s01?scenario=tool-failure&frame=5&paused=1`, width: 1440, height: 1000 },
  { name: "session-running-phone", path: `/plugins/dispatch/sessions/demo-s01?scenario=live&frame=8&paused=1`, width: 390, height: 1100 },
  { name: "state-stale-desktop", path: `${task}?scenario=stale&frame=2&paused=1`, width: 1440, height: 800 },
  { name: "state-cancelled-desktop", path: `${task}?scenario=cancelled&frame=2&paused=1`, width: 1440, height: 800 },
  { name: "state-missing-service-desktop", path: `${task}?scenario=missing-service&frame=1&paused=1`, width: 1440, height: 800 },
  { name: "task-outage-held-phone-dark", path: `${task}?scenario=missing-service&frame=0&paused=1&theme=dark&body=custom-element`, width: 390, height: 1100, before: async (page) => {
    await page.locator(".widget-expand").click();
    await page.locator(".wk-tool-row").first().click();
    await page.locator(".wk-code").first().evaluate((el) => { const r = document.createRange(); r.selectNodeContents(el); const s = window.getSelection()!; s.removeAllRanges(); s.addRange(r); });
    await page.evaluate(() => window.__demo.advance());
  } },
  { name: "state-plugin-removed-desktop", path: `${task}?scenario=plugin-removed&frame=1&paused=1`, width: 1440, height: 800 },
  { name: "state-unknown-version-desktop", path: `${task}?scenario=unknown-version&frame=1&paused=1`, width: 1440, height: 800 },
  { name: "state-body-error-desktop", path: `${task}?scenario=body-error&frame=1&paused=1`, width: 1440, height: 800 },
  { name: "not-found-desktop", path: `/plugins/dispatch/sessions/unknown-id`, width: 1440, height: 600 },
];

for (const shot of shots) {
  const context = await browser.newContext({ viewport: { width: shot.width, height: shot.height }, deviceScaleFactor: 1 });
  const page = await context.newPage();
  await page.goto(base + shot.path);
  await page.locator("#app h1").waitFor();
  await shot.before?.(page);
  await page.waitForTimeout(150);
  await page.screenshot({ path: resolve(out, `${shot.name}.png`), fullPage: true });
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  console.log(`${shot.name}.png ${shot.width}px${overflow > 0 ? ` OVERFLOW ${overflow}px` : ""}`);
  await context.close();
}

await browser.close();
server.stop(true);
