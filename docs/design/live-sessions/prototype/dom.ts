// Tiny DOM helpers for the fixture prototype. No framework, no innerHTML.

type Child = Node | string | null | undefined | false;

export function h<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  attrs: Record<string, string | boolean | undefined | null> = {},
  ...children: Child[]
): HTMLElementTagNameMap[K] {
  const el = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs)) {
    if (value === undefined || value === null || value === false) continue;
    if (key === "class") el.className = String(value);
    else if (key === "text") el.textContent = String(value);
    else el.setAttribute(key, value === true ? "" : String(value));
  }
  append(el, children);
  return el;
}

export function append(parent: Node, children: Child[]): void {
  for (const child of children) {
    if (child === null || child === undefined || child === false) continue;
    parent.appendChild(typeof child === "string" ? document.createTextNode(child) : child);
  }
}

export function setText(el: Element, text: string): void {
  if (el.textContent !== text) el.textContent = text;
}

export function setAttr(el: Element, name: string, value: string | null | undefined): void {
  if (value === null || value === undefined) { if (el.hasAttribute(name)) el.removeAttribute(name); return; }
  if (el.getAttribute(name) !== value) el.setAttribute(name, value);
}

/**
 * Keyed reconciliation: keeps existing element identity for unchanged keys,
 * creates new elements for new keys, removes stale ones, and reorders in
 * place. Focus and selection inside retained elements survive updates.
 */
export function reconcile<T>(
  container: Element,
  items: T[],
  key: (item: T) => string,
  create: (item: T) => Element,
  update: (el: Element, item: T) => void,
): void {
  const existing = new Map<string, Element>();
  for (const child of Array.from(container.children)) {
    const k = child.getAttribute("data-key");
    if (k) existing.set(k, child);
  }
  const seen = new Set<string>();
  let cursor: Element | null = container.firstElementChild;
  for (const item of items) {
    const k = key(item);
    seen.add(k);
    let el = existing.get(k);
    if (!el) { el = create(item); el.setAttribute("data-key", k); }
    update(el, item);
    if (cursor !== el) container.insertBefore(el, cursor);
    else cursor = el.nextElementSibling;
  }
  for (const [k, el] of existing) if (!seen.has(k)) el.remove();
}

export function containsFocus(root: Node): boolean {
  let active: Element | null = document.activeElement;
  while (active) {
    if (root.contains(active)) return true;
    const shadowRoot = active.shadowRoot;
    active = shadowRoot ? shadowRoot.activeElement : null;
  }
  return false;
}

/**
 * True when `node` is inside `root`, crossing shadow boundaries: a node inside
 * a shadow tree counts as inside `root` when its shadow host does. Plain
 * `Node.contains` stops at the shadow boundary, which is why a text selection
 * inside a custom-element body was previously invisible to the wrapper.
 */
export function containsComposed(root: Node, node: Node | null): boolean {
  let current: Node | null = node;
  while (current) {
    if (root.contains(current)) return true;
    const rootNode = current.getRootNode();
    current = rootNode instanceof ShadowRoot ? rootNode.host : null;
  }
  return false;
}

export function containsSelection(root: Node): boolean {
  const selection = document.getSelection();
  if (!selection || selection.isCollapsed || selection.rangeCount === 0) return false;
  return containsComposed(root, selection.anchorNode) || containsComposed(root, selection.focusNode);
}

export function formatDuration(ms: number | undefined): string | null {
  if (ms === undefined || !Number.isFinite(ms) || ms < 0) return null;
  const seconds = Math.round(ms / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  return `${minutes}m ${String(seconds % 60).padStart(2, "0")}s`;
}

export function formatClock(iso: string | undefined): string | null {
  if (!iso) return null;
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return null;
  return `${String(date.getUTCHours()).padStart(2, "0")}:${String(date.getUTCMinutes()).padStart(2, "0")}`;
}

export function shortId(id: string): string {
  return id.length > 12 ? `${id.slice(0, 8)}…` : id;
}
