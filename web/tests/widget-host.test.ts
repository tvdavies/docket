import { afterEach, describe, expect, test, vi } from "vitest";
import { WidgetHost } from "../src/registry/widget-host";
import { DetailController } from "../src/registry/detail-controller";
import {
  customElementWidget,
  defineWidgetElement,
  standardWidgetBody,
  widgetElementName,
  type WidgetContext,
  type WidgetModule,
  type WidgetSnapshot,
} from "@docket/plugin-ui";
const identity = {
  workspace: "one",
  taskId: "TASK-1",
  widgetType: "host/card",
  instanceId: "instance",
};
const preferences = {
  theme: "light" as const,
  density: "comfortable" as const,
  reducedMotion: false,
};
const input = {
  apiVersion: 2 as const,
  identity,
  location: "activity" as const,
  preferences,
  refresh: () => {},
};
const snapshot = (revision = 1, value = "First"): WidgetSnapshot => ({
  task: {
    id: "TASK-1",
    title: "Task",
    status: "todo",
    labels: [],
    references: [],
    active_sessions: [],
    resource_count: 0,
    created_at: "",
    updated_at: "",
  },
  data: { version: 1, revision, value },
  freshness: { connection: "open", stale: false, rehydrating: false },
  availability: "available",
  fallback: {
    version: 1,
    widget_type: identity.widgetType,
    instance_id: identity.instanceId,
    task_id: "TASK-1",
    created_at: "2026-09-10T00:00:00Z",
    revision: 1,
    phase: "created",
    fallback: {
      label: "Saved label",
      status_label: "Running",
      priority: "active",
      summary: "Saved summary",
    },
  },
});
const present: WidgetModule["present"] = (s) => ({
  label: "Progress",
  status: { text: "Running", tone: "info" },
  priority: "active",
  terminal: s.fallback?.phase === "finalised",
  summary: s.fallback?.fallback.summary,
  rows: [
    {
      key: "row",
      order: 1,
      role: "text",
      label: "Step",
      text: String(s.data?.value || ""),
    },
  ],
});
afterEach(() => {
  document.body.replaceChildren();
  vi.useRealTimers();
});
const root = () => {
  const node = document.createElement("div");
  document.body.append(node);
  return node;
};
describe("production widget host and adapters", () => {
  test("mount gets identity before first snapshot, preserves body on preferences, abort precedes destroy", () => {
    const events: string[] = [];
    let ctx!: WidgetContext;
    const update = vi.fn();
    const node = root();
    const module: WidgetModule = {
      type: "host/card",
      dataVersions: [1],
      present,
      mount(_el, c) {
        ctx = c;
        events.push("mount");
        c.signal.addEventListener("abort", () => events.push("abort"));
        return {
          update,
          destroy() {
            events.push("destroy");
            throw Error("cleanup");
          },
        };
      },
    };
    const host = new WidgetHost(node, input, module);
    host.update(snapshot());
    expect(ctx.identity).toEqual(identity);
    expect(update).toHaveBeenCalledTimes(1);
    host.update(snapshot(), {
      ...input,
      preferences: { ...preferences, theme: "dark" },
    });
    expect(events).toEqual(["mount"]);
    expect(node.dataset.theme).toBe("dark");
    host.destroy();
    host.destroy();
    expect(events).toEqual(["mount", "abort", "destroy"]);
    expect(node.children.length).toBe(0);
  });
  test("update failure removes body and focuses stable wrapper, with no automatic remount", () => {
    vi.useFakeTimers();
    const mount = vi.fn((el: HTMLElement) => {
      const button = document.createElement("button");
      button.textContent = "Focus body";
      el.append(button);
      return {
        update() {
          button.focus();
          throw Error("private payload");
        },
        destroy() {
          throw Error("destroy");
        },
      };
    });
    const node = root(),
      host = new WidgetHost(node, input, {
        type: "host/card",
        dataVersions: [1],
        present,
        mount,
      });
    host.update(snapshot());
    expect(document.activeElement).toBe(node);
    expect(node.textContent).toContain("Widget unavailable");
    expect(node.textContent).not.toContain("private payload");
    host.update(snapshot(2));
    vi.advanceTimersByTime(1001);
    expect(mount).toHaveBeenCalledTimes(1);
    host.destroy();
  });
  test("partial mount aborts resources even when no instance was returned", () => {
    const aborted = vi.fn();
    const host = new WidgetHost(root(), input, {
      type: "host/card",
      dataVersions: [1],
      present,
      mount(_el, ctx) {
        ctx.signal.addEventListener("abort", aborted);
        throw Error("mount");
      },
    });
    host.update(snapshot());
    expect(aborted).toHaveBeenCalledTimes(1);
    host.destroy();
    expect(aborted).toHaveBeenCalledTimes(1);
  });
  test("unsupported data retires the body; newer supported data remounts once", () => {
    const destroy = vi.fn(),
      mount = vi.fn(() => ({ update() {}, destroy }));
    const host = new WidgetHost(root(), input, {
      type: "host/card",
      dataVersions: [1],
      present,
      mount,
    });
    host.update(snapshot());
    host.update({
      ...snapshot(2),
      data: { version: 99, revision: 2, value: "unknown" },
    });
    expect(destroy).toHaveBeenCalledTimes(1);
    host.update(snapshot(3));
    expect(mount).toHaveBeenCalledTimes(2);
    host.destroy();
  });
  test("focus in shadow DOM holds data, while current metadata and terminal snapshot update", () => {
    vi.useFakeTimers();
    const node = root();
    const host = new WidgetHost(node, input, {
      type: "host/card",
      dataVersions: [1],
      present,
      mount: standardWidgetBody,
    });
    host.update(snapshot());
    const shadow = node.querySelector(".widget-body")!.shadowRoot!;
    const row = shadow.querySelector("li")!;
    row.tabIndex = 0;
    row.focus();
    host.update(snapshot(2, "New content"));
    vi.advanceTimersByTime(1001);
    expect(row.textContent).toContain("First");
    expect(node.textContent).toContain("New activity");
    const terminal = snapshot(3, "Final");
    terminal.fallback = { ...terminal.fallback!, phase: "finalised" };
    terminal.data = undefined;
    host.update(terminal);
    vi.advanceTimersByTime(20);
    expect(row.textContent).toContain("First");
    expect(document.activeElement).toBe(shadow.host);
    const button = [...node.querySelectorAll("button")].find((b) =>
      b.textContent?.includes("show summary"),
    )!;
    button.click();
    expect(shadow.textContent).toContain("Saved summary");
    host.destroy();
  });
  test("compact previews never overwrite a selected detail projection", () => {
    vi.useFakeTimers();
    const node = root(),
      controller = new DetailController();
    let sink: any;
    const host = new WidgetHost(
      node,
      { ...input, serviceBase: "/plugins/host" },
      {
        type: "host/card",
        dataVersions: [1],
        present,
        mount: standardWidgetBody,
        detail: {
          open(_ctx, s) {
            sink = s;
            return { close() {}, requestReset() {} };
          },
        },
      },
      controller,
    );
    host.update(snapshot());
    [...node.querySelectorAll("button")]
      .find((b) => b.textContent === "Expand activity")!
      .click();
    sink.frame({
      version: 1,
      identity,
      dataVersion: 1,
      revision: 10,
      baseSeq: 0,
      throughSeq: 10,
      reset: true,
      value: "Detailed reading projection",
    });
    vi.advanceTimersByTime(20);
    host.update(snapshot(2, "New compact preview"));
    vi.advanceTimersByTime(1001);
    expect(
      node.querySelector(".widget-body")!.shadowRoot!.textContent,
    ).toContain("Detailed reading projection");
    expect(
      node.querySelector(".widget-body")!.shadowRoot!.textContent,
    ).not.toContain("New compact preview");
    host.destroy();
    expect(controller.activeCount).toBe(0);
    controller.destroy();
  });
  test("actual custom element registration is injective and conflicting definitions fail", () => {
    const hooks = {
      mount() {
        return { update() {}, destroy() {} };
      },
    };
    const name = defineWidgetElement("custom/chart", "build-1", hooks);
    expect(defineWidgetElement("custom/chart", "build-1", hooks)).toBe(name);
    expect(() => defineWidgetElement("custom/chart", "build-2", hooks)).toThrow(
      "duplicate_definition",
    );
    expect(widgetElementName("a/b-c")).not.toBe(widgetElementName("a-b/c"));
    const module = customElementWidget({
      type: "custom/real",
      buildIdentity: "1",
      hooks,
      dataVersions: [1],
      present,
    });
    const node = root(),
      host = new WidgetHost(
        node,
        { ...input, identity: { ...identity, widgetType: "custom/real" } },
        module,
      );
    host.update(snapshot());
    expect(
      node.querySelector(widgetElementName("custom/real"))?.shadowRoot,
    ).toBeTruthy();
    host.destroy();
    expect(node.children.length).toBe(0);
  });
});
