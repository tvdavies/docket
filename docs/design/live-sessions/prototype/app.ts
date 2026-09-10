// Fixture demo application: board → task → session → task with deterministic
// synthetic data. No fetch, WebSocket, EventSource, forms or external assets.

import { chooseBoardSession } from "./adapter";
import { cleanupCount } from "./components/bodies";
import { refreshFreshness } from "./components/wrapper";
import { renderEntries, type FullToolEntry, type RenderableEntry } from "./components/preview";
import { containsFocus, containsSelection, formatClock, h, setText } from "./dom";
import { BOARD_STATUSES, BOARD_TASKS, DEFAULT_SCENARIO, SCENARIOS, WORKSPACE, scenarioById, type Scenario } from "./fixtures";
import { FixtureHost, type BodyChoice } from "./host";
import { fullTranscript } from "./publisher";
import { PROPOSED_BUDGETS } from "../contracts.proposed";

const host = new FixtureHost();
const app = document.getElementById("app")!;
const strip = document.getElementById("demo-strip")!;

interface Persisted { scenario: string; frames: number; paused: boolean; body: BodyChoice; theme: "light" | "dark"; density: "compact" | "comfortable"; reducedMotion: boolean; narrow: boolean }
const STORAGE_KEY = "job-0092-demo";

function readPersisted(): Persisted | null {
  try { const raw = sessionStorage.getItem(STORAGE_KEY); return raw ? JSON.parse(raw) as Persisted : null; } catch { return null; }
}
function persist(): void {
  const frames = Math.max(0, ...[...host.sessions.values()].map((state) => state.frameIndex));
  const data: Persisted = { scenario: host.currentScenario?.id ?? DEFAULT_SCENARIO, frames, paused, body: host.body, theme: host.preferences.theme, density: host.preferences.density, reducedMotion: host.preferences.reducedMotion, narrow };
  try { sessionStorage.setItem(STORAGE_KEY, JSON.stringify(data)); } catch { /* storage disabled */ }
}

let paused = false;
let narrow = false;
let timer: number | null = null;
let currentView: { kind: string; cleanup(): void } | null = null;

// --- Demo controls -----------------------------------------------------------

function applyPreferencesToDocument(): void {
  const root = document.documentElement;
  root.dataset.theme = host.preferences.theme;
  root.dataset.density = host.preferences.density;
  root.dataset.reducedMotion = String(host.preferences.reducedMotion);
  document.body.dataset.narrow = String(narrow);
}

function startAutoplay(): void {
  stopAutoplay();
  if (paused || !host.currentScenario?.autoplay) return;
  timer = window.setInterval(() => { if (!host.advance()) stopAutoplay(); }, 1000);
}
function stopAutoplay(): void { if (timer !== null) { window.clearInterval(timer); timer = null; } }

function loadScenario(scenario: Scenario, frames = 0, startPaused = false, rerender = true): void {
  stopAutoplay();
  host.loadScenario(scenario);
  for (let index = 0; index < frames; index += 1) host.advance();
  paused = startPaused;
  if (rerender) render({ fresh: true });
  startAutoplay();
  persist();
}

