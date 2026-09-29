import {
  isFrameMessage,
  PROTOCOL_VERSION,
  type FrameContext,
  type FrameErrorCode,
  type FrameMethod,
  type HostMessage,
  type PluginCapability,
  type PluginMetadata,
  type ServiceRequest,
  type ServiceResponse,
} from "@docket/plugin-sdk";

export const BRIDGE_LIMITS = Object.freeze({
  inFlight: 32,
  streams: 4,
  requestMS: 30000,
  requestBytes: 1 << 20,
  responseBytes: 4 << 20,
  stateBytes: 16 << 10,
  stateEntries: 256,
  minHeight: 32,
});

/** Side effects the bridge performs on a frame's behalf; injected for tests. */
export interface BridgeEffects {
  fetch(input: string, init: RequestInit): Promise<Response>;
  eventSource(url: string): EventSource;
  readTask(workspace: string, taskId: string): Promise<unknown>;
  comment(workspace: string, taskId: string, text: string): Promise<unknown>;
  move(workspace: string, taskId: string, status: string): Promise<unknown>;
  navigate(path: string): void;
  openExternal(url: string): boolean;
}

/** Which capability, if any, gates each method. `null` means always allowed. */
const gate: Record<FrameMethod, PluginCapability | null> = {
  "task.read": "task.read",
  "task.comment": "task.comment",
  "task.move": "task.move",
  "service.fetch": "service.fetch",
  "service.stream": "service.stream",
  "stream.close": "service.stream",
  navigate: null,
  "open.external": "open.external",
  "state.set": null,
};

class Denied extends Error {
  constructor(
    readonly code: FrameErrorCode,
    message?: string,
  ) {
    super(message || code);
  }
}

// Host-held frame state, keyed per plugin view. It outlives frames so a
// hot-reloaded frame gets back what it saved; it does not outlive the page.
const states = new Map<string, unknown>();
export function frameState(key: string) {
  return states.get(key);
}
export function clearFrameState() {
  states.clear();
}

const text = (value: unknown, max: number): value is string =>
  typeof value === "string" && value.length <= max;

/**
 * Resolves a frame-supplied path under the plugin's own service. Rejects
 * anything that could step outside `/plugins/<name>/` once normalised.
 */
