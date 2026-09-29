import { afterEach, describe, expect, test, vi } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import {
  DARK_TOKENS,
  LIGHT_TOKENS,
  PROTOCOL_VERSION,
  themeTokens,
  type FrameContext,
  type HostMessage,
  type PluginMetadata,
} from "@docket/plugin-sdk";
import {
  BRIDGE_LIMITS,
  clampHeight,
  clearFrameState,
  FrameBridge,
  frameState,
  servicePath,
  type BridgeEffects,
} from "../src/plugin-host/bridge";

const plugin = {
  name: "example",
  service_base: "/plugins/example",
  ui_base: "/plugin-ui/example/abc",
  capabilities: ["task.read", "service.fetch"],
} as unknown as PluginMetadata;

const context = (overrides: Partial<FrameContext> = {}): FrameContext => ({
  plugin: "example",
  workspace: "one",
  view: { kind: "widget", id: "example/job" },
  taskId: "TASK-1",
  capabilities: ["task.read", "service.fetch"],
  preferences: { theme: "light", density: "comfortable", reducedMotion: false },
  theme: { scheme: "light", tokens: themeTokens("light", "comfortable") },
  state: null,
  ...overrides,
});

function harness(ctx = context(), effects: Partial<BridgeEffects> = {}) {
  const sent: HostMessage[] = [];
  const frame = { postMessage: (message: HostMessage) => sent.push(message) } as unknown as Window;
  const listeners = new Set<(event: MessageEvent) => void>();
  const host = {
    addEventListener: (_: string, listener: (event: MessageEvent) => void) => listeners.add(listener),
    removeEventListener: (_: string, listener: (event: MessageEvent) => void) => listeners.delete(listener),
  } as unknown as Window;
  const all: BridgeEffects = {
    fetch: vi.fn(async () => new Response('{"ok":true}', { status: 200, headers: { "content-type": "application/json", "set-cookie": "x=1" } })),
    eventSource: vi.fn(),
    readTask: vi.fn(async (_workspace: string, id: string) => ({ id })),
    comment: vi.fn(async () => ({})),
    move: vi.fn(async () => ({})),
    navigate: vi.fn(),
    openExternal: vi.fn(() => true),
    ...effects,
  };
  const resize = vi.fn();
  const bridge = new FrameBridge(() => frame, plugin, ctx, "key", all, resize, host);
  const send = (data: unknown, source: unknown = frame) =>
    listeners.forEach((listener) => listener({ source, data } as MessageEvent));
  const request = async (method: string, params: unknown = {}, id = "r1") => {
    send({ docket: PROTOCOL_VERSION, type: "request", id, method, params });
    await new Promise((done) => setTimeout(done, 0));
    return [...sent].reverse().find((m) => m.type === "response" && m.id === id);
  };
  return { bridge, sent, send, request, effects: all, resize, listeners };
}

afterEach(() => clearFrameState());

