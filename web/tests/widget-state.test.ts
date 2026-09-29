import { describe, expect, test, vi } from "vitest";
import { WidgetRouter } from "../src/registry/widget-state";
import { DetailController } from "../src/registry/detail-controller";
import {
  jsonBytes,
  WIDGET_BUDGETS,
  type DetailFrame,
  type DetailProvider,
  type LivePayload,
} from "@docket/plugin-ui";
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
const identity = {
  workspace: "one",
  taskId: "TASK-1",
  widgetType: "fixture/card",
  instanceId: "instance",
};
const detailFrame = (overrides: Partial<DetailFrame> = {}): DetailFrame => ({
  version: 1,
  identity,
  dataVersion: 1,
  revision: 1,
  baseSeq: 0,
  throughSeq: 1,
  reset: true,
  value: "first",
  ...overrides,
});
describe("view-owned detail leases", () => {
  test("received sequence advances while display is held; gaps require one reset; late callbacks are inert", () => {
    vi.useFakeTimers();
    const controller = new DetailController();
    let sink!: Parameters<DetailProvider["open"]>[1];
    const close = vi.fn(),
      reset = vi.fn();
    const provider: DetailProvider = {
      open(_ctx, s) {
        sink = s;
        return { close, requestReset: reset };
      },
    };
    const received: DetailFrame[] = [];
    const revoked = vi.fn();
    const abort = new AbortController();
    const lease = controller.request(
      identity,
      "/plugins/fixture",
      provider,
      [1],
      abort.signal,
      (f) => received.push(f),
      revoked,
    )!;
    expect(controller.activeCount).toBe(1);
    expect(reset).toHaveBeenCalledTimes(1);
    sink.frame(detailFrame());
    sink.frame(
      detailFrame({ revision: 2, baseSeq: 1, throughSeq: 2, reset: false }),
    );
    sink.frame(
      detailFrame({ revision: 3, baseSeq: 2, throughSeq: 3, reset: false }),
    );
    expect(received).toHaveLength(3);
    expect(reset).toHaveBeenCalledTimes(1);
    sink.frame(
      detailFrame({ revision: 5, baseSeq: 4, throughSeq: 5, reset: false }),
    );
    sink.frame(
      detailFrame({ revision: 6, baseSeq: 5, throughSeq: 6, reset: false }),
    );
    expect(received).toHaveLength(3);
    expect(reset).toHaveBeenCalledTimes(2);
    sink.frame(detailFrame({ revision: 6, throughSeq: 6, reset: true }));
    expect(received).toHaveLength(4);
    sink.frame(detailFrame({ revision: 7, throughSeq: 2, reset: true }));
    expect(received).toHaveLength(4);
    lease.release();
    lease.release();
    sink.frame(detailFrame({ revision: 8, throughSeq: 8 }));
    expect(received).toHaveLength(4);
    expect(close).toHaveBeenCalledTimes(1);
    expect(controller.activeCount).toBe(0);
    expect(revoked).toHaveBeenCalledWith("released");
    controller.destroy();
    vi.useRealTimers();
  });
  test("reselection, not-found, throwing cleanup, abort and reset timeout release transport", () => {
    vi.useFakeTimers();
    const c = new DetailController();
    const sinks: Parameters<DetailProvider["open"]>[1][] = [];
    const close = vi.fn(() => {
      throw Error("cleanup");
    });
    const provider: DetailProvider = {
      open(_ctx, s) {
        sinks.push(s);
        return { close, requestReset() {} };
      },
    };
    const first = vi.fn(),
      second = vi.fn(),
      abort = new AbortController();
    c.request(
      identity,
      "/plugins/fixture",
      provider,
      [1],
      abort.signal,
      () => {},
      first,
    );
    c.request(
      { ...identity, instanceId: "two" },
      "/plugins/fixture",
      provider,
      [1],
      abort.signal,
      () => {},
      second,
    );
    expect(first).toHaveBeenCalledWith("reselected");
    expect(c.activeCount).toBe(1);
    sinks[0].status("unavailable");
    expect(c.activeCount).toBe(1);
    sinks[1].status("not_found");
    expect(second).toHaveBeenCalledWith("not_found");
    expect(c.activeCount).toBe(0);
    c.request(
      identity,
      "/plugins/fixture",
      provider,
      [1],
      abort.signal,
      () => {},
      second,
    );
    vi.advanceTimersByTime(5001);
    expect(second).toHaveBeenCalledWith("gap_timeout");
    expect(c.activeCount).toBe(0);
    c.request(
      identity,
      "/plugins/fixture",
      provider,
      [1],
      abort.signal,
      () => {},
      second,
    );
    abort.abort();
    expect(c.activeCount).toBe(0);
    c.destroy();
    vi.useRealTimers();
  });
  test("synchronous callbacks and provider failures cannot leak a lease", () => {
    const c = new DetailController(),
      close = vi.fn();
    const lease = c.request(
      identity,
      undefined,
      {
        open(_ctx, s) {
          s.status("unavailable");
          return { close, requestReset() {} };
        },
      },
      [1],
      new AbortController().signal,
      () => {},
      () => {},
    );
    expect(lease).toBeNull();
    expect(close).toHaveBeenCalledTimes(1);
    expect(c.activeCount).toBe(0);
  });
});
