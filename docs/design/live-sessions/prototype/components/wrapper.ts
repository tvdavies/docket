// Illustrative Docket-owned wrapper.
//
// Anatomy (board contribution or one activity entry):
//   header    — plugin label, domain status, separate freshness
//   notices   — error/input attention and generic availability, never clipped
//   body slot — standard themed composition OR one custom element
//   footer    — safe reference anchors, Open session, known start/duration
//   fallback  — replaces an unavailable/failed body; retains the saved record
//
// State model (the part the review asked to be made explicit):
//   accepted  — the latest snapshot the host delivered. Header, notices and
//               footer ALWAYS render from it; they are never withheld.
//   shown     — the presentation/detail the body currently displays. It is
//               reader-owned: while the reader has selected text, has focus
//               inside the body, or has explicitly expanded the card, new
//               body content is withheld as `pending` behind an explicit
//               "New activity · show" control. Terminal transitions obey the
//               same rule, so a held preview is never hidden under a summary.
//   window    — "preview" | "expanded" | "summary"; changes only through
//               reader actions or an unattended terminal transition.
// Interactive footer nodes (Open session and other references) are
// reconciled by key so focus survives streaming updates.
//
// The wrapper never interprets ACP/session enums; the display adapter returns
// labels/tones. The body cannot replace the wrapper, hide its notices or
// create a persistent panel. This is trusted-plugin conformance, not a
// sandbox.

import type {
  DetailRevocation,
  DetailSnapshot,
  DisplayAdapter,
  DurableFallback,
  SessionPreviewV1,
  WidgetContext,
  WidgetInstance,
  WidgetPresentation,
  WidgetReference,
  WidgetSnapshot,
} from "../../contracts.proposed";
import { containsFocus, containsSelection, formatClock, formatDuration, h, reconcile, setAttr, setText, shortId } from "../dom";
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

type DisplayWindow = "preview" | "expanded" | "summary";

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