function buildStrip(): void {
  const select = h("select", { id: "scenario", "aria-label": "Scenario" });
  for (const scenario of SCENARIOS) select.append(h("option", { value: scenario.id, text: scenario.title }));
  select.addEventListener("change", () => { const scenario = scenarioById(select.value); loadScenario(scenario, 0, false, false); navigate(`/workspaces/${WORKSPACE}/tasks/${BOARD_TASKS[1].id}`); });
  const advance = h("button", { type: "button", class: "wk-button", id: "advance", text: "Advance" });
  advance.addEventListener("click", () => { host.advance(); host.flushLate(); persist(); render(); });
  const pause = h("button", { type: "button", class: "wk-button", id: "pause", "aria-pressed": "false", text: "Pause" });
  pause.addEventListener("click", () => { paused = !paused; pause.setAttribute("aria-pressed", String(paused)); setText(pause, paused ? "Resume" : "Pause"); if (paused) stopAutoplay(); else startAutoplay(); persist(); });
  const reset = h("button", { type: "button", class: "wk-button", id: "reset", text: "Reset" });
  reset.addEventListener("click", () => loadScenario(host.currentScenario ?? SCENARIOS[0], 0, paused));
  const flush = h("button", { type: "button", class: "wk-button", id: "flush-late", text: "Deliver late result" });
  flush.addEventListener("click", () => { host.flushLate(); render(); });

  const body = h("select", { id: "body", "aria-label": "Body kind" }, h("option", { value: "standard", text: "Standard body" }), h("option", { value: "custom-element", text: "Custom element body" }));
  body.addEventListener("change", () => { host.setBody(body.value as BodyChoice, () => render({ fresh: true })); persist(); });
  const theme = h("select", { id: "theme", "aria-label": "Theme" }, h("option", { value: "light", text: "Light" }), h("option", { value: "dark", text: "Dark" }));
  theme.addEventListener("change", () => { host.applyPreferences({ theme: theme.value as "light" | "dark" }); applyPreferencesToDocument(); persist(); });
  const density = h("select", { id: "density", "aria-label": "Density" }, h("option", { value: "comfortable", text: "Comfortable" }), h("option", { value: "compact", text: "Compact" }));
  density.addEventListener("change", () => { host.applyPreferences({ density: density.value as "compact" | "comfortable" }); applyPreferencesToDocument(); persist(); });
  const motion = h("button", { type: "button", class: "wk-button", id: "motion", "aria-pressed": "false", text: "Reduced motion" });
  motion.addEventListener("click", () => { host.applyPreferences({ reducedMotion: !host.preferences.reducedMotion }); motion.setAttribute("aria-pressed", String(host.preferences.reducedMotion)); applyPreferencesToDocument(); persist(); });
  const narrowButton = h("button", { type: "button", class: "wk-button", id: "narrow", "aria-pressed": "false", text: "Narrow 390px" });
  narrowButton.addEventListener("click", () => { narrow = !narrow; narrowButton.setAttribute("aria-pressed", String(narrow)); applyPreferencesToDocument(); persist(); });

  const counters = h("dl", { class: "demo-counters", id: "counters" });
  const frame = h("span", { id: "frame-label", class: "demo-frame" });
  const note = h("span", { id: "frame-note", class: "demo-note" });
  const log = h("details", { class: "demo-log" }, h("summary", { text: "Host log" }), h("ol", { id: "host-log" }));

  strip.replaceChildren(
    h("div", { class: "demo-strip-row" },
      h("strong", { class: "demo-banner", text: "Fixture demo — no live agents" }),
      h("label", { class: "demo-field" }, "Scenario ", select),
      advance, pause, reset, flush, frame, note,
    ),
    h("div", { class: "demo-strip-row" },
      h("label", { class: "demo-field" }, "Body ", body),
      h("label", { class: "demo-field" }, "Theme ", theme),
      h("label", { class: "demo-field" }, "Density ", density),
      motion, narrowButton,
      h("span", { class: "demo-field demo-desc", id: "scenario-desc" }),
    ),
    h("div", { class: "demo-strip-row demo-strip-meta" }, counters, log),
  );

  host.onChange(() => {
    const scenario = host.currentScenario; if (!scenario) return;
    select.value = scenario.id; body.value = host.body; theme.value = host.preferences.theme; density.value = host.preferences.density;
    motion.setAttribute("aria-pressed", String(host.preferences.reducedMotion));
    setText(document.getElementById("scenario-desc")!, scenario.description);
    const frames = [...host.sessions.values()].map((state) => `${state.fixture.sessionId} ${state.frameIndex + 1}/${state.fixture.frames.length}`);
    setText(frame, `Frame ${frames.join(" · ")} · t=${host.now / 1000}s`);
    const notes = [...host.sessions.values()].map((state) => state.fixture.frames[state.frameIndex]?.note).filter(Boolean);
    setText(note, notes.join(" · "));
    counters.replaceChildren(...Object.entries(host.counters).flatMap(([key, value]) => [h("dt", { text: key }), h("dd", { id: `counter-${key}`, text: String(value) })]), h("dt", { text: "bodyCleanups" }), h("dd", { id: "counter-bodyCleanups", text: String(cleanupCount()) }));
    const list = document.getElementById("host-log")!;
    list.replaceChildren(...host.log.slice(-40).map((line) => h("li", { text: line })));
    setText(advance, host.atEnd() ? "Advance (end)" : "Advance");
    refreshFreshness(app, (id) => host.snapshotFor(id), host.now);
    applyPreferencesToDocument();
  });
}

