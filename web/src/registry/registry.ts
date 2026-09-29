import { useSyncExternalStore } from "react";
import {
  adaptLegacyPluginUI,
  serviceBase,
  safeHref,
  WIDGET_BUDGETS,
  type BoardTask,
  type PluginMetadata,
  type PluginUI,
  type ResolvedReference,
  type TaskReference,
  type WidgetLocation,
} from "@docket/plugin-ui";
import { catalogue } from "./catalogue";
const modules = new Map<string, PluginUI>();
const listeners = new Set<() => void>();
let version = 0;
export function registerPluginUI(plugin: PluginUI, name?: string) {
  name ||=
    "apiVersion" in plugin
      ? plugin.name
      : (
          plugin.cards?.[0]?.type ||
          plugin.referenceResolvers?.[0]?.id ||
          ""
        ).split("/")[0];
  if (!name || (modules.has(name) && modules.get(name) !== plugin))
    throw new Error("duplicate_definition");
  const ids =
    "apiVersion" in plugin
      ? [
          ...(plugin.widgets || []).map((c) => c.type),
          ...(plugin.referenceResolvers || []).map((r) => r.id),
        ]
      : [
          ...(plugin.cards || []).map((c) => c.type),
          ...(plugin.referenceResolvers || []).map((r) => r.id),
        ];
  if (
    new Set(ids).size !== ids.length ||
    ids.some((id) => !id.startsWith(name + "/"))
  )
    throw new Error("duplicate_definition");
  if (modules.get(name) === plugin) return;
  modules.set(name, plugin);
  version++;
  for (const notify of listeners) notify();
}
export function loadBuiltinPluginUI() {
  for (const [name, plugin] of Object.entries(catalogue))
    registerPluginUI(plugin, name);
}
export function useRegistryVersion() {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    () => version,
    () => version,
  );
}
export function widgetModule(
  config: PluginMetadata[],
  type: string,
  location: WidgetLocation,
) {
  for (const metadata of config) {
    const declaration = metadata.cards.find((c) => c.type === type);
    if (
      !declaration ||
      !(
        declaration.locations ||
        ((metadata.api_version || 1) === 1 ? ["board", "activity"] : [])
      ).includes(location)
    )
      continue;
    const module = modules.get(metadata.name);
    const base = serviceBase(metadata.name, metadata.service_base);
    if (!module) return { metadata, declaration, base };
    if (
      metadata.api_version !== 2 ||
      !("apiVersion" in module) ||
      module.apiVersion !== 2 ||
      module.name !== metadata.name
    )
      return { metadata, declaration, base };
    return {
      metadata,
      declaration,
      base,
      module: module.widgets?.find((w) => w.type === type),
    };
  }
  return undefined;
}
export function cardModules(
  task: BoardTask,
  config: PluginMetadata[] = [],
  location: WidgetLocation = "board",
) {
  return config.flatMap((metadata) => {
    const plugin = modules.get(metadata.name);
    if ((metadata.api_version || 1) !== 1 || !plugin || "apiVersion" in plugin)
      return [];
    const legacy = adaptLegacyPluginUI(plugin);
    return metadata.cards.flatMap((declaration) => {
      if (!(declaration.locations || ["board", "activity"]).includes(location))
        return [];
      const module = legacy.cards?.find((c) => c.type === declaration.type);
      try {
        return module?.appliesTo(task)
          ? [
              {
                module,
                base: serviceBase(metadata.name, metadata.service_base) || "",
              },
            ]
          : [];
      } catch {
        return [];
      }
    });
  });
}
export function fallbackReference(ref: TaskReference): ResolvedReference {
  return {
    label: ref.title || ref.url,
    icon: "link",
    meta: { kind: ref.kind },
  };
}
export class ReferenceRegistry {
  private cache = new Map<string, Promise<ResolvedReference>>();
  private controller = new AbortController();
  constructor(
    readonly workspace: string,
    readonly generation: string,
    readonly config: PluginMetadata[],
  ) {}
  destroy() {
    this.controller.abort();
    this.cache.clear();
  }
  resolve(
    reference: TaskReference,
    taskId: string,
  ): Promise<ResolvedReference> {
    const fallback = fallbackReference(reference);
    if (
      this.controller.signal.aborted ||
      !reference.resolver_id ||
      reference.resolver_generation !== this.generation
    )
      return Promise.resolve(fallback);
    const metadata = this.config.find((p) =>
      p.reference_resolvers.some((r) => r.id === reference.resolver_id),
    );
    const plugin = metadata && modules.get(metadata.name);
    if (!metadata || !plugin) return Promise.resolve(fallback);
    const resolver = plugin.referenceResolvers?.find(
      (r) => r.id === reference.resolver_id,
    );
    if (
      !resolver ||
      (metadata.api_version || 1) !==
        ("apiVersion" in plugin ? plugin.apiVersion : 1)
    )
      return Promise.resolve(fallback);
    const base = serviceBase(metadata.name, metadata.service_base);
    const key = JSON.stringify([
      this.workspace,
      this.generation,
      reference.resolver_id,
      base,
      taskId,
      reference,
    ]);
    const cached = this.cache.get(key);
    if (cached) return cached;
    const request = new Promise<ResolvedReference>((resolve) => {
      const controller = new AbortController();
      let done = false;
      const finish = (value: ResolvedReference) => {
        if (done) return;
        done = true;
        clearTimeout(timer);
        this.controller.signal.removeEventListener("abort", abort);
        controller.abort();
        resolve(value);
      };
      const abort = () => finish(fallback);
      const timer = setTimeout(abort, WIDGET_BUDGETS.timeoutMS);
      this.controller.signal.addEventListener("abort", abort, { once: true });
      Promise.resolve()
        .then(() =>
          controller.signal.aborted
            ? fallback
            : "apiVersion" in plugin
              ? (
                  resolver as import("@docket/plugin-ui").ReferenceResolverV2
                ).resolve(reference, {
                  workspace: this.workspace,
                  taskId,
                  serviceBase: base,
                  signal: controller.signal,
                })
              : (
                  resolver as import("@docket/plugin-ui").ReferenceResolverModule
                ).resolve(reference, { pluginBase: base || "" }),
        )
        .then((value) => {
          if (!value || typeof value.label !== "string")
            return finish(fallback);
          const meta = Object.fromEntries(
            Object.entries(value.meta || {})
              .filter(([, v]) => typeof v === "string")
              .slice(0, 8)
              .map(([k, v]) => [k.slice(0, 120), v.slice(0, 256)]),
          );
          const href =
            value.href &&
            safeHref(
              { kind: reference.kind, url: value.href, title: value.label },
              {
                workspace: this.workspace,
                taskId,
                widgetType: metadata.name + "/reference",
                instanceId: "",
              },
            );
          finish({
            label: value.label.slice(0, 120),
            icon: value.icon?.slice(0, 32),
            meta,
            href: href || undefined,
          });
        }, abort)
        .catch(abort);
    });
    if (this.cache.size >= WIDGET_BUDGETS.resolverEntries)
      this.cache.delete(this.cache.keys().next().value!);
    this.cache.set(key, request);
    return request;
  }
}
export function resolveReference(
  reference: TaskReference,
  registry?: ReferenceRegistry,
  taskId = "",
) {
  return (
    registry?.resolve(reference, taskId) ||
    Promise.resolve(fallbackReference(reference))
  );
}
