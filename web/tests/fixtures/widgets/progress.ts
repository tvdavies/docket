import {
  standardWidgetBody,
  type DocketPluginUIV2,
  type DetailProvider,
  type WidgetModule,
  type WidgetPresentation,
} from "@docket/plugin-ui";
export const counters = {
  mounts: 0,
  destroys: 0,
  active: 0,
  detail: 0,
  maximumDetail: 0,
  updates: 0,
  aborted: 0,
};
export const faults = { mount: false, update: false, destroy: false };
export const detailSinks = new Set<Parameters<DetailProvider["open"]>[1]>();
export const provider: DetailProvider = {
  open(ctx, sink) {
    counters.detail++;
    counters.maximumDetail = Math.max(counters.maximumDetail, counters.detail);
    detailSinks.add(sink);
    let closed = false;
    return {
      requestReset() {
        sink.frame({
          version: 1,
          identity: ctx.identity,
          dataVersion: 1,
          revision: 100,
          baseSeq: 0,
          throughSeq: 100,
          reset: true,
          value: {
            label: "Selected detail",
            text: "Bounded detail from fixture service",
            count: 100,
          },
        });
      },
      close() {
        if (closed) return;
        closed = true;
        counters.detail--;
        detailSinks.delete(sink);
      },
    };
  },
};
export const present: WidgetModule["present"] = (
  snapshot,
  context,
): WidgetPresentation | null => {
  const data = snapshot.data?.value as
    | { label?: string; text?: string; count?: number; attention?: boolean }
    | undefined;
  if (!data || typeof data.text !== "string") return null;
  return {
    label: data.label || "Fixture progress",
    status: {
      text: data.attention ? "Input needed" : "Processing",
      tone: data.attention ? "warning" : "info",
    },
    priority: data.attention ? "attention" : "active",
    terminal: false,
    action: `Processed ${data.count || 0} fixture items`,
    rows: [
      {
        key: "text",
        order: 1,
        role: "text",
        label: "Progress",
        text: data.text,
      },
      {
        key: "step",
        order: 2,
        role: "step",
        label: "Check samples",
        text: `${data.count || 0} samples checked`,
      },
    ],
    notice: data.attention
      ? { text: "Fixture input notice", tone: "warning" }
      : undefined,
    references: [
      {
        kind: "task",
        url: `/workspaces/${context.identity.workspace}/tasks/${context.identity.taskId}`,
        title: "Original task",
      },
    ],
  };
};
export const progressPlugin: DocketPluginUIV2 = {
  apiVersion: 2,
  name: "fixture-progress",
  widgets: [
    {
      type: "fixture-progress/job",
      dataVersions: [1],
      present,
      detail: provider,
      mount(body, context) {
        counters.mounts++;
        context.signal.addEventListener("abort", () => counters.aborted++, {
          once: true,
        });
        if (faults.mount) throw Error("fixture private mount diagnostic");
        counters.active++;
        const instance = standardWidgetBody(body, context);
        return {
          update(snapshot, ctx, view) {
            counters.updates++;
            if (faults.update) throw Error("fixture private update diagnostic");
            instance.update(snapshot, ctx, view);
          },
          destroy() {
            counters.destroys++;
            counters.active--;
            instance.destroy();
            if (faults.destroy) throw Error("fixture destroy");
          },
        };
      },
    },
  ],
};
