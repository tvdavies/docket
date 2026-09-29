import { describe, expect, test, vi } from "vitest";
import { WidgetRouter } from "../src/registry/widget-state";
import {
  jsonBytes,
  WIDGET_BUDGETS,
  type LivePayload,
} from "@docket/plugin-sdk";
const frame = (
  revision = 2,
  value: unknown = "text",
  version = 1,
): LivePayload => ({
  kind: "fixture/card",
  task: "TASK-1",
  session: "instance",
  ttl_ms: 30,
  payload: { widget_version: 1, revision, data: { version, value } },
});
describe("accepted preview state", () => {
  test("duplicates and older frames cannot regress data or renew freshness, even after TTL/reset", () => {
    vi.useFakeTimers();
    let now = 0;
    const router = new WidgetRouter(
      "one",
      () => {},
      () => now,
    );
    router.setConnection("open");
    expect(router.accept(frame())).toBe(true);
    const key = () => router.get("fixture/card", "TASK-1", "instance")!;
    now = 5;
    expect(router.accept(frame(1, "old"))).toBe(false);
    expect(router.accept(frame(2, "changed"))).toBe(false);
    expect(key().freshness.receivedAt).toBe(0);
    now = 10;
    expect(router.accept(frame())).toBe(true);
    expect(key().freshness.receivedAt).toBe(10);
    vi.advanceTimersByTime(31);
    expect(key().freshness.stale).toBe(true);
    expect(key().data.value).toBe("text");
    router.reset();
    expect(key().freshness.rehydrating).toBe(true);
    expect(router.accept(frame(1))).toBe(false);
    expect(router.accept(frame(3, "unsupported", 99))).toBe(true);
    expect(key().data.version).toBe(99);
    router.destroy();
    expect(router.get("fixture/card", "TASK-1", "instance")).toBeUndefined();
    vi.useRealTimers();
  });
  test("workspace cache is isolated and ingress budgets fail closed", () => {
    const one = new WidgetRouter("one"),
      two = new WidgetRouter("two");
    one.accept(frame());
    expect(two.get("fixture/card", "TASK-1", "instance")).toBeUndefined();
    expect(one.accept(frame(3, "x".repeat(WIDGET_BUDGETS.previewBytes)))).toBe(
      false,
    );
    expect(one.accept({ ...frame(), ttl_ms: NaN })).toBe(false);
    expect(one.accept({ ...frame(), payload: { widget_version: 9 } })).toBe(
      false,
    );
    expect(jsonBytes({ value: "é" })).toBeGreaterThan(
      JSON.stringify({ value: "é" }).length,
    );
    one.destroy();
    two.destroy();
  });
});