// --- Router ------------------------------------------------------------------

type Route =
  | { kind: "board" }
  | { kind: "task"; taskId: string }
  | { kind: "session"; sessionId: string }
  | { kind: "not-found"; what: string };

function parseRoute(pathname: string): Route {
  if (pathname === "/" || pathname === `/workspaces/${WORKSPACE}` || pathname === `/workspaces/${WORKSPACE}/`) return { kind: "board" };
  const task = pathname.match(new RegExp(`^/workspaces/${WORKSPACE}/tasks/([A-Z0-9-]+)$`));
  if (task) return { kind: "task", taskId: task[1] };
  const session = pathname.match(/^\/plugins\/dispatch\/sessions\/([^/]+)$/);
  if (session) return { kind: "session", sessionId: decodeURIComponent(session[1]) };
  return { kind: "not-found", what: pathname };
}

interface HistoryState { expanded?: string[]; scrollY?: number; focus?: string }

function captureState(): HistoryState {
  const expanded = [...app.querySelectorAll<HTMLElement>('.widget-expand[aria-expanded="true"]')].map((el) => el.closest<HTMLElement>(".widget")?.dataset.instance ?? "").filter(Boolean);
  const active = document.activeElement as HTMLElement | null;
  return { expanded, scrollY: window.scrollY, focus: active?.id || undefined };
}

function navigate(path: string): void {
  history.replaceState(captureState(), "", location.href);
  history.pushState({}, "", path);
  render({ fresh: true });
}

