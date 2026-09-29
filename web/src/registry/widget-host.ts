import {
  boundedPresentation,
  safeHref,
  WIDGET_BUDGETS,
  type DetailLease,
  type WidgetContext,
  type WidgetData,
  type WidgetInstance,
  type WidgetModule,
  type WidgetPresentation,
  type WidgetRenderState,
  type WidgetSnapshot,
} from "@docket/plugin-ui";
import { DetailController } from "./detail-controller";

type ContextInput = Omit<WidgetContext, "signal" | "helpers"> & {
  refresh(): void;
};
let nextID = 0;
function setText(node: HTMLElement, text: string) {
  if (node.textContent !== text) node.textContent = text;
}
/** Host-owned DOM and reader state. The body is the only authored mount point. */
export class WidgetHost {
  private label = document.createElement("strong");
  private status = document.createElement("span");
  private freshness = document.createElement("small");
  private notice = document.createElement("p");
  private body = document.createElement("div");
  private fallback = document.createElement("p");
  private footer = document.createElement("div");
  private times = document.createElement("small");
  private toggle = document.createElement("button");
  private apply = document.createElement("button");
  private retry = document.createElement("button");
  private links = new Map<string, HTMLElement>();
  private instance?: WidgetInstance;
  private abort?: AbortController;
  private lease?: DetailLease;
  private snapshot?: WidgetSnapshot;
  private displayed?: WidgetPresentation;
  private shownData?: WidgetData;
  private pending?: { presentation: WidgetPresentation; data?: WidgetData };
  private context: WidgetContext;
  private expanded = false;
  private failed = false;
  private stopped = false;
  private renderTimer?: ReturnType<typeof setTimeout>;
  private frame?: number;
  private lastRender = -Infinity;
  private transportGeneration = -1;
  private latest?: WidgetPresentation;
  private detailNotice = "";
  private selectedData?: WidgetData;
  constructor(
    readonly root: HTMLElement,
    private input: ContextInput,
    private module?: WidgetModule,
    private detail?: DetailController,
  ) {
    this.context = this.makeContext(new AbortController());
    root.className = "docket-widget";
    root.tabIndex = -1;
    root.dataset.widget = input.identity.widgetType;
    root.dataset.instance = input.identity.instanceId;
    root.dataset.location = input.location;
    const header = document.createElement("header");
    const pluginName = document.createElement("small");
    pluginName.textContent = input.identity.widgetType.split("/")[0];
    header.append(pluginName, this.label, this.status, this.freshness);
    this.status.setAttribute("role", "status");
    this.status.setAttribute("aria-live", "polite");
    this.notice.className = "widget-notice";
    this.notice.setAttribute("role", "status");
    this.body.className = "widget-body";
    this.body.id = `widget-body-${++nextID}`;
    this.fallback.className = "widget-fallback";
    this.footer.className = "widget-references";
    this.footer.append(this.times);
    for (const button of [this.toggle, this.apply, this.retry])
      button.type = "button";
    this.toggle.textContent = "Expand activity";
    this.toggle.setAttribute("aria-controls", this.body.id);
    this.toggle.setAttribute("aria-expanded", "false");
    this.toggle.addEventListener("click", () => this.toggleExpanded());
    this.apply.addEventListener("click", () => this.applyPending());
    this.retry.textContent = "Retry widget";
    this.retry.addEventListener("click", () => {
      this.failed = false;
      this.retire();
      if (this.snapshot)
        this.update(this.snapshot, this.input, this.transportGeneration);
      this.input.refresh();
    });
    const controls = document.createElement("div");
    controls.className = "widget-controls";
    controls.append(this.toggle, this.apply, this.retry);
    root.append(
      header,
      this.notice,
      this.body,
      this.fallback,
      controls,
      this.footer,
    );
  }
  private makeContext(abort: AbortController): WidgetContext {
    return {
      apiVersion: 2,
      identity: this.input.identity,
      location: this.input.location,
      serviceBase: this.input.serviceBase,
      preferences: this.input.preferences,
      signal: abort.signal,
      helpers: {
        refreshTask: () => {
          if (!abort.signal.aborted) this.input.refresh();
        },
        hrefFor: (ref) => safeHref(ref, this.input.identity),
        ...(this.input.location === "activity"
          ? {
              requestDetail: (
                listener: Parameters<DetailController["request"]>[5],
                onRevoked: Parameters<DetailController["request"]>[6],
              ) => {
                if (abort.signal.aborted || !this.canDetail()) return null;
                const lease = this.detail!.request(
                  this.input.identity,
                  this.input.serviceBase,
                  this.module!.detail!,
                  this.module!.dataVersions,
                  abort.signal,
                  listener,
                  (reason) => {
                    this.lease = undefined;
                    if (!["released", "retired"].includes(reason)) {
                      this.detailNotice =
                        reason === "reselected"
                          ? "Detail selected in another widget"
                          : `Details unavailable · ${reason.replaceAll("_", " ")}. Collapse and expand to retry.`;
                      this.notice.textContent = this.detailNotice;
                      this.notice.hidden = false;
                    }
                    onRevoked(reason);
                  },
                );
                this.lease = lease || undefined;
                return lease;
              },
            }
          : {}),
      },
    };
  }
  update(
    snapshot: WidgetSnapshot,
    input: ContextInput = this.input,
    generation = this.transportGeneration,
  ) {
    if (this.stopped) return;
    this.input = input;
    this.snapshot = snapshot;
    this.context = { ...this.context, preferences: input.preferences };
    this.root.dataset.theme = input.preferences.theme;
    this.root.dataset.density = input.preferences.density;
    this.root.dataset.motion = input.preferences.reducedMotion
      ? "reduced"
      : "normal";
    if (generation !== this.transportGeneration) {
      this.lease?.release();
      this.lease = undefined;
      this.transportGeneration = generation;
    }
    const supported =
      !!this.module &&
      (!snapshot.data ||
        this.module.dataVersions.includes(snapshot.data.version));
    const runnable =
      supported &&
      !["plugin_disabled", "module_missing", "unsupported", "error"].includes(
        snapshot.availability,
      ) &&
      !this.failed;
    if (!this.canDetail()) {
      this.lease?.release();
      this.lease = undefined;
    }
    let presentation = this.saved(snapshot);
    if (runnable && snapshot.data) {
      try {
        const p = this.module!.present(snapshot, this.context);
        if (p) presentation = boundedPresentation(p, true);
        else {
          this.retire();
          snapshot = { ...snapshot, availability: "unsupported" };
          this.snapshot = snapshot;
        }
      } catch {
        this.fail();
      }
    }
    this.latest = presentation;
    if (this.failed) {
      this.controls();
      return;
    }
    // Metadata is always current, including while the reader holds the body.
    setText(this.label, presentation.label);
    setText(this.status, presentation.status.text);
    this.status.dataset.tone = presentation.status.tone;
    setText(
      this.freshness,
      snapshot.fallback?.phase === "finalised"
        ? "Saved"
        : snapshot.freshness.rehydrating
          ? "Awaiting live data"
          : snapshot.freshness.stale
            ? "Last known · stale"
            : snapshot.freshness.connection === "open"
              ? "Live preview"
              : "Disconnected",
    );
    const unavailable = this.failed
      ? "Widget unavailable"
      : snapshot.availability !== "available"
        ? snapshot.availability.replaceAll("_", " ")
        : !supported
          ? "Unsupported widget data"
          : "";
    const notices = [presentation.notice?.text, unavailable, this.detailNotice]
      .filter(Boolean)
      .join(" · ");
    if (this.notice.textContent !== notices) this.notice.textContent = notices;
    this.notice.hidden = !notices;
    this.notice.dataset.tone = presentation.notice?.tone || "info";
    this.renderReferences(presentation);
    if (!runnable || this.snapshot?.availability === "unsupported") {
      this.retire();
      this.fallback.textContent =
        snapshot.fallback?.fallback.summary ||
        `${presentation.label} · ${presentation.status.text}`;
      this.fallback.hidden = false;
      this.controls();
      return;
    }
    if (!snapshot.data && !this.instance) {
      this.fallback.textContent =
        snapshot.fallback?.fallback.summary ||
        "Saved record · awaiting owner publication";
      this.fallback.hidden = false;
      this.controls();
      return;
    }
    if (!this.instance) {
      this.abort = new AbortController();
      this.context = this.makeContext(this.abort);
      try {
        this.instance = this.module!.mount(this.body, this.context);
      } catch {
        this.fail();
        this.controls();
        return;
      }
    }
    let bodyPresentation = presentation,
      bodyData = snapshot.data;
    if (
      this.expanded &&
      this.selectedData &&
      snapshot.fallback?.phase !== "finalised"
    ) {
      bodyData = this.selectedData;
      try {
        const p = this.module!.present(
          { ...snapshot, data: bodyData },
          this.context,
        );
        if (p) bodyPresentation = boundedPresentation(p, true);
      } catch {
        this.fail();
        return;
      }
    }
    const changed =
      JSON.stringify([bodyPresentation, bodyData]) !==
      JSON.stringify([this.displayed, this.shownData]);
    if (changed)
      this.pending = { presentation: bodyPresentation, data: bodyData };
    const urgent =
      presentation.terminal ||
      !!presentation.notice ||
      snapshot.availability !== "available";
    this.schedule(urgent);
    this.controls();
  }
  private saved(snapshot: WidgetSnapshot): WidgetPresentation {
    const f = snapshot.fallback?.fallback;
    return {
      label: f?.label || this.input.identity.widgetType,
      status: {
        text: f?.status_label || "Saved widget",
        tone:
          f?.priority === "error"
            ? "danger"
            : f?.priority === "attention"
              ? "warning"
              : "neutral",
      },
      priority: f?.priority || "history",
      terminal: snapshot.fallback?.phase === "finalised",
      summary: f?.summary,
      references: f?.references,
    };
  }
  private isHeld() {
    const selection = this.root.ownerDocument.getSelection();
    let anchor = selection?.anchorNode;
    let selected = false;
    while (anchor) {
      if (this.body.contains(anchor)) {
        selected = !!selection && !selection.isCollapsed;
        break;
      }
      const root = anchor.getRootNode();
      anchor = root instanceof ShadowRoot ? root.host : null;
    }
    return (
      this.expanded || this.body.contains(document.activeElement) || selected
    );
  }
  private canDetail() {
    return (
      !!this.detail &&
      !!this.module?.detail &&
      !!this.input.serviceBase &&
      !!this.snapshot?.data &&
      this.snapshot.availability === "available" &&
      !this.snapshot.freshness.stale &&
      !this.snapshot.freshness.rehydrating &&
      this.snapshot.freshness.connection === "open" &&
      this.snapshot.fallback?.phase !== "finalised" &&
      !this.failed
    );
  }
  private schedule(urgent = false) {
    if (this.stopped) return;
    if (urgent) {
      clearTimeout(this.renderTimer);
      this.renderTimer = undefined;
      if (this.frame === undefined)
        this.frame = requestAnimationFrame(() => {
          this.frame = undefined;
          this.render();
        });
      return;
    }
    if (this.renderTimer || this.frame !== undefined) return;
    const wait = Math.max(
      0,
      WIDGET_BUDGETS.renderMS - (performance.now() - this.lastRender),
    );
    if (wait === 0) this.render();
    else
      this.renderTimer = setTimeout(() => {
        this.renderTimer = undefined;
        this.render();
      }, wait);
  }
  private render() {
    if (!this.instance || !this.snapshot || this.stopped) return;
    this.lastRender = performance.now();
    if (this.pending && (!this.displayed || !this.isHeld())) {
      this.displayed = this.pending.presentation;
      this.shownData = this.pending.data;
      this.pending = undefined;
    }
    const view: WidgetRenderState = {
      expanded: this.expanded,
      held: this.isHeld(),
      displayed: this.displayed
        ? boundedPresentation(this.displayed, this.expanded)
        : undefined,
      data: this.shownData,
      pending: !!this.pending,
    };
    try {
      this.instance.update(this.snapshot, this.context, view);
      this.fallback.hidden = true;
    } catch {
      this.fail();
    }
    this.controls();
  }
  private controls() {
    this.toggle.hidden =
      this.input.location === "board" ||
      !this.instance ||
      (!this.module?.detail && !this.expanded);
    this.toggle.disabled = !this.expanded && !this.canDetail();
    this.toggle.textContent = this.expanded
      ? "Collapse activity"
      : "Expand activity";
    this.toggle.setAttribute("aria-expanded", String(this.expanded));
    this.apply.hidden = !this.pending;
    this.apply.textContent = this.pending?.presentation.terminal
      ? "Finished · show summary"
      : "New activity · show";
    this.retry.hidden = !this.failed;
  }
  private toggleExpanded() {
    if (this.expanded) {
      this.expanded = false;
      this.selectedData = undefined;
      this.lease?.release();
      this.lease = undefined;
      this.toggle.focus();
      if (this.latest)
        this.pending = { presentation: this.latest, data: this.snapshot?.data };
      this.applyPending();
      return;
    }
    if (!this.canDetail()) return;
    this.detailNotice = "";
    this.expanded = true;
    this.lease =
      this.context.helpers.requestDetail?.(
        (frame) => {
          if (!this.snapshot || !this.module) return;
          const data = {
            version: frame.dataVersion,
            revision: frame.revision,
            value: frame.value,
          };
          try {
            const p = this.module.present(
              { ...this.snapshot, data },
              this.context,
            );
            if (p) {
              const presentation = boundedPresentation(p, true);
              if (!this.selectedData) {
                this.displayed = presentation;
                this.shownData = data;
                this.pending = undefined;
              } else this.pending = { presentation, data };
              this.selectedData = data;
              this.schedule(true);
            }
            this.controls();
          } catch {
            this.fail();
          }
        },
        (reason) => {
          this.lease = undefined;
          if (reason === "reselected") {
            this.expanded = false;
            this.selectedData = undefined;
            this.displayed = this.latest;
            this.shownData = this.snapshot?.data;
            this.pending = undefined;
            this.notice.textContent = "Detail selected in another widget";
            this.render();
          }
          this.controls();
        },
      ) || undefined;
    if (!this.lease) this.expanded = false;
    this.render();
    this.controls();
  }
  private applyPending() {
    if (this.pending) {
      this.displayed = this.pending.presentation;
      this.shownData = this.pending.data;
      this.pending = undefined;
    } else if (!this.expanded && this.latest) {
      this.displayed = this.latest;
      this.shownData = this.snapshot?.data;
    }
    this.render();
    this.controls();
  }
  private renderReferences(p: WidgetPresentation) {
    const start = p.startedAt || this.snapshot?.fallback?.fallback.started_at,
      end = p.endedAt || this.snapshot?.fallback?.fallback.ended_at;
    const startMS = typeof start === "string" ? Date.parse(start) : NaN,
      endMS = typeof end === "string" ? Date.parse(end) : NaN;
    setText(
      this.times,
      Number.isFinite(startMS)
        ? `Started ${new Date(startMS).toLocaleTimeString()}${Number.isFinite(endMS) && endMS >= startMS ? ` · ${Math.round((endMS - startMS) / 1000)}s elapsed` : ""}`
        : "",
    );
    this.times.hidden = !this.times.textContent;
    const keep = new Set<string>();
    for (const ref of (p.references || []).slice(0, 8)) {
      const href = safeHref(ref, this.input.identity);
      const key = JSON.stringify([ref.kind, ref.url]);
      keep.add(key);
      let node = this.links.get(key);
      if (!node) {
        node = document.createElement(href ? "a" : "span");
        this.links.set(key, node);
        this.footer.append(node);
      }
      if (node.textContent !== ref.title) node.textContent = ref.title;
      if (node instanceof HTMLAnchorElement && href) {
        node.setAttribute("href", href);
        node.rel = "noreferrer";
      }
    }
    for (const [key, node] of this.links)
      if (!keep.has(key)) {
        if (node.contains(document.activeElement))
          this.root.focus({ preventScroll: true });
        node.remove();
        this.links.delete(key);
      }
  }
  private fail() {
    this.failed = true;
    this.retire();
    this.notice.textContent = "Widget unavailable";
    this.notice.hidden = false;
    this.fallback.hidden = false;
    this.fallback.textContent =
      this.snapshot?.fallback?.fallback.summary || "Saved widget";
  }
  private retire() {
    clearTimeout(this.renderTimer);
    this.renderTimer = undefined;
    if (this.frame !== undefined) cancelAnimationFrame(this.frame);
    this.frame = undefined;
    this.abort?.abort();
    this.lease?.release();
    this.lease = undefined;
    const instance = this.instance;
    this.instance = undefined;
    const focused = this.body.contains(document.activeElement);
    try {
      instance?.destroy();
    } catch {
      /* destroy is attempted exactly once */
    } finally {
      this.body.replaceChildren();
      if (this.body.shadowRoot) this.body.shadowRoot.replaceChildren();
      // A standard body may have attached a ShadowRoot: replace the slot, not the wrapper.
      if (this.body.shadowRoot) {
        const next = document.createElement("div");
        next.className = this.body.className;
        next.id = this.body.id;
        this.body.replaceWith(next);
        this.body = next;
      }
      if (focused) this.root.focus({ preventScroll: true });
    }
    this.expanded = false;
    this.selectedData = undefined;
    this.displayed = undefined;
    this.shownData = undefined;
    this.pending = undefined;
  }
  destroy() {
    if (this.stopped) return;
    this.stopped = true;
    this.retire();
    this.root.replaceChildren();
  }
}
