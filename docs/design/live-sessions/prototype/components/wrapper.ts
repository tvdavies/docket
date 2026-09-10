// Illustrative Docket-owned wrapper.
//
// Anatomy (board contribution or one activity entry):
//   header    — plugin label, domain status, separate freshness
//   notices   — error/input attention and generic availability, never clipped
//   body slot — standard themed composition OR one custom element
//   footer    — safe reference anchors, Open session, known start/duration
//   fallback  — replaces an unavailable/failed body; retains the saved record
//
// The wrapper never interprets ACP/session enums; the display adapter returns
// labels/tones. The body cannot replace the wrapper, hide its notices or
// create a persistent panel. This is trusted-plugin conformance, not a
// sandbox.

import type {
  DetailSnapshot,
  DisplayAdapter,
  DurableFallback,
  SessionPreviewV1,
  WidgetContext,
  WidgetInstance,
  WidgetPresentation,
  WidgetSnapshot,
} from "../../contracts.proposed";
import { containsFocus, containsSelection, formatClock, formatDuration, h, setAttr, setText, shortId } from "../dom";
import { disclosure, freshness, notice, reference, status, updateDisclosure, updateFreshness, updateStatus, type FreshnessValue } from "./kit";

export interface BodyUpdate {
  presentation: WidgetPresentation;
  context: WidgetContext;
  expanded: boolean;
  detail: DetailSnapshot | null;
  /** Detail stream paused because of a gap; body shows a paused note. */
  detailPaused: boolean;
}

export interface Body {
  element: HTMLElement;
  update(update: BodyUpdate): void;
  destroy(): void;
}

export type BodyFactory = (context: WidgetContext) => Body;

export interface WrapperHooks {
  /** Host clock, used only for freshness text. */
  now(): number;
  onBodyError?(error: unknown): void;
  onAnnounce?(message: string): void;
}

const FRESHNESS_TEXT: Record<string, string> = { connecting: "Connecting", live: "Live", disconnected: "Disconnected" };

function freshnessValue(snapshot: WidgetSnapshot, now: number): FreshnessValue {
  const { freshness: f } = snapshot;
  const stale = f.expiresAt !== null && now > f.expiresAt;
  const ago = f.receivedAt === null ? null : Math.max(0, Math.round((now - f.receivedAt) / 1000));
  let text = FRESHNESS_TEXT[f.connection] ?? f.connection;
  if (f.rehydrating) text = "Reconnected · awaiting rehydration";
  else if (stale) text = `Stale · last update ${ago === null ? "unknown" : `${ago}s ago`}`;
  else if (ago !== null) text = `${text} · updated ${ago}s ago`;
  return { connection: f.connection, stale, text };
}

function fallbackPresentation(fallback: DurableFallback | undefined, reason: string): WidgetPresentation {
  return {
    label: fallback?.label ?? "Session",
    status: { text: fallback?.statusLabel ?? "Unknown", tone: "neutral" },
    terminal: true,
    entries: [],
    truncated: false,
    summary: fallback?.summary ?? reason,
    startedAt: fallback?.startedAt,
    references: fallback?.references ?? [],
  };
}

