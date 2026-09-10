// Two bodies for the same wrapper contract:
//   standardBody  — themed composition in light DOM.
//   <demo-session-body> — one statically registered, namespaced custom element
//                  with Shadow DOM for style isolation. It inherits the
//                  `--widget-*` tokens, adopts the same kit sheet and renders
//                  the same ordered entries. It does not inject global styles,
//                  portal outside its slot or import host internals.
//
// Shadow DOM here is style isolation only. Trusted custom code can still
// reach the page; conformance is by review and tests, not a sandbox.

import type { PreviewEntry, WidgetContext, WidgetPresentation } from "../../contracts.proposed";
import { PROPOSED_BUDGETS } from "../../contracts.proposed";
import { h, setAttr, setText } from "../dom";
import kitCss from "./kit.css" with { type: "text" };
import { renderEntries } from "./preview";
import type { Body, BodyUpdate } from "./wrapper";

let cleanupCounter = 0;
export function cleanupCount(): number { return cleanupCounter; }
export function resetCleanupCount(): void { cleanupCounter = 0; }

/** Shared render core: builds the body DOM inside `container`. */
function createCore(container: ParentNode, context: WidgetContext, variant: "standard" | "custom"): { update(update: BodyUpdate): void; destroy(): void } {
  const idPrefix = `${variant}-${context.identity.instanceId}`;
  const list = h("ol", { class: "wk-steps", "aria-label": "Ordered session activity" });
  const truncation = h("p", { class: "wk-truncation", hidden: true });
  const empty = h("p", { class: "wk-empty", hidden: true, text: "Running; no activity yet." });
  const paused = h("p", { class: "wk-notice", "data-tone": "neutral", hidden: true, role: "status", text: "Detail stream paused: a range was missing. Requested a fresh snapshot." });
  const more = h("p", { class: "wk-more", hidden: true });
  const wrap = h("div", { class: `body-core body-${variant}` }, empty, list, truncation, paused, more);
  container.append(wrap);
  let destroyed = false;

  return {
    update({ presentation, expanded, detail, detailPaused, context: ctx }: BodyUpdate) {
      if (destroyed) throw new Error("body updated after destroy");
      const entries: PreviewEntry[] = expanded && detail ? detail.entries : presentation.entries;
      empty.hidden = entries.length > 0 || presentation.terminal;
      const mode = expanded ? "expanded" : "preview";
      // Preview rows are plain groups; expanded rows are disclosures. A mode
      // change (reader clicked Expand/Collapse) rebuilds rows once; streaming
      // updates within a mode keep DOM identity.
      if (list.dataset.mode !== mode) { list.replaceChildren(); setAttr(list, "data-mode", mode); }
      renderEntries(list, entries, { detail: expanded, idPrefix });
      paused.hidden = !detailPaused;
      const budget = expanded ? `${PROPOSED_BUDGETS.expandedEntries} entries / ${PROPOSED_BUDGETS.expandedAssistantChars} characters` : `${PROPOSED_BUDGETS.previewEntries} entries / ${PROPOSED_BUDGETS.previewAssistantChars} characters`;
      truncation.hidden = !(expanded && detail ? detail.truncated : presentation.truncated);
      setText(truncation, `Earlier activity not shown (bounded to ${budget}).`);
      more.hidden = !expanded;
      if (expanded) {
        const session = presentation.references.find((ref) => ref.kind === "session");
        const href = session ? ctx.helpers.hrefFor(session) : null;
        // Rebuild only when the destination changes so a focused link survives streaming.
        if (more.dataset.href !== (href ?? "")) {
          more.dataset.href = href ?? "";
          more.replaceChildren("More in the full session: ", href ? h("a", { class: "wk-reference", href, text: "open transcript" }) : h("span", { text: "link unavailable" }));
        }
      }
    },
    destroy() { if (destroyed) return; destroyed = true; cleanupCounter += 1; wrap.remove(); },
  };
}

export function standardBody(context: WidgetContext): Body {
  const element = h("div", { class: "widget-body widget-body-standard", "data-body": "standard" });
  const core = createCore(element, context, "standard");
  return { element, update: (update) => core.update(update), destroy: () => core.destroy() };
}

// --- Custom element body -----------------------------------------------------

let kitSheet: CSSStyleSheet | null = null;
function sharedKitSheet(): CSSStyleSheet {
  if (!kitSheet) {
    kitSheet = new CSSStyleSheet();
    kitSheet.replaceSync(`${kitCss}\n:host { display: block; color: var(--widget-text); font-family: var(--widget-font); font-size: var(--widget-text-size); }\n.custom-chrome { display: grid; gap: var(--widget-gap); }\n.custom-badge { justify-self: start; }`);
  }
  return kitSheet;
}

export class DemoSessionBody extends HTMLElement {
  static readonly tagName = "demo-session-body";
  private core: ReturnType<typeof createCore> | null = null;
  private destroyed = false;

  constructor() {
    super();
    const shadow = this.attachShadow({ mode: "open" });
    shadow.adoptedStyleSheets = [sharedKitSheet()];
  }

  /** Called by the demo adapter after construction; not a public API. */
  bind(context: WidgetContext): void {
    const shadow = this.shadowRoot!;
    const chrome = h("div", { class: "custom-chrome" },
      h("span", { class: "wk-status custom-badge", "data-tone": "info", text: "Custom element body · Shadow DOM" }),
    );
    shadow.append(chrome);
    this.core = createCore(chrome, context, "custom");
  }

  updateBody(update: BodyUpdate): void {
    if (!this.core) throw new Error("custom body used before bind()");
    this.core.update(update);
  }

  destroyBody(): void {
    if (this.destroyed) return;
    this.destroyed = true;
    this.core?.destroy();
    this.core = null;
    this.remove();
  }

  /** Disconnect cleanup must tolerate host destroy() having already run. */
  disconnectedCallback(): void { this.destroyBody(); }
}

export function registerDemoElement(): void {
  // Static, namespaced registration. Definitions cannot be unregistered; a
  // real host must settle duplicate/version conflicts (JOB-0093).
  if (!customElements.get(DemoSessionBody.tagName)) customElements.define(DemoSessionBody.tagName, DemoSessionBody);
}

export function customElementBody(context: WidgetContext): Body {
  registerDemoElement();
  const element = document.createElement(DemoSessionBody.tagName) as DemoSessionBody;
  element.setAttribute("data-body", "custom-element");
  element.bind(context);
  return { element, update: (update) => element.updateBody(update), destroy: () => element.destroyBody() };
}

/** Body that throws on demand — used only by the body-error fixture. */
export function throwingBody(shouldThrow: () => boolean): (context: WidgetContext) => Body {
  return (context) => {
    const inner = standardBody(context);
    return { element: inner.element, update: (update) => { if (shouldThrow()) throw new Error("synthetic body failure"); inner.update(update); }, destroy: () => inner.destroy() };
  };
}

export function presentationLabel(presentation: WidgetPresentation): string { return presentation.label; }