/** Reading-window content signature: identical entries never trigger a "New activity" prompt. Status/summary metadata is not part of the window. */
function bodySignature(presentation: WidgetPresentation): string {
  return JSON.stringify([presentation.truncated, presentation.entries]);
}
function detailSignature(detail: DetailSnapshot | null): string {
  return detail ? JSON.stringify([detail.truncated, detail.entries]) : "";
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
  // Notices (outside the clipped body; always rendered from the accepted snapshot)
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
  const controlNote = h("p", { class: "wk-note widget-control-note", hidden: true, role: "status" });
  const footerMeta = h("div", { class: "wk-meta widget-footer-meta" });
  const footerLinks = h("div", { class: "widget-footer-links" });
  const footer = h("footer", { class: "widget-footer" }, footerMeta, footerLinks);
  const fallbackEl = h("div", { class: "widget-fallback", hidden: true });
  const controls = h("div", { class: "widget-controls" }, expandButton, collapseFinal, newActivity, controlNote);

  root.append(header, notices, live, fallbackEl);
  if (location === "board") { root.append(boardAction); } else { root.append(summaryEl, bodySlot, controls); }
  root.append(footer);
  slot.replaceChildren(root);

  // --- State -----------------------------------------------------------------
  let body: Body | null = null;
  let bodyFailed = false; // permanent: body error or plugin removal; never remounted
  let currentContext = context;
  let accepted: WidgetSnapshot<SessionPreviewV1> | null = null;
  let shown: WidgetPresentation | null = null; // what the body displays
  let shownDetail: DetailSnapshot | null = null;
  let displayWindow: DisplayWindow = "preview";
  let readerExpanded = false; // explicit reader intent
  let pending: WidgetPresentation | null = null; // withheld body content
  let pendingDetail: DetailSnapshot | null = null;
  let detailPaused = false;
  // Transport continuity follows receipt, not the reader's deliberately older window.
  let receivedThroughSeq = 0;
  let releaseDetail: (() => void) | null = null;
  let lastAttentionId: string | null = null;
  let destroyed = false;

  const expanded = () => displayWindow === "expanded";

  function announce(message: string): void { setText(live, message); hooks.onAnnounce?.(message); }
  function setNote(text: string | null): void { controlNote.hidden = !text; setText(controlNote, text ?? ""); }

  // --- Body lifecycle --------------------------------------------------------
  function ensureBody(): Body | null {
    if (bodyFailed || destroyed || location === "board") return null;
    if (!body) {
      try { body = bodyFactory(currentContext); bodySlot.replaceChildren(body.element); }
      catch (error) { failBody(error); return null; }
    }
    return body;
  }

  /** Release body resources without marking failure (unsupported data, no live data). */
  function dropBody(): void {
    try { body?.destroy(); } catch { /* idempotent cleanup */ }
    body = null;
    bodySlot.replaceChildren();
    bodySlot.hidden = true;
    shown = null; shownDetail = null; pending = null; pendingDetail = null; newActivity.hidden = true;
    releaseLease();
    if (expanded()) { displayWindow = "preview"; readerExpanded = false; updateDisclosure(expandButton, "Expand activity", false); }
  }

  function failBody(error: unknown): void {
    bodyFailed = true;
    dropBody();
    controls.hidden = true;
    hooks.onBodyError?.(error);
    showFallback("Widget body failed; showing the saved record.");
  }

  function showFallback(reason: string): void {
    const presentation = fallbackPresentation(accepted?.fallback, reason);
    fallbackEl.hidden = false;
    fallbackEl.replaceChildren(
      notice("neutral", "Details unavailable", reason),
      h("p", { class: "widget-fallback-summary", text: presentation.summary ?? "" }),
    );
    renderFooter(presentation);
  }

  // --- Detail selection (view-scoped lease owned by the host) ----------------
  function onDetail(incoming: DetailSnapshot): void {
    if (destroyed || !expanded()) return; // late/irrelevant
    if (!incoming.reset && incoming.baseSeq !== receivedThroughSeq) {
      detailPaused = true; // gap: pause and ask the owner for a snapshot
      renderBody();
      releaseLease();
      acquireLease();
      return;
    }
    receivedThroughSeq = incoming.throughSeq;
    detailPaused = false;
    if (shownDetail === null) { shownDetail = incoming; renderBody(); return; } // the reader asked for this
    if (detailSignature(incoming) === detailSignature(shownDetail)) { shownDetail = incoming; renderBody(); return; }
    // Explicit expansion holds the reading window; surface the change instead of moving it.
    pendingDetail = incoming;
    newActivity.hidden = false; setText(newActivity, "New activity · show");
    renderBody();
  }

  function onRevoked(reason: DetailRevocation): void {
    releaseDetail = null;
    if (reason === "unavailable") {
      // Releasing transport must not discard the selected/expanded reading window.
      // It stays read-only; reconnecting detail requires a new explicit selection.
      setNote("Detail updates paused; showing last known details. When the service returns, collapse and expand to reconnect.");
      announce(`${shown?.label ?? "Session"}: detail updates paused.`);
      return;
    }
    shownDetail = null; pendingDetail = null; detailPaused = false;
    if (expanded()) {
      const hadFocus = containsFocus(bodySlot);
      displayWindow = shown?.terminal ? "summary" : "preview";
      readerExpanded = false;
      updateDisclosure(expandButton, expandLabel(), false);
      if (hadFocus) expandButton.focus();
    }
    if (reason === "reselected") setNote("Detail is shown for one session at a time; it moved to the session you expanded.");
    if (reason !== "retired") announce(`${shown?.label ?? "Session"}: detail closed (${reason}).`);
    renderBody();
  }

  function acquireLease(): boolean {
    if (releaseDetail || location === "board" || !currentContext.helpers.requestDetail) return Boolean(releaseDetail);
    receivedThroughSeq = 0;
    const release = currentContext.helpers.requestDetail(onDetail, onRevoked);
    if (!release) { setNote("Details are unavailable right now; the preview is the last known state."); return false; }
    releaseDetail = release;
    return true;
  }

  function releaseLease(): void {
    if (!releaseDetail) return;
    const release = releaseDetail; releaseDetail = null; release();
  }

  // --- Reader-owned window ---------------------------------------------------
  function readerEngaged(): boolean { return containsFocus(bodySlot) || containsSelection(root); }
  function held(): boolean { return readerEngaged() || (expanded() && readerExpanded); }

  function expandLabel(): string {
    if (shown?.terminal) return "Expand completed activity";
    return `Expand activity${shown && shown.truncated ? " · more entries" : ""}`;
  }

  function setExpanded(next: boolean, byReader: boolean): void {
    if (expanded() === next) return;
    if (byReader) setNote(null);
    if (next) {
      shownDetail = null; pendingDetail = null; detailPaused = false;
      const previous = displayWindow;
      displayWindow = "expanded"; // before the lease: the first detail snapshot may arrive synchronously
      if (!acquireLease()) { displayWindow = previous; return; } // host declined: stay collapsed, note shown
      if (byReader) readerExpanded = true;
      updateDisclosure(expandButton, "Collapse activity", true);
    } else {
      if (containsFocus(bodySlot)) expandButton.focus();
      releaseLease();
      shownDetail = null; pendingDetail = null; detailPaused = false;
      if (byReader) readerExpanded = false;
      displayWindow = shown?.terminal ? "summary" : "preview";
      updateDisclosure(expandButton, expandLabel(), false);
      // The reader closed the detail; bring the preview up to date unless text is still selected.
      if (pending && !containsSelection(root)) applyPending();
    }
    expandButton.disabled = accepted?.availability !== "available" && !expanded();
    renderBody();
  }

  function applyPending(): void {
    const next = pending; pending = null;
    if (next) applyPresentation(next);
    if (pendingDetail) { shownDetail = pendingDetail; pendingDetail = null; }
    newActivity.hidden = true;
  }

  expandButton.addEventListener("click", () => setExpanded(!expanded(), true));
  collapseFinal.addEventListener("click", () => { readerExpanded = false; setExpanded(false, true); });
  newActivity.addEventListener("click", () => { applyPending(); renderBody(); });

  // --- Rendering (header/notices/footer always follow the accepted snapshot) --
  function renderHeader(presentation: WidgetPresentation, snapshot: WidgetSnapshot): void {
    setText(label, presentation.label);
    updateStatus(statusEl, presentation.status);
    updateFreshness(freshnessEl, freshnessValue(snapshot, hooks.now()));
    setAttr(root, "data-tone", presentation.status.tone);
    setAttr(root, "data-terminal", String(presentation.terminal));
  }

  function renderNotices(presentation: WidgetPresentation, snapshot: WidgetSnapshot, extra: HTMLElement[] = []): void {
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
    notices.append(...extra);
  }

  function renderFooter(presentation: WidgetPresentation): void {
    type Meta = { key: string; text: string; tabular?: boolean; title?: string };
    const meta: Meta[] = [{ key: "session", text: `Session ${shortId(identity.instanceId)}`, title: identity.instanceId }];
    const started = formatClock(presentation.startedAt);
    const duration = formatDuration(presentation.durationMs);
    if (started) meta.push({ key: "started", text: `Started ${started}`, tabular: true });
    if (duration) meta.push({ key: "duration", text: duration, tabular: true });
    if (presentation.usage?.tokens !== undefined) meta.push({ key: "tokens", text: `${presentation.usage.tokens.toLocaleString()} tokens`, tabular: true });
    reconcile(footerMeta, meta, (item) => item.key,
      (item) => h("span", { class: item.tabular ? "wk-tabular" : undefined, title: item.title }),
      (el, item) => setText(el, item.text));
    // Reject unsafe/off-origin destinations rather than render a dead link.
    const links = presentation.references.map((ref) => ({ ref, href: currentContext.helpers.hrefFor(ref) })).filter((item): item is { ref: WidgetReference; href: string } => item.href !== null);
    reconcile(footerLinks, links, (item) => `${item.ref.kind}:${item.href}`,
      (item) => {
        const anchor = reference(item.ref, item.href, "");
        if (item.ref.kind === "session") anchor.id = `${baseId}-open`; // stable node AND id: focus survives streaming, history restore can return here
        return anchor;
      },
      (el, item) => {
        const text = item.ref.kind === "session" ? `Open session${location === "board" ? "" : " →"}` : item.ref.label;
        setText(el, text);
        setAttr(el, "aria-label", item.ref.kind === "session" ? `Open session ${identity.instanceId} for ${identity.taskId}` : item.ref.label);
      });
  }

  function renderBody(): void {
    if (location === "board" || !shown) return;
    const terminal = shown.terminal;
    const collapsedToSummary = displayWindow === "summary";
    summaryEl.hidden = !collapsedToSummary;
    if (collapsedToSummary) {
      summaryEl.replaceChildren(
        h("p", { text: shown.summary ?? "Session completed; no summary was published" }),
        h("div", { class: "wk-meta" }, h("span", { text: "Summary saved" }), h("span", { text: "No approval implied" })),
      );
    }
    collapseFinal.hidden = !(terminal && expanded());
    bodySlot.hidden = collapsedToSummary;
    if (collapsedToSummary) return; // nothing to update inside a hidden body; keep it for reopen
    const current = ensureBody(); if (!current) return;
    try { current.update({ presentation: shown, context: currentContext, expanded: expanded(), detail: shownDetail, detailPaused }); }
    catch (error) { failBody(error); }
  }

  /** Move the accepted presentation into the reading window (never called while held). */
  function applyPresentation(presentation: WidgetPresentation): void {
    const wasTerminal = shown?.terminal ?? false;
    shown = presentation;
    if (presentation.terminal && !wasTerminal) {
      if (!readerExpanded) { releaseLease(); shownDetail = null; pendingDetail = null; displayWindow = "summary"; }
      announce(`${presentation.label}: ${presentation.status.text}.`);
    }
    if (!presentation.terminal && displayWindow === "summary") displayWindow = "preview";
    if (!expanded()) updateDisclosure(expandButton, expandLabel(), false);
  }

  const instance: WidgetInstance<SessionPreviewV1> = {
    update(snapshot, ctx) {
      if (destroyed) return;
      currentContext = ctx;
      accepted = snapshot;
      fallbackEl.hidden = true;
      if (snapshot.availability === "plugin_removed") {
        if (!bodyFailed) failBody(new Error("plugin removed"));
        showFallback("Plugin removed from this workspace. Saved record shown without plugin code.");
        renderHeader(fallbackPresentation(snapshot.fallback, ""), snapshot);
        renderNotices({ ...fallbackPresentation(snapshot.fallback, ""), attention: undefined }, snapshot);
        return;
      }
      const presentation = snapshot.data && adapter.supportedVersions.includes(snapshot.data.version) ? adapter.present(snapshot.data) : null;
      if (!presentation) {
        // Unsupported or absent live data: generic saved record. The body and
        // any detail selection are released; they are recreated only when
        // supported data returns (this is not a failure, so no permanent retire).
        const reason = snapshot.data ? "Unsupported session data; showing the saved record." : snapshot.freshness.rehydrating ? "Awaiting rehydration; showing the saved record." : "No live data; showing the saved record.";
        const fallback = fallbackPresentation(snapshot.fallback, reason);
        renderHeader({ ...fallback, status: { text: snapshot.fallback?.statusLabel ?? "Unknown", tone: "neutral" } }, snapshot);
        renderNotices({ ...fallback, attention: undefined }, snapshot, snapshot.data ? [notice("neutral", "Unsupported session data", `Version ${snapshot.data.version} is not supported by this widget; the saved record is shown instead.`)] : []);
        renderFooter(fallback);
        if (location === "activity") {
          dropBody();
          summaryEl.hidden = false; summaryEl.replaceChildren(h("p", { text: fallback.summary ?? "" }));
          controls.hidden = true; collapseFinal.hidden = true;
        } else {
          setText(boardAction, snapshot.fallback?.statusLabel ?? "");
        }
        return;
      }
      // Header, notices and footer always reflect the accepted snapshot —
      // an input request must surface even while the body window is held.
      renderHeader(presentation, snapshot);
      renderNotices(presentation, snapshot);
      renderFooter(presentation);
      if (location === "board") { setText(boardAction, presentation.action ?? ""); boardAction.hidden = !presentation.action; return; }
      if (bodyFailed) { showFallback("Widget body failed; showing the saved record."); return; }
      controls.hidden = false;
      const unavailable = snapshot.availability !== "available";
      // Outages release the lease, not the reader's last-known detail. Collapse
      // remains usable, but a collapsed card cannot acquire unavailable detail.
      expandButton.disabled = unavailable && !expanded();
      if (unavailable) releaseLease();
      // Reader-owned window: withhold changed body content, and the collapse to
      // a summary, while the reader is inside the body or has explicitly
      // expanded it. Unchanged content (for example a terminal status with the
      // same entries) applies silently; an explicitly expanded card then stays
      // expanded and offers Collapse.
      if (shown && held()) {
        const contentChanged = bodySignature(presentation) !== bodySignature(shown);
        const wouldCollapse = presentation.terminal && !shown.terminal && !readerExpanded;
        if (contentChanged || wouldCollapse) {
          pending = presentation;
          newActivity.hidden = false;
          setText(newActivity, wouldCollapse && !contentChanged ? "Session finished · show summary" : "New activity · show");
          return;
        }
      }
      pending = null; newActivity.hidden = pendingDetail === null;
      applyPresentation(presentation);
      renderBody();
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
