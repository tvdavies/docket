import { render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";
import { PluginCardHost } from "../src/registry/PluginCardHost";
import { demoPluginUI } from "./fixtures-legacy-plugin";
import {
  cardModules,
  registerPluginUI,
  resolveReference,
  ReferenceRegistry,
  widgetModule,
} from "../src/registry/registry";
import {
  serviceBase,
  type BoardTask,
  type PluginMetadata,
} from "@docket/plugin-ui";
const task = (labels: string[] = []): BoardTask => ({
  id: "TASK-1",
  title: "Demo",
  status: "todo",
  labels,
  references: [],
  active_sessions: [{ session: "audit-only" }],
  created_at: "2026-01-01",
  updated_at: "2026-01-01",
  resource_count: 0,
});
const metadata = (name: string, cards: string[]): PluginMetadata => ({
  name,
  version: "1.0.0",
  cards: cards.map((type) => ({ type, title: type })),
  reference_resolvers: [],
});
describe("workspace-scoped plugin UI", () => {
  test("legacy resolver only runs for the server-selected enabled declaration", async () => {
    registerPluginUI(demoPluginUI);
    const plugin = {
      ...metadata("demo", []),
      reference_resolvers: [{ id: "demo/plan", pattern: "server only" }],
    };
    const registry = new ReferenceRegistry("one", "generation", [plugin]);
    const ref = {
      id: "ref-1",
      kind: "plan",
      url: "https://plans.myslop.app/p/abc",
      added_at: "",
      resolver_id: "demo/plan",
      resolver_generation: "generation",
    };
    expect((await resolveReference(ref, registry)).label).toBe(
      "Implementation plan",
    );
    expect(
      (await resolveReference({ ...ref, title: "Changed" }, registry)).label,
    ).toBe("Changed");
    expect(
      (await resolveReference({ ...ref, resolver_generation: "old" }, registry))
        .label,
    ).toBe(ref.url);
    expect(
      (
        await resolveReference(
          ref,
          new ReferenceRegistry("two", "generation", []),
        )
      ).label,
    ).toBe(ref.url);
    registry.destroy();
  });
  test("v1 receives exact old context and task references; disable and update errors clean up", () => {
    const update = vi.fn(),
      destroy = vi.fn(),
      mount = vi.fn((el: HTMLElement, ctx: any) => {
        expect(Object.keys(ctx).sort()).toEqual([
          "pluginBase",
          "refresh",
          "task",
          "workspace",
        ]);
        expect(ctx.pluginBase).toBe("/plugins/lifecycle/base");
        el.textContent = "plugin mounted";
        return { update, destroy };
      });
    registerPluginUI({
      cards: [
        {
          type: "lifecycle/card",
          appliesTo: (t) => t.labels.includes("show"),
          mount,
        },
      ],
    });
    const config = [
      {
        ...metadata("lifecycle", ["lifecycle/card"]),
        service_base: "/plugins/lifecycle/base",
      },
    ];
    const original = task(["show"]);
    const view = render(
      <PluginCardHost workspace="one" task={original} config={config} />,
    );
    expect(screen.getByText("plugin mounted")).toBeInTheDocument();
    expect(mount.mock.calls[0][1].task).toBe(original);
    expect(update).toHaveBeenLastCalledWith(original);
    const newer = { ...original, status: "done" };
    view.rerender(
      <PluginCardHost workspace="one" task={newer} config={config} />,
    );
    expect(update).toHaveBeenLastCalledWith(newer);
    expect(mount).toHaveBeenCalledTimes(1);
    update.mockImplementation(() => {
      throw Error("private error");
    });
    view.rerender(
      <PluginCardHost
        workspace="one"
        task={{ ...newer, title: "Changed" }}
        config={config}
      />,
    );
    expect(destroy).toHaveBeenCalledTimes(1);
    expect(screen.getByText("Plugin card unavailable")).toBeInTheDocument();
    view.rerender(<PluginCardHost workspace="one" task={newer} config={[]} />);
    view.unmount();
    expect(destroy).toHaveBeenCalledTimes(1);
  });
  test("enablement and declaration order precede appliesTo; absent service never invented", () => {
    const applies = vi.fn(() => true);
    registerPluginUI({
      cards: ["b", "a"].map((id) => ({
        type: `order/${id}`,
        appliesTo: applies,
        mount: () => ({ update() {}, destroy() {} }),
      })),
    });
    expect(cardModules(task(), [])).toEqual([]);
    expect(applies).not.toHaveBeenCalled();
    const selected = cardModules(task(), [
      metadata("order", ["order/a", "order/b"]),
    ]);
    expect(selected.map((c) => c.module.type)).toEqual(["order/a", "order/b"]);
    expect(selected[0].base).toBe("");
    expect(serviceBase("order", "//evil.test")).toBeUndefined();
    expect(serviceBase("order", "/plugins/other")).toBeUndefined();
    expect(serviceBase("order", "/plugins/order/../other")).toBeUndefined();
  });
  test("v2 requires explicit matching API and placement; duplicate registration fails locally", () => {
    const plugin = {
      apiVersion: 2 as const,
      name: "v2test",
      widgets: [
        {
          type: "v2test/card",
          dataVersions: [1],
          present: () => null,
          mount: () => ({ update() {}, destroy() {} }),
        },
      ],
    };
    registerPluginUI(plugin);
    registerPluginUI(plugin);
    expect(() => registerPluginUI({ ...plugin })).toThrow(
      "duplicate_definition",
    );
    const config = [
      {
        ...metadata("v2test", []),
        api_version: 2,
        cards: [
          {
            type: "v2test/card",
            title: "Card",
            locations: ["activity" as const],
          },
        ],
      },
    ];
    expect(widgetModule(config, "v2test/card", "board")).toBeUndefined();
    expect(widgetModule(config, "v2test/card", "activity")?.module).toBe(
      plugin.widgets[0],
    );
    expect(
      widgetModule(
        [{ ...config[0], api_version: 9 }],
        "v2test/card",
        "activity",
      )?.module,
    ).toBeUndefined();
  });
  test("late resolver result after destroy is ignored, and failures do not try another resolver", async () => {
    let complete: (v: any) => void = () => {};
    const second = vi.fn();
    registerPluginUI({
      apiVersion: 2,
      name: "late",
      referenceResolvers: [
        {
          id: "late/first",
          resolve: () =>
            new Promise<import("@docket/plugin-ui").ResolvedReference>(
              (resolve) => {
                complete = resolve;
              },
            ),
        },
        { id: "late/second", resolve: second },
      ],
    });
    const config = [
      {
        ...metadata("late", []),
        api_version: 2,
        reference_resolvers: [
          { id: "late/first", pattern: ".*" },
          { id: "late/second", pattern: ".*" },
        ],
      },
    ];
    const registry = new ReferenceRegistry("one", "g", config),
      ref = {
        id: "r",
        url: "https://example.test",
        title: "Original",
        kind: "plan",
        added_at: "",
        resolver_id: "late/first",
        resolver_generation: "g",
      };
    const pending = registry.resolve(ref, "TASK-1");
    await Promise.resolve();
    registry.destroy();
    complete({ label: "Stale private result" });
    expect((await pending).label).toBe("Original");
    expect(second).not.toHaveBeenCalled();
  });
});
