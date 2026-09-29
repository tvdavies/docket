import { expect, test } from "vitest";
import {
  validWidgetRecord,
  validWidgetPayload,
  boundedPresentation,
  safeHref,
  WIDGET_BUDGETS,
} from "@docket/plugin-ui";
import fixture from "../../packages/plugin-ui/fixtures/wire.json";
test("public wire fixtures agree with the Go validators", () => {
  expect(validWidgetRecord(fixture.record, fixture.workspace)).toBe(true);
  expect(validWidgetPayload(fixture.preview)).toBe(true);
  expect(
    validWidgetRecord({ ...fixture.record, version: 99 }, fixture.workspace),
  ).toBe(false);
  expect(
    validWidgetPayload({
      ...fixture.preview,
      revision: Number.MAX_SAFE_INTEGER + 1,
    }),
  ).toBe(false);
});
test("payload and presentation budgets are independent and safe URLs fail closed", () => {
  expect(
    validWidgetPayload({
      ...fixture.preview,
      data: { version: 1, value: "é".repeat(WIDGET_BUDGETS.previewBytes / 2) },
    }),
  ).toBe(false);
  const value = boundedPresentation({
    label: "Label",
    status: { text: "Working", tone: "info" },
    terminal: false,
    priority: "active",
    action: "a".repeat(200),
    rows: Array.from({ length: 30 }, (_, order) => ({
      key: String(order),
      order,
      role: "text",
      label: "Row",
      text: "t".repeat(1000),
    })),
  });
  expect(value.action).toHaveLength(120);
  expect(value.rows).toHaveLength(4);
  expect(
    value.rows!.reduce(
      (sum, r) => sum + r.label.length + (r.text?.length || 0),
      0,
    ),
  ).toBeLessThanOrEqual(600);
  const identity = {
    workspace: "one",
    taskId: "TASK-1",
    widgetType: "fixture/card",
    instanceId: "one",
  };
  for (const url of [
    "//evil.test",
    "javascript:alert(1)",
    "file:///tmp/a",
    "/plugins/other/x",
    "/plugins/fixture/%2e%2e/x",
    "https://evil.test/session",
  ])
    expect(
      safeHref({ kind: "session", url, title: "Unsafe" }, identity),
    ).toBeNull();
});
