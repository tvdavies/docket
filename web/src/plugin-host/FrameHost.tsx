import { useEffect, useRef, useState } from "react";
import {
  themeTokens,
  type BoardTask,
  type FrameContext,
  type FrameViewKind,
  type FrameWidget,
  type PluginCapability,
  type PluginMetadata,
} from "@docket/plugin-sdk";
import { addComment, getTask, patchTask } from "../api/client";
import { usePluginScope } from "../registry/scope";
import { clampHeight, frameState, FrameBridge, type BridgeEffects } from "./bridge";

/** Frames may not load scripts from elsewhere, so the sandbox needs only these. */
export const FRAME_SANDBOX = "allow-scripts allow-forms";

export function frameSource(plugin: PluginMetadata, entry: string) {
  return plugin.ui_base && entry ? `${plugin.ui_base}/${entry}` : "";
}

export function defaultEffects(navigate: (path: string) => void): BridgeEffects {
  return {
    fetch: (input, init) => fetch(input, init),
    eventSource: (url) => new EventSource(url),
    readTask: (workspace, taskId) => getTask(workspace, taskId),
    comment: (workspace, taskId, text) => addComment(workspace, taskId, text),
    move: (workspace, taskId, status) => patchTask(workspace, taskId, { status }),
    navigate,
    openExternal(url) {
      if (!window.confirm(`Open this link in a new tab?\n\n${url}`)) return false;
      window.open(url, "_blank", "noopener,noreferrer");
      return true;
    },
  };
}

/**
 * One sandboxed plugin view. The iframe has an opaque origin (no
 * allow-same-origin), cannot reach Docket's API, and talks only through
 * FrameBridge. A new `ui_base` hash swaps the src in place: the bridge
 * re-sends init with the host-held state, so hot reload keeps frame state.
 */
export function FrameHost({
  plugin,
  entry,
  view,
  title,
  task,
  taskId,
  widget,
  maxHeight = 640,
  initialHeight = 160,
  navigate,
  effects,
}: {
  plugin: PluginMetadata;
  entry: string;
  view: { kind: FrameViewKind; id: string };
  title: string;
  task?: BoardTask;
  taskId?: string;
  widget?: FrameWidget;
  maxHeight?: number;
  initialHeight?: number;
  navigate?(path: string): void;
  effects?: BridgeEffects;
}) {
  const env = usePluginScope();
  const frame = useRef<HTMLIFrameElement>(null);
  const bridge = useRef<FrameBridge | undefined>(undefined);
  const [height, setHeight] = useState(initialHeight);
  const src = frameSource(plugin, entry);
  const capabilities = (plugin.capabilities || []) as PluginCapability[];
  const stateKey = JSON.stringify([env.workspace, plugin.name, view.kind, view.id, taskId || "", widget?.identity.instanceId || ""]);
  const context: FrameContext = {
    plugin: plugin.name,
    workspace: env.workspace,
    view,
    task: capabilities.includes("task.read") ? task : undefined,
    taskId,
    widget,
    capabilities,
    preferences: env.preferences,
    theme: { scheme: env.preferences.theme, tokens: themeTokens(env.preferences.theme, env.preferences.density) },
    state: frameState(stateKey) ?? null,
  };
  const latest = useRef(context);
  latest.current = context;
  const go = navigate || ((path: string) => {
    history.pushState(null, "", path);
    dispatchEvent(new PopStateEvent("popstate"));
  });
  useEffect(() => {
    const instance = new FrameBridge(
      () => frame.current?.contentWindow,
      plugin,
      latest.current,
      stateKey,
      effects || defaultEffects(go),
      (value) => setHeight(clampHeight(value, maxHeight)),
    );
    bridge.current = instance;
    return () => {
      instance.destroy();
      if (bridge.current === instance) bridge.current = undefined;
    };
    // Capabilities and service base are part of the trust boundary: a change rebuilds the bridge.
  }, [plugin.name, plugin.service_base, capabilities.join(","), stateKey, maxHeight]);
  useEffect(() => {
    bridge.current?.reset();
  }, [src]);
  const contextKey = JSON.stringify(context);
  useEffect(() => {
    bridge.current?.update(latest.current);
  }, [contextKey]);
  if (!src) return <p className="plugin-frame-missing">{title} is unavailable: the plugin publishes no UI.</p>;
  return (
    <iframe
      ref={frame}
      className="plugin-frame"
      title={title}
      src={src}
      sandbox={FRAME_SANDBOX}
      referrerPolicy="no-referrer"
      loading="lazy"
      style={{ height, maxHeight }}
      data-plugin={plugin.name}
    />
  );
}
