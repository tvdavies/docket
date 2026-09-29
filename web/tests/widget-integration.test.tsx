import { act, fireEvent, render } from "@testing-library/react";
import { expect, test, vi } from "vitest";
import { TaskDetail } from "../src/views/task/TaskDetail";
import type { TaskDetail as Detail } from "../src/types";
import { BoardStore } from "../src/store/board-store";
import fixture from "../../packages/plugin-ui/fixtures/wire.json";

test("lifecycle revision refresh preserves task drafts and one stable activity identity", async () => {
  const base: Detail = {
    id: "TASK-1",
    title: "Original",
    status: "todo",
    created_at: "2026-09-10T00:00:00Z",
    updated_at: "2026-09-10T00:00:00Z",
    labels: [],
    references: [],
    description: "Original description",
    description_html: "<p>Original description</p>",
    comments: [],
    attachments: [],
    activity: [],
    widgets: [],
    widget_revision: "empty",
  };
  let response = base;
  const fetcher = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(
      async () =>
        new Response(JSON.stringify(response), {
          headers: { "Content-Type": "application/json" },
        }),
    );
  const props = {
    workspace: "fixture",
    taskId: "TASK-1",
    open: true,
    config: { statuses: ["todo"], terminal: [], labels: [] },
    live: [],
    onClose() {},
    onPatch: async () => base,
    onCursor() {},
  };
  try {
    let view!: ReturnType<typeof render>;
    await act(async () => {
      view = render(<TaskDetail {...props} widgetRevision="empty" />);
    });
    fireEvent.click(view.getByRole("button", { name: "Edit" }));
    fireEvent.change(view.getByLabelText("Description"), {
      target: { value: "Keep my unsaved draft" },
    });
    const record = {
      ...fixture.record,
      revision: 3,
      phase: "finalised" as const,
      fallback: { ...fixture.record.fallback, summary: "Durable final result" },
    };
    response = {
      ...base,
      title: "New server title",
      widget_revision: "terminal",
      widgets: [record as any],
      activity: [
        {
          at: record.created_at,
          kind: "widget",
          type: record.widget_type,
          data: { record },
        },
      ],
    };
    await act(async () => {
      view.rerender(<TaskDetail {...props} widgetRevision="terminal" />);
    });
    expect(
      (view.getByLabelText("Description") as HTMLTextAreaElement).value,
    ).toBe("Keep my unsaved draft");
    expect(view.getByText("Durable final result")).toBeInTheDocument();
    expect(view.container.querySelectorAll("[data-widget]")).toHaveLength(1);
    view.unmount();
  } finally {
    fetcher.mockRestore();
  }
});
test("workspace frame bursts coalesce and disposed stores release cached timers", () => {
  vi.useFakeTimers();
  const store = new BoardStore("fixture");
  const listener = vi.fn();
  store.subscribe(listener);
  for (let i = 0; i < 100; i++)
    store.applyLive({
      kind: "fixture-progress/job",
      task: `TASK-${i}`,
      session: "job",
      ttl_ms: 1000,
      payload: fixture.preview,
    });
  expect(listener).not.toHaveBeenCalled();
  vi.advanceTimersByTime(20);
  expect(listener).toHaveBeenCalledTimes(1);
  store.destroy();
  vi.advanceTimersByTime(2000);
  expect(listener).toHaveBeenCalledTimes(1);
  expect(store.getSnapshot().connection).toBe("closed");
  vi.useRealTimers();
});