export function mountWrapper(
  slot: HTMLElement,
  context: WidgetContext,
  adapter: DisplayAdapter<SessionPreviewV1>,
  bodyFactory: BodyFactory,
  hooks: WrapperHooks,
): WidgetInstance<SessionPreviewV1> {
  const { identity, location } = context;
  const baseId = `w-${location}-${identity.instanceId}`;
  const root = h("article", {
    class: "widget", "data-location": location, "data-widget-type": identity.widgetType,
    "data-instance": identity.instanceId, "aria-labelledby": `${baseId}-label`, id: baseId,
  });
  // Header
  const label = h("span", { class: "widget-label", id: `${baseId}-label` });
  const statusEl = status({ text: "—", tone: "neutral" }, "widget-status");
  const freshnessEl = freshness({ connection: "connecting", stale: false, text: "Connecting" });
  const header = h("header", { class: "widget-header" }, h("div", { class: "widget-heading" }, h("span", { class: "widget-avatar", "aria-hidden": "true" }), label, statusEl), freshnessEl);
  // Notices (outside the clipped body)
  const notices = h("div", { class: "widget-notices" });
  const live = h("p", { class: "sr-only", "aria-live": "polite", "aria-atomic": "true" });
  // Body slot
  const bodySlot = h("div", { class: "widget-body-slot", id: `${baseId}-body` });
  const boardAction = h("p", { class: "widget-action" });
  const newActivity = h("button", { type: "button", class: "wk-button widget-new-activity", hidden: true, text: "New activity · show" });
  const summaryEl = h("div", { class: "wk-summary widget-summary", hidden: true });
  // Disclosure + footer
  const expandButton = disclosure("Expand activity", false, `${baseId}-body`);
  expandButton.classList.add("widget-expand");
  const collapseFinal = h("button", { type: "button", class: "wk-disclosure widget-collapse", hidden: true, text: "Collapse to summary" });
  const footerMeta = h("div", { class: "wk-meta widget-footer-meta" });
  const footerLinks = h("div", { class: "widget-footer-links" });
  const footer = h("footer", { class: "widget-footer" }, footerMeta, footerLinks);
  const fallbackEl = h("div", { class: "widget-fallback", hidden: true });
  const controls = h("div", { class: "widget-controls" }, expandButton, collapseFinal, newActivity);

  root.append(header, notices, live, fallbackEl);
  if (location === "board") { root.append(boardAction); } else { root.append(summaryEl, bodySlot, controls); }
  root.append(footer);
  slot.replaceChildren(root);

  // State
  let body: Body | null = null;
  let bodyRetired = false;
  let expanded = false;
  let readerExpanded = false; // explicit reader intent
  let frozen = false;
  let pending: { presentation: WidgetPresentation; snapshot: WidgetSnapshot<SessionPreviewV1>; context: WidgetContext } | null = null;
  let lastPresentation: WidgetPresentation | null = null;
  let lastSnapshot: WidgetSnapshot<SessionPreviewV1> | null = null;
  let currentContext = context;
  let detail: DetailSnapshot | null = null;
  let detailPaused = false;
  let releaseDetail: (() => void) | null = null;
  let lastAttentionId: string | null = null;
  let lastTerminal = false;
  let destroyed = false;

  function announce(message: string): void { setText(live, message); hooks.onAnnounce?.(message); }

  function ensureBody(): Body | null {
    if (bodyRetired || destroyed || location === "board") return null;
    if (!body) {
      try { body = bodyFactory(currentContext); bodySlot.replaceChildren(body.element); }
      catch (error) { retireBody(error); return null; }
    }
    return body;
  }

  function retireBody(error: unknown): void {
    bodyRetired = true;
    try { body?.destroy(); } catch { /* idempotent cleanup */ }
    body = null;
    bodySlot.replaceChildren();
    bodySlot.hidden = true;
    controls.hidden = true;
    releaseLease();
    hooks.onBodyError?.(error);
    showFallback("Widget body failed; showing the saved record.");
  }

  function showFallback(reason: string): void {
    const presentation = fallbackPresentation(lastSnapshot?.fallback, reason);
    fallbackEl.hidden = false;
    fallbackEl.replaceChildren(
      notice("neutral", "Details unavailable", reason),
      h("p", { class: "widget-fallback-summary", text: presentation.summary ?? "" }),
    );
    renderFooter(presentation);
  }

  function acquireLease(): void {
    if (releaseDetail || location === "board" || !currentContext.helpers.requestDetail) return;
    releaseDetail = currentContext.helpers.requestDetail((incoming) => {
      if (destroyed || !expanded) return; // late/irrelevant
      if (!incoming.reset && detail && incoming.baseSeq !== detail.throughSeq) {
        detailPaused = true; // gap: pause and ask the owner for a snapshot
        renderBody();
        releaseLease();
        acquireLease();
        return;
      }
      detail = incoming; detailPaused = false; renderBody();
    });
  }

  function releaseLease(): void {
    if (!releaseDetail) return;
    const release = releaseDetail; releaseDetail = null; release();
  }

  function setExpanded(next: boolean, byReader: boolean): void {
    if (expanded === next) return;
    if (!next && containsFocus(bodySlot)) expandButton.focus();
    expanded = next;
    if (byReader) readerExpanded = next;
    updateDisclosure(expandButton, next ? "Collapse activity" : `Expand activity${lastPresentation && lastPresentation.truncated ? " · more entries" : ""}`, next);
    if (next) acquireLease(); else { releaseLease(); detail = null; detailPaused = false; }
    renderBody();
  }

  expandButton.addEventListener("click", () => setExpanded(!expanded, true));
  collapseFinal.addEventListener("click", () => { readerExpanded = false; setExpanded(false, true); renderTerminalLayout(); });
  newActivity.addEventListener("click", () => { frozen = false; newActivity.hidden = true; if (pending) { const p = pending; pending = null; apply(p.snapshot, p.presentation, p.context); } });

  function readerEngaged(): boolean {
    return containsFocus(bodySlot) || containsSelection(bodySlot) || containsSelection(root);
  }

  function renderHeader(presentation: WidgetPresentation, snapshot: WidgetSnapshot): void {
    setText(label, presentation.label);
    updateStatus(statusEl, presentation.status);
    updateFreshness(freshnessEl, freshnessValue(snapshot, hooks.now()));
    setAttr(root, "data-tone", presentation.status.tone);
    setAttr(root, "data-terminal", String(presentation.terminal));
  }

  function renderNotices(presentation: WidgetPresentation, snapshot: WidgetSnapshot): void {
    notices.replaceChildren();
    const attention = presentation.attention;
    if (attention) {
      const tone = attention.kind === "error" ? "danger" : "warning";
      const title = attention.kind === "error" ? "Session failed" : "Input required";
      notices.append(notice(tone, title, attention.message, "This preview is read-only. No approval or reply controls."));
      if (attention.id !== lastAttentionId) announce(`${presentation.label}: ${title}. ${attention.message}`);
    }
    lastAttentionId = attention?.id ?? null;
    if (snapshot.availability === "missing_service") notices.append(notice("neutral", "Plugin service unavailable", "Showing the last known state. Details are unavailable until the service returns.", "Retry is read-only and explicit."));
    if (snapshot.freshness.rehydrating) notices.append(notice("neutral", "Awaiting rehydration", "The live cache is empty after reconnecting. Execution state is unknown, not lost."));
  }

  function renderFooter(presentation: WidgetPresentation): void {
    footerMeta.replaceChildren();
    const started = formatClock(presentation.startedAt);
    const duration = formatDuration(presentation.durationMs);
    footerMeta.append(h("span", { text: `Session ${shortId(identity.instanceId)}`, title: identity.instanceId }));
    if (started) footerMeta.append(h("span", { class: "wk-tabular", text: `Started ${started}` }));
    if (duration) footerMeta.append(h("span", { class: "wk-tabular", text: duration }));
    if (presentation.usage?.tokens !== undefined) footerMeta.append(h("span", { class: "wk-tabular", text: `${presentation.usage.tokens.toLocaleString()} tokens` }));
    footerLinks.replaceChildren();
    for (const ref of presentation.references) {
      const href = currentContext.helpers.hrefFor(ref);
      if (!href) continue; // reject unsafe/off-origin destinations rather than render a dead link
      const text = ref.kind === "session" ? `Open session${location === "board" ? "" : " →"}` : ref.label;
      const anchor = reference(ref, href, text);
      anchor.setAttribute("aria-label", ref.kind === "session" ? `Open session ${identity.instanceId} for ${identity.taskId}` : ref.label);
      if (ref.kind === "session") anchor.id = `${baseId}-open`; // stable id so history restoration can return focus here
      footerLinks.append(anchor);
    }
  }

  function renderTerminalLayout(): void {
    const presentation = lastPresentation; if (!presentation) return;
    const collapsedToSummary = presentation.terminal && !expanded;
    summaryEl.hidden = !collapsedToSummary;
    if (collapsedToSummary) {
      summaryEl.replaceChildren(
        h("p", { text: presentation.summary ?? "Session completed; no summary was published" }),
        h("div", { class: "wk-meta" }, h("span", { text: "Summary saved" }), h("span", { text: "No approval implied" })),
      );
      updateDisclosure(expandButton, "Expand completed activity", false);
    }
    collapseFinal.hidden = !(presentation.terminal && expanded);
  }

  function renderBody(): void {
    const presentation = lastPresentation; if (!presentation || location === "board") return;
    const current = ensureBody(); if (!current) return;
    bodySlot.hidden = presentation.terminal && !expanded;
    try { current.update({ presentation, context: currentContext, expanded, detail, detailPaused }); }
    catch (error) { retireBody(error); }
    renderTerminalLayout();
  }

  function apply(snapshot: WidgetSnapshot<SessionPreviewV1>, presentation: WidgetPresentation, ctx: WidgetContext): void {
    lastPresentation = presentation; lastSnapshot = snapshot; currentContext = ctx;
    renderHeader(presentation, snapshot);
    renderNotices(presentation, snapshot);
    renderFooter(presentation);
    if (location === "board") { setText(boardAction, presentation.action ?? ""); boardAction.hidden = !presentation.action; return; }
    // Finalisation: collapse to summary only when the reader has not engaged.
    if (presentation.terminal && !lastTerminal) {
      if (!readerExpanded && !readerEngaged()) { setExpanded(false, false); }
      announce(`${presentation.label}: ${presentation.status.text}.`);
    }
    lastTerminal = presentation.terminal;
    if (!presentation.terminal && expanded && !readerExpanded) setExpanded(false, false);
    renderBody();
  }

  const instance: WidgetInstance<SessionPreviewV1> = {
    update(snapshot, ctx) {
      if (destroyed) return;
      currentContext = ctx;
      lastSnapshot = snapshot;
      fallbackEl.hidden = true;
      if (snapshot.availability === "plugin_removed") { retireBody(new Error("plugin removed")); showFallback("Plugin removed from this workspace. Saved record shown without plugin code."); renderHeader(fallbackPresentation(snapshot.fallback, ""), snapshot); return; }
      let presentation: WidgetPresentation | null = null;
      if (snapshot.data && adapter.supportedVersions.includes(snapshot.data.version)) presentation = adapter.present(snapshot.data);
      if (!presentation) {
        const reason = snapshot.data ? "Unsupported session data; showing the saved record." : snapshot.freshness.rehydrating ? "Awaiting rehydration; showing the saved record." : "No live data; showing the saved record.";
        presentation = fallbackPresentation(snapshot.fallback, reason);
        renderHeader({ ...presentation, status: { text: snapshot.fallback?.statusLabel ?? "Unknown", tone: "neutral" } }, snapshot);
        renderNotices({ ...presentation, attention: undefined }, snapshot);
        if (snapshot.data) notices.append(notice("neutral", "Unsupported session data", `Version ${snapshot.data.version} is not supported by this widget; the saved record is shown instead.`));
        renderFooter(presentation);
        if (location === "activity") { bodySlot.hidden = true; summaryEl.hidden = false; summaryEl.replaceChildren(h("p", { text: presentation.summary ?? "" })); controls.hidden = true; }
        else { setText(boardAction, snapshot.fallback?.statusLabel ?? ""); }
        return;
      }
      controls.hidden = bodyRetired;
      if (bodyRetired) { renderHeader(presentation, snapshot); renderNotices(presentation, snapshot); showFallback("Widget body failed; showing the saved record."); return; }
      // Reader intent: freeze the visible window while the reader is inside it.
      if (location === "activity" && readerEngaged() && lastPresentation && !presentation.terminal) {
        frozen = true; pending = { snapshot, presentation, context: ctx };
        renderHeader(presentation, snapshot); // status/freshness still update; the body does not move
        updateFreshness(freshnessEl, freshnessValue(snapshot, hooks.now()));
        newActivity.hidden = false;
        return;
      }
      if (frozen && pending) { frozen = false; pending = null; newActivity.hidden = true; }
      apply(snapshot, presentation, ctx);
    },
    destroy() {
      if (destroyed) return;
      destroyed = true;
      releaseLease();
      try { body?.destroy(); } catch { /* idempotent */ }
      body = null;
      root.remove();
    },
  };
  context.signal.addEventListener("abort", () => instance.destroy(), { once: true });
  return instance;
}

export function refreshFreshness(root: ParentNode, snapshotFor: (instanceId: string) => WidgetSnapshot | null, now: number): void {
  for (const el of root.querySelectorAll<HTMLElement>(".widget")) {
    const snapshot = snapshotFor(el.dataset.instance ?? "");
    const target = el.querySelector<HTMLElement>(".wk-freshness");
    if (snapshot && target) updateFreshness(target, freshnessValue(snapshot, now));
  }
}
