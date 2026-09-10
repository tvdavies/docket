// Illustrative themed kit roles as small DOM builders. Each role renders text
// plus tone/icon, never color alone. Not a public package: JOB-0093 owns the
// production kit; these exist so the prototype's standard and custom bodies
// share one vocabulary.

import { formatDuration, h, setAttr, setText } from "../dom";
import type { WidgetReference } from "../../contracts.proposed";

export type Tone = "neutral" | "info" | "positive" | "warning" | "danger";

export interface StatusValue { text: string; tone: Tone; icon?: string }

export function status(value: StatusValue, extraClass = ""): HTMLSpanElement {
  const el = h("span", { class: `wk-status ${extraClass}`.trim(), "data-tone": value.tone });
  updateStatus(el, value);
  return el;
}

export function updateStatus(el: HTMLElement, value: StatusValue): void {
  setAttr(el, "data-tone", value.tone);
  setText(el, value.icon ? `${value.icon} ${value.text}` : value.text);
}

export interface FreshnessValue { connection: "connecting" | "live" | "disconnected"; stale: boolean; text: string }

export function freshness(value: FreshnessValue): HTMLSpanElement {
  const el = h("span", { class: "wk-freshness" }, h("i", { "aria-hidden": "true" }), h("span"));
  updateFreshness(el, value);
  return el;
}

export function updateFreshness(el: HTMLElement, value: FreshnessValue): void {
  setAttr(el, "data-connection", value.connection);
  setAttr(el, "data-stale", String(value.stale));
  setText(el.lastElementChild!, value.text);
}

export function notice(tone: Tone, title: string, message: string, footnote?: string): HTMLDivElement {
  return h("div", { class: "wk-notice", "data-tone": tone, role: tone === "danger" ? "alert" : "status" },
    h("strong", { text: title }),
    h("span", { text: message }),
    footnote ? h("small", { text: footnote }) : null,
  );
}

export function disclosure(label: string, expanded: boolean, controls: string): HTMLButtonElement {
  const button = h("button", { type: "button", class: "wk-disclosure", "aria-expanded": String(expanded), "aria-controls": controls },
    h("span", { class: "wk-caret", "aria-hidden": "true", text: "▸" }),
    h("span", { class: "wk-disclosure-label", text: label }),
  );
  return button;
}

export function updateDisclosure(button: HTMLElement, label: string, expanded: boolean): void {
  setAttr(button, "aria-expanded", String(expanded));
  setText(button.querySelector(".wk-disclosure-label")!, label);
}

export function safeText(text: string): HTMLParagraphElement {
  // Text nodes only: no HTML, no remote images, no links inside streamed prose.
  return h("p", { class: "wk-text", text });
}

export function code(text: string): HTMLPreElement {
  return h("pre", { class: "wk-code", text });
}

export function reference(ref: WidgetReference, href: string, label = ref.label): HTMLAnchorElement {
  return h("a", { class: "wk-reference", href, "data-ref-kind": ref.kind, text: label });
}

export function toolStatusLabel(statusValue: string, durationMs?: number): string {
  const duration = formatDuration(durationMs);
  const base = statusValue === "running" ? "Running" : statusValue === "completed" ? "Completed" : statusValue === "failed" ? "Failed" : "Cancelled";
  return duration && statusValue !== "running" ? `${base} · ${duration}` : base;
}

export function toolTone(statusValue: string): Tone {
  return statusValue === "failed" ? "danger" : statusValue === "running" ? "info" : statusValue === "cancelled" ? "warning" : "neutral";
}