document.addEventListener("click", (event) => {
  const anchor = (event.target as Element | null)?.closest?.("a[href]") as HTMLAnchorElement | null;
  if (!anchor || event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
  if (anchor.target === "_blank" || anchor.origin !== location.origin) return;
  event.preventDefault();
  navigate(anchor.pathname + anchor.search + anchor.hash);
});

window.addEventListener("popstate", (event) => render({ restore: (event.state as HistoryState | null) ?? undefined }));

// --- Views -------------------------------------------------------------------

function crumb(parts: Array<{ text: string; href?: string }>): HTMLElement {
  const nav = h("nav", { class: "crumb", "aria-label": "Breadcrumb" });
  parts.forEach((part, index) => {
    nav.append(part.href ? h("a", { href: part.href, text: part.text }) : h("span", { "aria-current": "page", text: part.text }));
    if (index < parts.length - 1) nav.append(h("span", { class: "crumb-sep", "aria-hidden": "true", text: "/" }));
  });
  return nav;
}

function sessionsForTask(taskId: string) {
  return [...host.sessions.values()].filter((state) => state.taskId === taskId).sort((a, b) => a.fixture.createdAt.localeCompare(b.fixture.createdAt));
}

function renderBoard(): { kind: string; cleanup(): void } {
  const mounted: Array<[string]> = [];
  const lanes = h("div", { class: "board-grid" });
  for (const status of BOARD_STATUSES) {
    const list = h("div", { class: "lane-list" });
    for (const task of BOARD_TASKS.map((item) => host.taskFor(item.id) ?? item).filter((item) => item.status === status.id)) {
      const card = h("article", { class: "task-card", "data-task": task.id, "aria-labelledby": `card-${task.id}` });
      card.append(h("div", { class: "card-top" }, h("span", { class: "task-id", text: task.id })));
      card.append(h("h3", { id: `card-${task.id}` }, h("a", { class: "card-title", href: `/workspaces/${WORKSPACE}/tasks/${task.id}`, text: task.title })));
      card.append(h("div", { class: "card-meta" }, ...task.labels.map((label) => h("span", { class: "label", text: label }))));
      const sessions = sessionsForTask(task.id);
      if (sessions.length) {
        const chosen = chooseBoardSession(sessions.map((state) => ({ execution: state.fixture.frames[state.frameIndex]?.execution ?? "queued", createdAt: state.fixture.createdAt, sessionId: state.fixture.sessionId })))!;
        const slot = h("div", { class: "plugin-card-host", "data-plugin-card": "dispatch/session" });
        card.append(slot);
        host.mount(slot, "board", chosen.sessionId); mounted.push([chosen.sessionId]);
        if (sessions.length > 1) card.append(h("a", { class: "wk-reference more-sessions", href: `/workspaces/${WORKSPACE}/tasks/${task.id}`, "aria-label": `${sessions.length - 1} more sessions on ${task.id}`, text: `+${sessions.length - 1} sessions` }));
      }
      list.append(card);
    }
    lanes.append(h("section", { class: "lane", "aria-label": status.label }, h("header", { class: "lane-header" }, h("h2", { text: status.label })), list));
  }
  app.replaceChildren(crumb([{ text: "Demo workspace" }]), h("div", { class: "toolbar" }, h("h1", { id: "view-heading", tabindex: "-1", text: "Board" }), h("p", { text: "Compact board contribution: agent · stage · state plus one current action. No transcript here." })), h("div", { class: "board-scroll" }, lanes));
  return { kind: "board", cleanup: () => { for (const [id] of mounted) host.unmount("board", id); } };
}

function renderTask(taskId: string): { kind: string; cleanup(): void } {
  const task = host.taskFor(taskId);
  if (!task) return renderNotFound(`Task ${taskId}`);
  const sessions = sessionsForTask(taskId);
  const document_ = h("div", { class: "detail-document" });
  const heading = h("h1", { id: "view-heading", tabindex: "-1", text: task.title });
  document_.append(
    h("p", { class: "task-id", text: `Task · ${task.id}` }),
    heading,
    h("p", { class: "muted", text: "One session, one activity entry. Read the current action here; open the session when you need the full story." }),
  );
  // Task fields arrive in snapshots; the host applies them to its task record
  // and the page heading follows without remounting any widget.
  const stageEl = h("dd", { text: task.status });
  const unsubscribe = host.onChange(() => { const current = host.taskFor(taskId); if (current) { setText(heading, current.title); setText(stageEl, current.status); document.title = `${taskId} · JOB-0092 fixture demo`; } });
  const activity = h("section", { class: "activity", "aria-labelledby": "activity-heading" }, h("h2", { id: "activity-heading", text: "Activity" }));
  const list = h("ol", { class: "activity-list" });
  for (const state of sessions) {
    const entry = h("li", { class: "activity-entry", "data-session": state.fixture.sessionId }, h("div", { class: "activity-marker", "aria-hidden": "true", text: "◇" }));
    const column = h("div", { class: "activity-column" }, h("p", { class: "activity-lead", text: `${state.fixture.source.persona} started a session · ${formatClock(state.fixture.createdAt) ?? ""}` }));
    const slot = h("div", { class: "plugin-activity-host", "data-plugin-widget": "dispatch/session" });
    column.append(slot); entry.append(column); list.append(entry);
    host.mount(slot, "activity", state.fixture.sessionId);
  }
  if (!sessions.length) list.append(h("li", { class: "activity-entry muted", text: "No sessions on this task." }));
  activity.append(list);
  document_.append(activity);
  const properties = h("aside", { class: "detail-properties" }, h("h2", { text: "Task properties" }),
    h("dl", {}, h("dt", { text: "Stage" }), stageEl, h("dt", { text: "Assignee" }), h("dd", { text: task.assignee ?? "—" }), h("dt", { text: "Workspace" }), h("dd", { text: "Demo" })),
    h("p", { class: "muted small", text: "The board shows one meaningful action, never a scrolling transcript. On a phone, activity uses the full width." }),
  );
  app.replaceChildren(crumb([{ text: "Demo workspace", href: `/workspaces/${WORKSPACE}` }, { text: task.id }]), h("div", { class: "detail-layout" }, document_, properties));
  return { kind: "task", cleanup: () => { unsubscribe(); for (const state of sessions) host.unmount("activity", state.fixture.sessionId); } };
}

function renderSession(sessionId: string): { kind: string; cleanup(): void } {
  const state = host.sessions.get(sessionId);
  if (!state) return renderNotFound(`Session ${sessionId}`, "The session ID is not known to this demo. Task context is not guessed.");
  const taskPath = `/workspaces/${WORKSPACE}/tasks/${state.taskId}`;
  const task = host.taskFor(state.taskId);
  const header = h("header", { class: "session-header" },
    h("p", { class: "task-id" }, "Originating task · ", h("a", { href: taskPath, text: state.taskId })),
    h("h1", { id: "view-heading", tabindex: "-1", text: task?.title ?? state.taskId }),
    h("div", { class: "wk-meta session-meta" }, h("span", { text: "Workspace Demo" }), h("span", { text: `${state.fixture.source.persona} · ${state.fixture.source.stage}` }), h("span", { class: "session-id", text: `Session ${sessionId}` })),
    h("div", { class: "session-status" }, h("span", { class: "wk-status", id: "session-execution" }), h("span", { class: "wk-freshness", id: "session-freshness" }, h("i", { "aria-hidden": "true" }), h("span"))),
  );
  const notices = h("div", { class: "widget-notices" });
  const viewport = h("div", { class: "transcript-viewport", tabindex: "0", "aria-label": "Session transcript" });
  const transcript = h("ol", { class: "wk-steps transcript", "aria-label": "Ordered transcript" });
  const earlier = h("button", { type: "button", class: "wk-button show-earlier", hidden: true, text: "Show earlier" });
  const later = h("button", { type: "button", class: "wk-button show-later", hidden: true, text: "Show later" });
  const windowLabel = h("p", { class: "wk-note transcript-window", role: "status" });
  const jump = h("button", { type: "button", class: "wk-button jump-latest", hidden: true, text: "Jump to latest" });
  viewport.append(earlier, transcript, later);
  // Explicit reader-owned window over the ordered transcript. `latest` means
  // "the newest N entries" and follows growth; any other window is pinned to
  // an absolute start index so streaming never moves the reader. The mounted
  // count never exceeds PROPOSED_BUDGETS.fullSessionMountedEntries.
  const SIZE = PROPOSED_BUDGETS.fullSessionMountedEntries;
  const CHUNK = PROPOSED_BUDGETS.fullSessionOutputChunkChars;
  let following = true; let windowStart: number | "latest" = "latest"; let total = 0;
  const revealed = new Map<string, number>(); // toolCallId -> revealed output characters (reader-owned)
  const measure = () => { following = viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight < 120; jump.hidden = following && windowStart === "latest"; };
  viewport.addEventListener("scroll", measure);
  jump.addEventListener("click", () => { windowStart = "latest"; update(); viewport.scrollTo({ top: viewport.scrollHeight, behavior: host.preferences.reducedMotion ? "auto" : "smooth" }); following = true; jump.hidden = true; });
  const startOf = () => windowStart === "latest" ? Math.max(0, total - SIZE) : windowStart;
  earlier.addEventListener("click", () => { windowStart = Math.max(0, startOf() - SIZE); following = false; update(); transcript.firstElementChild?.scrollIntoView({ block: "start", behavior: "auto" }); (transcript.firstElementChild as HTMLElement | null)?.focus?.(); });
  later.addEventListener("click", () => { const next = startOf() + SIZE; windowStart = next + SIZE >= total ? "latest" : next; update(); transcript.firstElementChild?.scrollIntoView({ block: "start", behavior: "auto" }); });

  const update = () => {
    const frame = state.fixture.frames[state.frameIndex];
    const snapshot = host.snapshotFor(sessionId);
    const preview = snapshot?.data?.value;
    const statusEl = header.querySelector("#session-execution") as HTMLElement;
    const execution = frame?.execution ?? "queued";
    const labels: Record<string, [string, string]> = { queued: ["Queued", "neutral"], starting: ["Starting", "neutral"], running: ["Running", "info"], awaiting_input: ["Awaiting input", "warning"], completed: ["Completed", "positive"], failed: ["Failed", "danger"], cancelled: ["Cancelled", "warning"] };
    const [text, tone] = labels[execution] ?? ["Unknown", "neutral"];
    statusEl.dataset.tone = tone; setText(statusEl, text);
    if (snapshot) refreshFreshnessInto(header.querySelector("#session-freshness") as HTMLElement, snapshot.freshness, host.now);
    notices.replaceChildren();
    if (preview?.attention) notices.append(h("div", { class: "wk-notice", "data-tone": preview.attention.kind === "error" ? "danger" : "warning", role: preview.attention.kind === "error" ? "alert" : "status" }, h("strong", { text: preview.attention.kind === "error" ? "Session failed" : "Input required" }), h("span", { text: preview.attention.message }), h("small", { text: "Read-only view. No approval or reply controls." })));
    if (snapshot?.availability === "plugin_removed") { notices.append(h("div", { class: "wk-notice", "data-tone": "neutral", role: "status" }, h("strong", { text: "Plugin unavailable" }), h("span", { text: "The Dispatch plugin is not enabled in this workspace. The saved record is shown; the transcript is unavailable." }), h("a", { class: "wk-reference", href: taskPath, text: `Back to ${state.taskId}` }))); transcript.replaceChildren(); return; }
    if (snapshot?.availability === "missing_service") notices.append(h("div", { class: "wk-notice", "data-tone": "neutral", role: "status" }, h("strong", { text: "Plugin service unavailable" }), h("span", { text: "Showing the last known transcript." })));
    // The full session reads the accepted payload's extent, never a rejected frame.
    const all = fullTranscript(state.fixture.source, state.accepted?.through ?? 0);
    const engaged = containsFocus(transcript) || containsSelection(viewport);
    const wasFollowing = following;
    total = all.length;
    const start = startOf();
    const end = Math.min(total, start + SIZE);
    earlier.hidden = start === 0;
    later.hidden = end >= total;
    setText(windowLabel, total > SIZE ? `Showing entries ${start + 1}–${end} of ${total} (${SIZE} at a time)` : `${total} entries`);
    const entries: RenderableEntry[] = all.slice(start, end).map((entry) => {
      if (entry.type === "tool") {
        const output = entry.output ?? "";
        const shown = Math.min(output.length, Math.max(CHUNK, revealed.get(entry.toolCallId!) ?? 0));
        const row: FullToolEntry = { id: entry.id, seq: entry.seq, type: "tool", toolCallId: entry.toolCallId!, label: entry.label!, status: entry.status!, summary: entry.summary, durationMs: entry.durationMs, input: entry.input, output: output ? output.slice(0, shown) : undefined, outputRemaining: output.length > CHUNK ? output.length - shown : undefined };
        return row;
      }
      return entry.type === "user" ? ({ id: entry.id, seq: entry.seq, type: "user", text: entry.text ?? "" }) : ({ id: entry.id, seq: entry.seq, type: "assistant", text: entry.text ?? "" });
    });
    // User messages are permitted in the full session only; they are rendered in order here.
    renderEntries(transcript, entries, { detail: true, failedOpen: true, idPrefix: `full-${sessionId}`, showMore: (toolCallId) => { revealed.set(toolCallId, Math.max(CHUNK, revealed.get(toolCallId) ?? 0) + CHUNK); update(); transcript.querySelector<HTMLElement>(`.wk-show-more[data-tool-call="${CSS.escape(toolCallId)}"]`)?.focus(); } });
    if (wasFollowing && !engaged && windowStart === "latest") { viewport.scrollTop = viewport.scrollHeight; }
    measure();
  };
  const unsubscribe = host.onChange(update);
  app.replaceChildren(
    crumb([{ text: "Demo workspace", href: `/workspaces/${WORKSPACE}` }, { text: state.taskId, href: taskPath }, { text: `Session ${sessionId}` }]),
    h("div", { class: "session-layout" }, header, notices, windowLabel, viewport, h("div", { class: "session-footer" }, jump, h("a", { class: "wk-reference back-to-task", href: taskPath, text: `Back to ${state.taskId}` }))),
  );
  update();
  viewport.scrollTop = viewport.scrollHeight;
  return { kind: "session", cleanup: unsubscribe };
}

function refreshFreshnessInto(el: HTMLElement, freshness: { connection: string; receivedAt: number | null; expiresAt: number | null; rehydrating?: boolean }, now: number): void {
  const stale = freshness.expiresAt !== null && now > freshness.expiresAt;
  const ago = freshness.receivedAt === null ? null : Math.max(0, Math.round((now - freshness.receivedAt) / 1000));
  el.dataset.connection = freshness.connection; el.dataset.stale = String(stale);
  const base = freshness.connection === "live" ? "Live" : freshness.connection === "connecting" ? "Connecting" : "Disconnected";
  setText(el.lastElementChild!, freshness.rehydrating ? "Reconnected · awaiting rehydration" : stale ? `Stale · last update ${ago ?? "unknown"}s ago` : ago === null ? base : `${base} · updated ${ago}s ago`);
}

function renderNotFound(what: string, detail = "Nothing is known about this address in the demo."): { kind: string; cleanup(): void } {
  app.replaceChildren(
    crumb([{ text: "Demo workspace", href: `/workspaces/${WORKSPACE}` }, { text: "Not found" }]),
    h("div", { class: "detail-layout" }, h("div", { class: "detail-document" }, h("h1", { id: "view-heading", tabindex: "-1", text: `${what} not found` }), h("p", { class: "muted", text: detail }), h("a", { class: "wk-reference", href: `/workspaces/${WORKSPACE}`, text: "Back to the demo workspace" }))),
  );
  return { kind: "not-found", cleanup: () => undefined };
}

// --- Render ------------------------------------------------------------------

function render(options: { fresh?: boolean; restore?: HistoryState } = {}): void {
  const route = parseRoute(location.pathname);
  const sameView = currentView?.kind === route.kind && !options.fresh && !options.restore;
  if (currentView && !sameView) { currentView.cleanup(); currentView = null; }
  if (!currentView || options.fresh || options.restore) {
    if (currentView) currentView.cleanup();
    currentView = route.kind === "board" ? renderBoard() : route.kind === "task" ? renderTask(route.taskId) : route.kind === "session" ? renderSession(route.sessionId) : renderNotFound(route.what);
    document.title = `${route.kind === "task" ? route.taskId : route.kind === "session" ? `Session ${route.sessionId}` : route.kind === "board" ? "Board" : "Not found"} · JOB-0092 fixture demo`;
  }
  applyPreferencesToDocument();
  if (options.restore) {
    for (const instanceId of options.restore.expanded ?? []) app.querySelector<HTMLButtonElement>(`.widget[data-instance="${CSS.escape(instanceId)}"] .widget-expand`)?.click();
    if (options.restore.focus) document.getElementById(options.restore.focus)?.focus();
    if (options.restore.scrollY !== undefined) window.scrollTo({ top: options.restore.scrollY, behavior: "auto" });
  } else if (options.fresh) {
    document.getElementById("view-heading")?.focus({ preventScroll: false });
    window.scrollTo({ top: 0, behavior: "auto" });
  }
}

// --- Boot --------------------------------------------------------------------

buildStrip();
const params = new URLSearchParams(location.search);
const persisted = readPersisted();
const scenarioId = params.get("scenario") ?? persisted?.scenario ?? DEFAULT_SCENARIO;
const frames = params.has("frame") ? Number(params.get("frame")) : persisted?.frames ?? 0;
const initialPaused = params.has("frame") || params.get("paused") === "1" || Boolean(persisted?.paused);
if (persisted) { host.body = persisted.body; host.preferences = { theme: persisted.theme, density: persisted.density, reducedMotion: persisted.reducedMotion }; narrow = persisted.narrow; }
if (params.get("body") === "custom-element" || params.get("body") === "standard") host.body = params.get("body") as BodyChoice;
if (params.get("theme") === "dark" || params.get("theme") === "light") host.preferences.theme = params.get("theme") as "light" | "dark";
if (params.get("density") === "compact" || params.get("density") === "comfortable") host.preferences.density = params.get("density") as "compact" | "comfortable";
if (params.get("motion") === "reduced") host.preferences.reducedMotion = true;
if (params.get("narrow") === "1") narrow = true;
document.getElementById("motion")?.setAttribute("aria-pressed", String(host.preferences.reducedMotion));
document.getElementById("narrow")?.setAttribute("aria-pressed", String(narrow));
if (initialPaused) { paused = true; const pause = document.getElementById("pause")!; pause.setAttribute("aria-pressed", "true"); setText(pause, "Resume"); }
// Strip the one-shot query so refresh keeps the persisted state.
if ([...params.keys()].length) history.replaceState({}, "", location.pathname);
loadScenario(scenarioById(scenarioId), frames, initialPaused, false);
render({ fresh: true });

declare global { interface Window { __demo: { host: FixtureHost; advance(): void; flushLate(): void; pause(): void; scenario(id: string, frames?: number): void; navigate(path: string): void } } }
window.__demo = {
  host,
  advance: () => { host.advance(); host.flushLate(); persist(); },
  flushLate: () => host.flushLate(),
  pause: () => { if (!paused) document.getElementById("pause")!.click(); },
  scenario: (id, framesToAdvance = 0) => loadScenario(scenarioById(id), framesToAdvance, true),
  navigate,
};