export function servicePath(base: string | undefined, path: unknown): string {
  if (!base) throw new Denied("capability_denied", "plugin has no service");
  if (
    !text(path, 2048) ||
    !path.startsWith("/") ||
    path.startsWith("//") ||
    /[\\\s\x00-\x1f]/.test(path) ||
    /%2e|%2f|%5c/i.test(path) ||
    path.split(/[?#]/)[0].split("/").some((p) => p === "." || p === "..")
  )
    throw new Denied("invalid_request", "invalid service path");
  return base + path;
}

export function clampHeight(height: number, max: number) {
  if (!Number.isFinite(height)) return BRIDGE_LIMITS.minHeight;
  return Math.round(Math.min(Math.max(height, BRIDGE_LIMITS.minHeight), max));
}

const forwardedHeaders = new Set(["accept", "content-type", "last-event-id"]);

/**
 * Host end of one frame's bridge. Messages are accepted only from the frame
 * window this bridge was created for; every request is checked against the
 * plugin's declared capabilities before any effect runs.
 */
export class FrameBridge {
  private inFlight = 0;
  private streams = new Map<string, EventSource>();
  private streamSeq = 0;
  private ready = false;
  private stopped = false;
  private readonly listener = (event: MessageEvent) => this.receive(event);

  constructor(
    private readonly target: () => Window | null | undefined,
    private readonly plugin: PluginMetadata,
    private context: FrameContext,
    private readonly stateKey: string,
    private readonly effects: BridgeEffects,
    private readonly onResize: (height: number) => void = () => undefined,
    private readonly host: Pick<Window, "addEventListener" | "removeEventListener"> = window,
  ) {
    host.addEventListener("message", this.listener);
  }

  /** Replace the context; sent to the frame immediately if it is ready. */
  update(context: FrameContext) {
    this.context = context;
    if (this.ready) this.post({ docket: PROTOCOL_VERSION, type: "update", context: this.withState() });
  }

  /** The frame navigated (hot reload or src swap): wait for a new `ready`. */
  reset() {
    this.ready = false;
    this.closeStreams();
  }

  destroy() {
    if (this.stopped) return;
    this.stopped = true;
    this.host.removeEventListener("message", this.listener);
    this.closeStreams();
  }

  private withState(): FrameContext {
    return { ...this.context, state: states.get(this.stateKey) ?? null };
  }

  private post(message: HostMessage) {
    this.target()?.postMessage(message, "*");
  }

  private allowed(capability: PluginCapability) {
    return this.context.capabilities.includes(capability);
  }

  receive(event: Pick<MessageEvent, "source" | "data">) {
    const frame = this.target();
    if (this.stopped || !frame || event.source !== frame || !isFrameMessage(event.data)) return;
    const message = event.data;
    if (message.type === "ready") {
      this.ready = true;
      this.post({ docket: PROTOCOL_VERSION, type: "init", context: this.withState() });
      return;
    }
    if (message.type === "resize") {
      this.onResize(message.height);
      return;
    }
    const { id, method, params } = message;
    const respond = (value: unknown) =>
      this.post({ docket: PROTOCOL_VERSION, type: "response", id, ok: true, value });
    const fail = (error: FrameErrorCode, detail?: string) =>
      this.post({ docket: PROTOCOL_VERSION, type: "response", id, ok: false, error, message: detail });
    if (!Object.hasOwn(gate, method)) return fail("unknown_method");
    const capability = gate[method];
    if (capability && !this.allowed(capability))
      return fail("capability_denied", `${capability} is not declared in ui.capabilities`);
    if (this.inFlight >= BRIDGE_LIMITS.inFlight) return fail("too_many_requests");
    this.inFlight++;
    this.handle(method, params)
      .then(respond, (error: unknown) =>
        error instanceof Denied
          ? fail(error.code, error.message)
          : fail("request_failed", error instanceof Error ? error.message.slice(0, 200) : undefined),
      )
      .finally(() => this.inFlight--);
  }

  private task(params: { taskId?: unknown }) {
    const id = params.taskId ?? this.context.taskId;
    if (!text(id, 200) || !id) throw new Denied("invalid_request", "taskId is required outside a task view");
    return id;
  }

  private async handle(method: FrameMethod, raw: unknown): Promise<unknown> {
    const params = (raw && typeof raw === "object" ? raw : {}) as Record<string, unknown>;
    const workspace = this.context.workspace;
    switch (method) {
      case "task.read":
        return this.effects.readTask(workspace, this.task(params));
      case "task.comment":
        if (!text(params.text, 20000) || !params.text.trim()) throw new Denied("invalid_request", "text is required");
        return this.effects.comment(workspace, this.task(params), params.text);
      case "task.move":
        if (!text(params.status, 200) || !params.status) throw new Denied("invalid_request", "status is required");
        return this.effects.move(workspace, this.task(params), params.status);
      case "service.fetch":
        return this.fetch(params as unknown as ServiceRequest);
      case "service.stream":
        return this.stream(params.path);
      case "stream.close": {
        const stream = this.streams.get(String(params.stream));
        stream?.close();
        this.streams.delete(String(params.stream));
        return null;
      }
      case "navigate": {
        const prefix = `/workspaces/${encodeURIComponent(workspace)}`;
        if (!text(params.path, 2048) || !(params.path === prefix || params.path.startsWith(prefix + "/")) || /[\\\s]|\/\/|\/\.\.?(\/|$)/.test(params.path))
          throw new Denied("invalid_request", "navigation must stay within this workspace");
        this.effects.navigate(params.path);
        return null;
      }
      case "open.external": {
        let url: URL;
        try {
          url = new URL(String(params.url));
        } catch {
          throw new Denied("invalid_request", "invalid URL");
        }
        if (!["https:", "http:"].includes(url.protocol) || url.username || url.password)
          throw new Denied("invalid_request", "only http(s) URLs can be opened");
        return { opened: this.effects.openExternal(url.href) };
      }
      case "state.set": {
        const encoded = JSON.stringify(params.value ?? null);
        if (encoded === undefined || new TextEncoder().encode(encoded).length > BRIDGE_LIMITS.stateBytes)
          throw new Denied("invalid_request", "state exceeds 16 KiB");
        if (!states.has(this.stateKey) && states.size >= BRIDGE_LIMITS.stateEntries)
          states.delete(states.keys().next().value!);
        states.set(this.stateKey, JSON.parse(encoded));
        return null;
      }
    }
  }

  private async fetch(request: ServiceRequest): Promise<ServiceResponse> {
    const url = servicePath(this.plugin.service_base, request.path);
    const method = (request.method || "GET").toUpperCase();
    if (!["GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"].includes(method))
      throw new Denied("invalid_request", "unsupported method");
    if (request.body !== undefined && (!text(request.body, BRIDGE_LIMITS.requestBytes) || method === "GET" || method === "HEAD"))
      throw new Denied("invalid_request", "invalid body");
    const headers = new Headers();
    for (const [name, value] of Object.entries(request.headers || {}))
      if (forwardedHeaders.has(name.toLowerCase()) && text(value, 1024)) headers.set(name, value);
    const abort = new AbortController();
    const timer = setTimeout(() => abort.abort(), BRIDGE_LIMITS.requestMS);
    try {
      // credentials: "omit" — the service proxy strips them anyway; never send them.
      const response = await this.effects.fetch(url, { method, headers, body: request.body, credentials: "omit", signal: abort.signal, redirect: "error" });
      const body = await response.text();
      if (body.length > BRIDGE_LIMITS.responseBytes) throw new Denied("request_failed", "response too large");
      const keep: Record<string, string> = {};
      for (const name of ["content-type", "etag", "last-modified"]) {
        const value = response.headers.get(name);
        if (value) keep[name] = value;
      }
      return { status: response.status, headers: keep, body };
    } catch (error) {
      if (abort.signal.aborted) throw new Denied("timeout");
      throw error;
    } finally {
      clearTimeout(timer);
    }
  }

  private stream(path: unknown) {
    const url = servicePath(this.plugin.service_base, path);
    if (this.streams.size >= BRIDGE_LIMITS.streams) throw new Denied("too_many_requests", "too many open streams");
    const id = `s${++this.streamSeq}`;
    const source = this.effects.eventSource(url);
    const relay = (event: "open" | "message" | "error", name?: string, data?: string) => {
      if (this.streams.get(id) !== source) return;
      this.post({ docket: PROTOCOL_VERSION, type: "stream", stream: id, event, name, data });
    };
    source.onopen = () => relay("open");
    source.onmessage = (event) => relay("message", "message", String(event.data));
    source.onerror = () => {
      if (source.readyState === EventSource.CLOSED) {
        relay("error");
        this.streams.delete(id);
        this.post({ docket: PROTOCOL_VERSION, type: "stream", stream: id, event: "closed" });
      } else relay("error");
    };
    this.streams.set(id, source);
    return { stream: id };
  }

  private closeStreams() {
    for (const source of this.streams.values()) source.close();
    this.streams.clear();
  }
}
