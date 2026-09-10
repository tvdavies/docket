// Shared ordered text/tool rendering used by BOTH the standard body and the
// custom-element body. Entry identity is preserved across updates: a tool that
// starts, updates and completes (or fails) keeps the same DOM node at its
// original position.

import type { DetailEntry, DetailToolEntry, PreviewEntry } from "../../contracts.proposed";
import { h, reconcile, setAttr, setText } from "../dom";
import { code, toolStatusLabel } from "./kit";

/** Full-session only: user messages never appear in board/task previews. */
export interface UserEntry { id: string; seq: number; type: "user"; text: string }
export type RenderableEntry = PreviewEntry | DetailEntry | UserEntry;

export interface EntriesOptions {
  /** Expanded task view: tool rows become disclosures over redacted detail. */
  detail: boolean;
  /** Full session: failed tools open by default. */
  failedOpen?: boolean;
  idPrefix: string;
}

export function renderEntries(list: HTMLElement, entries: RenderableEntry[], options: EntriesOptions): void {
  reconcile(list, entries, (entry) => entry.id,
    (entry) => createEntry(entry, options),
    (el, entry) => updateEntry(el as HTMLElement, entry, options));
}

function createEntry(entry: RenderableEntry, options: EntriesOptions): HTMLElement {
  if (entry.type === "assistant") return h("li", { class: "wk-step", "data-type": "assistant", "data-entry-id": entry.id });
  if (entry.type === "user") return h("li", { class: "wk-step", "data-type": "user", "data-entry-id": entry.id }, h("span", { class: "user-label", text: "You" }), h("span", { class: "user-text" }));
  const detailId = `${options.idPrefix}-${entry.toolCallId}-detail`;
  const li = h("li", { class: "wk-step", "data-type": "tool", "data-entry-id": entry.id, "data-tool-call": entry.toolCallId });
  const row = options.detail
    ? h("button", { type: "button", class: "wk-tool-row", "aria-expanded": "false", "aria-controls": detailId })
    : h("div", { class: "wk-tool-row", role: "group" });
  row.append(
    h("span", { class: "wk-caret", "aria-hidden": "true", text: options.detail ? "▸" : "•" }),
    h("span", { class: "wk-label" }),
    h("span", { class: "wk-tool-status" }),
  );
  li.append(row);
  if (options.detail) {
    const detail = h("dl", { class: "wk-tool-detail", id: detailId, hidden: true });
    li.append(detail);
    row.addEventListener("click", () => {
      const open = row.getAttribute("aria-expanded") !== "true";
      setAttr(row, "aria-expanded", String(open));
      detail.hidden = !open;
      row.dataset.userToggled = "true";
    });
  }
  return li;
}

function updateEntry(el: HTMLElement, entry: RenderableEntry, options: EntriesOptions): void {
  if (entry.type === "assistant") { setText(el, entry.text); return; }
  if (entry.type === "user") { setText(el.querySelector(".user-text")!, entry.text); return; }
  setAttr(el, "data-status", entry.status);
  const row = el.querySelector(".wk-tool-row") as HTMLElement;
  setAttr(row, "data-status", entry.status);
  const label = entry.summary ? `${entry.label} — ${entry.summary}` : entry.label;
  setText(row.querySelector(".wk-label")!, label);
  setText(row.querySelector(".wk-tool-status")!, toolStatusLabel(entry.status, entry.durationMs));
  if (row instanceof HTMLButtonElement) {
    const statusLabel = toolStatusLabel(entry.status, entry.durationMs);
    row.setAttribute("aria-label", `${entry.label}, ${statusLabel}, details`);
    const detail = el.querySelector(".wk-tool-detail") as HTMLElement;
    const detailEntry = entry as DetailToolEntry;
    detail.replaceChildren(
      h("div", {}, h("dt", { text: "Arguments (redacted)" }), h("dd", {}, detailEntry.input ? code(detailEntry.input) : h("span", { class: "wk-empty", text: "None published" }))),
      h("div", {}, h("dt", { text: "Result (bounded)" }), h("dd", {}, detailEntry.output ? code(detailEntry.output) : h("span", { class: "wk-empty", text: entry.status === "running" ? "Still running" : "None published" }))),
    );
    // Failed tools open by default in full session; never re-open something the reader closed.
    if (options.failedOpen && entry.status === "failed" && row.dataset.userToggled !== "true" && row.getAttribute("aria-expanded") !== "true") {
      setAttr(row, "aria-expanded", "true");
      detail.hidden = false;
    }
  }
}
