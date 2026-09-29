import {
  safeHref,
  WIDGET_BUDGETS,
  type PluginMetadata,
  type PluginWidgetDeclaration,
  type ResolvedReference,
  type TaskReference,
  type WidgetLocation,
} from "@docket/plugin-sdk";

/**
 * Finds the enabled plugin that declares a widget type for a location.
 * Declarations come only from the server's board metadata; the web build
 * contains no plugin code.
 */
export function widgetDeclaration(
  config: PluginMetadata[],
  type: string,
  location: WidgetLocation,
): { metadata: PluginMetadata; declaration: PluginWidgetDeclaration } | undefined {
  for (const metadata of config) {
    const declaration = (metadata.widgets || []).find((w) => w.type === type);
    if (declaration && declaration.slots.includes(location)) return { metadata, declaration };
  }
  return undefined;
}

export function fallbackReference(ref: TaskReference): ResolvedReference {
  return {
    label: ref.title || ref.url,
    icon: "link",
    meta: { kind: ref.kind },
  };
}

/** Coerces an untrusted resolver answer into bounded display values. */
export function boundedResolution(
  value: unknown,
  reference: TaskReference,
  workspace: string,
  taskId: string,
  plugin: string,
): ResolvedReference | undefined {
  const v = value as ResolvedReference | undefined;
  if (!v || typeof v !== "object" || typeof v.label !== "string" || !v.label.trim()) return undefined;
  const meta = Object.fromEntries(
    Object.entries(v.meta && typeof v.meta === "object" ? v.meta : {})
      .filter(([, item]) => typeof item === "string")
      .slice(0, 8)
      .map(([k, item]) => [k.slice(0, 120), (item as string).slice(0, 256)]),
  );
  const href =
    typeof v.href === "string"
      ? safeHref(
          { kind: reference.kind, url: v.href, title: v.label },
          { workspace, taskId, widgetType: plugin + "/reference", instanceId: "" },
        )
      : null;
  return {
    label: v.label.slice(0, 120),
    icon: typeof v.icon === "string" ? v.icon.slice(0, 32) : undefined,
    meta,
    href: href || undefined,
  };
}

/**
 * Resolves task references through each plugin's declared `endpoint`: a POST
 * of `{workspace, task_id, reference}` to the plugin service via the proxy.
 * Anything unexpected — no endpoint, stale generation, timeout, bad JSON —
 * falls back to the reference's own title.
 */
export class ReferenceRegistry {
  private cache = new Map<string, Promise<ResolvedReference>>();
  private controller = new AbortController();
  constructor(
    readonly workspace: string,
    readonly generation: string,
    readonly config: PluginMetadata[],
    private readonly fetcher: typeof fetch = (...args) => fetch(...args),
  ) {}
  destroy() {
    this.controller.abort();
    this.cache.clear();
  }
  resolve(reference: TaskReference, taskId: string): Promise<ResolvedReference> {
    const fallback = fallbackReference(reference);
    if (this.controller.signal.aborted || !reference.resolver_id || reference.resolver_generation !== this.generation)
      return Promise.resolve(fallback);
    const metadata = this.config.find((p) => p.reference_resolvers.some((r) => r.id === reference.resolver_id));
    const resolver = metadata?.reference_resolvers.find((r) => r.id === reference.resolver_id);
    if (!metadata?.service_base || !resolver?.endpoint) return Promise.resolve(fallback);
    const url = metadata.service_base + resolver.endpoint;
    const key = JSON.stringify([this.workspace, this.generation, url, taskId, reference]);
    const cached = this.cache.get(key);
    if (cached) return cached;
    const abort = new AbortController();
    const stop = () => abort.abort();
    const timer = setTimeout(stop, WIDGET_BUDGETS.timeoutMS);
    this.controller.signal.addEventListener("abort", stop, { once: true });
    const request = this.fetcher(url, {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: JSON.stringify({ workspace: this.workspace, task_id: taskId, reference }),
      credentials: "omit",
      signal: abort.signal,
    })
      .then((response) => (response.ok ? response.json() : undefined))
      .then((value) => boundedResolution(value, reference, this.workspace, taskId, metadata.name) || fallback)
      .catch(() => fallback)
      .finally(() => {
        clearTimeout(timer);
        this.controller.signal.removeEventListener("abort", stop);
      });
    if (this.cache.size >= WIDGET_BUDGETS.resolverEntries) this.cache.delete(this.cache.keys().next().value!);
    this.cache.set(key, request);
    return request;
  }
}
export function resolveReference(reference: TaskReference, registry?: ReferenceRegistry, taskId = "") {
  return registry?.resolve(reference, taskId) || Promise.resolve(fallbackReference(reference));
}
