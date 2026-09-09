import { JSDOM } from 'jsdom';

const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://localhost', pretendToBeVisual: true });
for (const key of Object.getOwnPropertyNames(dom.window)) {
  if (!(key in globalThis)) Object.defineProperty(globalThis, key, { configurable: true, get: () => (dom.window as any)[key] });
}
Object.defineProperty(globalThis, 'window', { value: dom.window, configurable: true });
Object.defineProperty(globalThis, 'document', { value: dom.window.document, configurable: true });
for (const key of ['Event', 'CustomEvent', 'EventTarget', 'MouseEvent', 'KeyboardEvent', 'FocusEvent', 'AbortController', 'AbortSignal']) {
  Object.defineProperty(globalThis, key, { configurable: true, value: (dom.window as any)[key] });
}
Object.defineProperty(globalThis, 'matchMedia', { configurable: true, value: (query: string) => window.matchMedia(query) });
