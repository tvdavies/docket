import { useState } from "react";
import {
  boundedPresentation,
  safeHref,
  type BoardTask,
  type PluginMetadata,
  type WidgetData,
  type WidgetFreshness,
  type WidgetLocation,
  type WidgetPresentation,
  type WidgetRecordV1,
  type WidgetSummary,
} from "@docket/plugin-sdk";
import { widgetDeclaration } from "../registry/registry";
import { usePluginScope } from "../registry/scope";
import { FrameHost } from "./FrameHost";

const priorityRank = { attention: 0, error: 1, active: 2, history: 3 } as const;

/** The saved ledger fallback as a presentation. */
export function savedPresentation(record: WidgetRecordV1 | WidgetSummary): WidgetPresentation {
  const f = record.fallback;
  return {
    label: f.label,
    status: {
      text: f.status_label,
      tone: f.priority === "error" ? "danger" : f.priority === "attention" ? "warning" : "neutral",
    },
    priority: f.priority,
    terminal: record.phase === "finalised",
    summary: "summary" in f ? f.summary : undefined,
    references: f.references,
    startedAt: "started_at" in f ? f.started_at : undefined,
    endedAt: "ended_at" in f ? f.ended_at : undefined,
  };
}

/**
 * A live preview's presentation: `data.value.presentation`, or `data.value`
 * itself when it is shaped like one. Anything else keeps the saved fallback.
 */
export function livePresentation(data: WidgetData | undefined): WidgetPresentation | undefined {
  const value = data?.value as Record<string, unknown> | undefined;
  if (!value || typeof value !== "object") return undefined;
  const candidate = (value.presentation && typeof value.presentation === "object" ? value.presentation : value) as WidgetPresentation;
  return typeof candidate.label === "string" && candidate.status && typeof candidate.status.text === "string" ? candidate : undefined;
}

export function currentPresentation(record: WidgetRecordV1 | WidgetSummary, data: WidgetData | undefined, expanded = false) {
  const live = record.phase === "finalised" ? undefined : livePresentation(data);
  return boundedPresentation(live || savedPresentation(record), expanded);
}

function freshnessLabel(record: WidgetRecordV1 | WidgetSummary, freshness: WidgetFreshness | undefined, connection: string) {
  if (record.phase === "finalised") return "Saved";
  if (!freshness) return "Awaiting live data";
  if (freshness.stale) return "Last known · stale";
  return connection === "open" ? "Live" : "Disconnected";
}

/**
 * Host-rendered widget. The collapsed view is declarative (no plugin code
 * runs); expanding in the activity timeline mounts the plugin's sandboxed
 * `entry` frame when it declares one.
 */
export function WidgetCard({
  workspace,
  task,
  record,
  location,
  config,
}: {
  workspace: string;
  task: BoardTask;
  record: WidgetRecordV1;
  location: WidgetLocation;
  config?: PluginMetadata[];
}) {
  const env = usePluginScope();
  const [expanded, setExpanded] = useState(false);
  const plugins = config || env.config.plugins || [];
  const match = widgetDeclaration(plugins, record.widget_type, location);
  const live = record.phase === "finalised" ? undefined : env.router?.get(record.widget_type, task.id, record.instance_id);
  const presentation = currentPresentation(record, live?.data, expanded);
  const identity = { workspace, taskId: task.id, widgetType: record.widget_type, instanceId: record.instance_id };
  const entry = location === "activity" && match?.declaration.entry && match.metadata.ui_base ? match.declaration.entry : "";
  const notices = [presentation.notice?.text, match ? "" : "Plugin disabled · showing saved record"].filter(Boolean).join(" · ");
  const start = presentation.startedAt ? Date.parse(presentation.startedAt) : NaN;
  const end = presentation.endedAt ? Date.parse(presentation.endedAt) : NaN;
  return (
    <div
      className="docket-widget"
      data-widget={record.widget_type}
      data-instance={record.instance_id}
      data-location={location}
      data-theme={env.preferences.theme}
      data-density={env.preferences.density}
    >
      <header>
        <small>{record.widget_type.split("/")[0]}</small>
        <strong>{presentation.label}</strong>
        <span role="status" aria-live="polite" data-tone={presentation.status.tone}>
          {presentation.status.text}
        </span>
        <small>{freshnessLabel(record, live?.freshness, env.connection)}</small>
      </header>
      {notices && (
        <p className="widget-notice" role="status" data-tone={presentation.notice?.tone || "info"}>
          {notices}
        </p>
      )}
      {presentation.summary && <p className="widget-summary">{presentation.summary}</p>}
      {!!presentation.rows?.length && (
        <ol className="widget-rows">
          {presentation.rows.map((row) => (
            <li key={row.key} data-role={row.role}>
              {row.label && <b>{row.label}</b>} {row.role === "code" ? <code>{row.text}</code> : row.text}
            </li>
          ))}
        </ol>
      )}
      {entry && expanded && (
        <FrameHost
          plugin={match!.metadata}
          entry={entry}
          view={{ kind: "widget", id: record.widget_type }}
          title={`${presentation.label} · ${match!.declaration.title}`}
          task={task}
          taskId={task.id}
          widget={{ identity, record, data: live?.data, freshness: live?.freshness, presentation }}
        />
      )}
      {entry && (
        <div className="widget-controls">
          <button type="button" aria-expanded={expanded} onClick={() => setExpanded((value) => !value)}>
            {expanded ? "Collapse" : "Expand"}
          </button>
        </div>
      )}
      <div className="widget-references">
        {Number.isFinite(start) && (
          <small>
            Started {new Date(start).toLocaleTimeString()}
            {Number.isFinite(end) && end >= start ? ` · ${Math.round((end - start) / 1000)}s elapsed` : ""}
          </small>
        )}
        {(presentation.references || []).map((ref) => {
          const href = safeHref(ref, identity);
          return href ? (
            <a key={`${ref.kind}:${ref.url}`} href={href} rel="noreferrer" target={href.startsWith("/") ? undefined : "_blank"}>
              {ref.title}
            </a>
          ) : (
            <span key={`${ref.kind}:${ref.url}`}>{ref.title}</span>
          );
        })}
      </div>
    </div>
  );
}

/** Top widget per declared type on a board card, ranked by live priority. */
export function BoardWidgets({ workspace, task, config }: { workspace: string; task: BoardTask; config: PluginMetadata[] }) {
  const env = usePluginScope();
  const rank = (record: WidgetSummary) => {
    const live = env.router?.get(record.widget_type, task.id, record.instance_id);
    return priorityRank[currentPresentation(record, live?.data).priority] ?? 3;
  };
  const summaries = (task.widget_summaries || []).filter((r) => widgetDeclaration(config, r.widget_type, "board"));
  const types = [...new Set(summaries.map((r) => r.widget_type))].sort();
  return (
    <>
      {types.map((type) => {
        const records = summaries
          .filter((r) => r.widget_type === type)
          .sort(
            (a, b) =>
              rank(a) - rank(b) || Date.parse(b.created_at) - Date.parse(a.created_at) || a.instance_id.localeCompare(b.instance_id),
          );
        return (
          <div key={type}>
            <WidgetCard workspace={workspace} task={task} record={records[0] as WidgetRecordV1} location="board" config={config} />
            {records.length > 1 && (
              <a href={`/workspaces/${encodeURIComponent(workspace)}/tasks/${encodeURIComponent(task.id)}#activity`}>
                +{records.length - 1} more {records[0].fallback.label} widgets
              </a>
            )}
          </div>
        );
      })}
    </>
  );
}