describe("frame bridge", () => {
  test("ignores messages from anything but its own frame", async () => {
    const h = harness();
    h.send({ docket: PROTOCOL_VERSION, type: "ready" }, {});
    h.send({ docket: PROTOCOL_VERSION, type: "ready" }, window);
    expect(h.sent).toHaveLength(0);
    h.send({ docket: PROTOCOL_VERSION, type: "ready" });
    expect(h.sent[0]).toMatchObject({ type: "init", context: { plugin: "example", taskId: "TASK-1" } });
  });

  test("updates are held until the frame is ready", () => {
    const h = harness();
    h.bridge.update(context({ taskId: "TASK-2" }));
    expect(h.sent).toHaveLength(0);
    h.send({ docket: PROTOCOL_VERSION, type: "ready" });
    expect(h.sent[0]).toMatchObject({ type: "init", context: { taskId: "TASK-2" } });
    h.bridge.update(context({ taskId: "TASK-3" }));
    expect(h.sent[1]).toMatchObject({ type: "update", context: { taskId: "TASK-3" } });
  });

  test("undeclared capabilities are denied without running effects", async () => {
    const h = harness();
    expect(await h.request("task.comment", { text: "hi" })).toMatchObject({ ok: false, error: "capability_denied" });
    expect(await h.request("open.external", { url: "https://example.com" }, "r2")).toMatchObject({ ok: false, error: "capability_denied" });
    expect(await h.request("nope", {}, "r3")).toMatchObject({ ok: false, error: "unknown_method" });
    expect(h.effects.comment).not.toHaveBeenCalled();
    expect(h.effects.openExternal).not.toHaveBeenCalled();
  });

  test("task operations default to the view's task", async () => {
    const h = harness();
    expect(await h.request("task.read")).toMatchObject({ ok: true, value: { id: "TASK-1" } });
    expect(h.effects.readTask).toHaveBeenCalledWith("one", "TASK-1");
  });

  test("service fetch stays under the plugin service and drops credentials and unsafe headers", async () => {
    const h = harness();
    const response = await h.request("service.fetch", { path: "/api/x?y=1", headers: { accept: "application/json", cookie: "a=b", authorization: "x" } });
    expect(response).toMatchObject({ ok: true, value: { status: 200, body: '{"ok":true}', headers: { "content-type": "application/json" } } });
    expect((response as { value: { headers: object } }).value.headers).not.toHaveProperty("set-cookie");
    const [url, init] = (h.effects.fetch as ReturnType<typeof vi.fn>).mock.calls[0];
    expect(url).toBe("/plugins/example/api/x?y=1");
    expect(init.credentials).toBe("omit");
    expect(init.redirect).toBe("error");
    expect([...(init.headers as Headers).keys()]).toEqual(["accept"]);
    expect(await h.request("service.fetch", { path: "/../../api/workspaces" }, "r2")).toMatchObject({ ok: false, error: "invalid_request" });
    expect(await h.request("service.fetch", { path: "/x", method: "GET", body: "no" }, "r3")).toMatchObject({ ok: false, error: "invalid_request" });
  });

  test("navigation stays inside the workspace; external links need http(s)", async () => {
    const h = harness(context({ capabilities: ["open.external"] }));
    expect(await h.request("navigate", { path: "/workspaces/one/tasks/T-1" })).toMatchObject({ ok: true });
    expect(await h.request("navigate", { path: "/workspaces/two" }, "r2")).toMatchObject({ ok: false, error: "invalid_request" });
    expect(await h.request("navigate", { path: "/workspaces/one/../two" }, "r3")).toMatchObject({ ok: false });
    expect(await h.request("open.external", { url: "javascript:alert(1)" }, "r4")).toMatchObject({ ok: false });
    expect(await h.request("open.external", { url: "https://u:p@example.com" }, "r5")).toMatchObject({ ok: false });
    expect(await h.request("open.external", { url: "https://example.com/x" }, "r6")).toMatchObject({ ok: true, value: { opened: true } });
    expect(h.effects.navigate).toHaveBeenCalledTimes(1);
  });

  test("state survives a reset (hot reload) and is bounded", async () => {
    const h = harness();
    expect(await h.request("state.set", { value: { tab: 2 } })).toMatchObject({ ok: true });
    expect(frameState("key")).toEqual({ tab: 2 });
    h.bridge.reset();
    h.send({ docket: PROTOCOL_VERSION, type: "ready" });
    expect([...h.sent].reverse().find((m) => m.type === "init")).toMatchObject({ context: { state: { tab: 2 } } });
    const big = "x".repeat(BRIDGE_LIMITS.stateBytes);
    expect(await h.request("state.set", { value: big }, "r2")).toMatchObject({ ok: false, error: "invalid_request" });
  });

  test("resize is forwarded; destroy detaches", () => {
    const h = harness();
    h.send({ docket: PROTOCOL_VERSION, type: "resize", height: 420 });
    expect(h.resize).toHaveBeenCalledWith(420);
    h.bridge.destroy();
    expect(h.listeners.size).toBe(0);
  });

  test("in-flight requests are limited", async () => {
    const h = harness(context(), { readTask: () => new Promise(() => undefined) });
    for (let i = 0; i < BRIDGE_LIMITS.inFlight; i++)
      h.send({ docket: PROTOCOL_VERSION, type: "request", id: `p${i}`, method: "task.read", params: {} });
    expect(await h.request("task.read", {}, "over")).toMatchObject({ ok: false, error: "too_many_requests" });
  });
});

describe("helpers", () => {
  test("servicePath rejects traversal and encoded separators", () => {
    expect(servicePath("/plugins/p", "/a/b?c=../d")).toBe("/plugins/p/a/b?c=../d");
    for (const bad of ["a", "//evil", "/a/../b", "/a/./b", "/%2e%2e/x", "/a%2fb", "/a\\b", "/a b", "/a\nb"])
      expect(() => servicePath("/plugins/p", bad)).toThrow();
    expect(() => servicePath(undefined, "/a")).toThrow();
  });

  test("clampHeight bounds frame height", () => {
    expect(clampHeight(5, 640)).toBe(BRIDGE_LIMITS.minHeight);
    expect(clampHeight(10_000, 640)).toBe(640);
    expect(clampHeight(NaN, 640)).toBe(BRIDGE_LIMITS.minHeight);
  });

  test("theme tokens mirror tokens.css", () => {
    const css = readFileSync(resolve(import.meta.dirname, "../../packages/plugin-sdk/src/tokens.css"), "utf8");
    const block = (selector: string) =>
      Object.fromEntries(
        [...css.slice(css.indexOf(selector + " {")).split("}")[0].matchAll(/(--docket-widget-[\w-]+):([^;]+);/g)].map((m) => [m[1], m[2].trim()]),
      );
    expect(LIGHT_TOKENS).toEqual(block(".docket-widget"));
    expect(DARK_TOKENS).toEqual(block(".docket-widget[data-theme=dark]"));
    expect(themeTokens("dark", "compact")["--docket-widget-pad"]).toBe("8px");
  });
});
