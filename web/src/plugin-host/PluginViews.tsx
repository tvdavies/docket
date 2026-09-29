import { useState } from "react";
import type { BoardTask, PluginMetadata, PluginViewDeclaration } from "@docket/plugin-sdk";
import { usePluginScope } from "../registry/scope";
import { FrameHost } from "./FrameHost";

type View = { metadata: PluginMetadata; view: PluginViewDeclaration };

/** Every page declared by an enabled plugin, in board metadata order. */
export function pluginPages(plugins: PluginMetadata[] = []): View[] {
  return plugins.flatMap((metadata) => (metadata.pages || []).map((view) => ({ metadata, view })));
}

function pluginPanels(plugins: PluginMetadata[] = []): View[] {
  return plugins.flatMap((metadata) => (metadata.panels || []).map((view) => ({ metadata, view })));
}

/** A full workspace page contributed by a plugin at `/workspaces/:ws/p/:plugin/:page`. */
export function PluginPage({ plugin, page, navigate }: { plugin: string; page: string; navigate(path: string): void }) {
  const env = usePluginScope();
  const match = pluginPages(env.config.plugins).find((item) => item.metadata.name === plugin && item.view.id === page);
  if (!match)
    return (
      <div className="empty-state">
        <h2>Page unavailable</h2>
        <p>
          No enabled plugin named <code>{plugin}</code> provides a <code>{page}</code> page in this workspace.
        </p>
      </div>
    );
  return (
    <section className="plugin-page">
      <h1>{match.view.title}</h1>
      <FrameHost
        key={`${plugin}/${page}`}
        plugin={match.metadata}
        entry={match.view.entry}
        view={{ kind: "page", id: page }}
        title={match.view.title}
        maxHeight={4000}
        initialHeight={480}
        navigate={navigate}
      />
    </section>
  );
}

/** Plugin panels for a task, shown as tabs below the task document. Only the selected tab is mounted. */
export function TaskPanels({ task }: { task: BoardTask }) {
  const env = usePluginScope();
  const panels = pluginPanels(env.config.plugins);
  const [selected, setSelected] = useState("");
  if (!panels.length) return null;
  const key = (item: View) => `${item.metadata.name}/${item.view.id}`;
  const active = panels.find((item) => key(item) === selected);
  return (
    <section className="plugin-panels">
      <div role="tablist" aria-label="Plugin panels">
        {panels.map((item) => (
          <button
            key={key(item)}
            role="tab"
            type="button"
            aria-selected={active === item}
            onClick={() => setSelected(active === item ? "" : key(item))}
          >
            {item.view.title}
          </button>
        ))}
      </div>
      {active && (
        <div role="tabpanel">
          <FrameHost
            key={key(active)}
            plugin={active.metadata}
            entry={active.view.entry}
            view={{ kind: "panel", id: active.view.id }}
            title={active.view.title}
            task={task}
            taskId={task.id}
            maxHeight={1200}
            initialHeight={240}
          />
        </div>
      )}
    </section>
  );
}
