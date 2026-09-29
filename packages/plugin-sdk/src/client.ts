import type {
  FrameContext,
  FrameErrorCode,
  FrameMethod,
  FrameMethods,
  HostMessage,
  ServiceRequest,
  ServiceResponse,
} from "./protocol";
import { isHostMessage, PROTOCOL_VERSION } from "./protocol";

/** Rejection raised by bridge calls; `code` is stable, `message` is advisory. */
export class BridgeError extends Error {
  constructor(
    readonly code: FrameErrorCode,
    message?: string,
  ) {
    super(message || code);
    this.name = "BridgeError";
  }
}

export interface StreamHandlers {
  message(data: string, name: string): void;
  open?(): void;
  error?(): void;
}

export interface Subscription {
  close(): void;
}

export interface DocketFrame {
  /** Latest context from the host; replaced on every update. */
  readonly context: FrameContext;
  /** Called with each new context, including the initial one. */
  onUpdate(listener: (context: FrameContext) => void): () => void;
  call<M extends FrameMethod>(
    method: M,
    params: FrameMethods[M]["params"],
  ): Promise<FrameMethods[M]["result"]>;
  /** Fetch a path from this plugin's own service (`/plugins/<name>/<path>`). */
  fetch(path: string, init?: Omit<ServiceRequest, "path">): Promise<ServiceResponse>;
  /** Fetch and parse JSON from this plugin's service; throws on non-2xx. */
  json<T = unknown>(path: string, init?: Omit<ServiceRequest, "path">): Promise<T>;
  /** Server-sent events from this plugin's service, relayed by the host. */
  stream(path: string, handlers: StreamHandlers): Subscription;
  /** Persist a small JSON value in the host; returned as `context.state` after reloads. */
  setState(value: unknown): Promise<void>;
  readTask(taskId?: string): Promise<unknown>;
  comment(text: string, taskId?: string): Promise<unknown>;
  moveTask(status: string, taskId?: string): Promise<unknown>;
  navigate(path: string): Promise<void>;
  openExternal(url: string): Promise<boolean>;
}

let pending: Promise<DocketFrame> | undefined;

/**
 * Connect to the Docket host. Resolves once the host sends `init`. Applies
 * theme tokens to `document.documentElement` and reports the document height
 * so the host can size the frame; pass `{autoResize: false}` to size manually.
 */
export function connect(
  options: { autoResize?: boolean; applyTheme?: boolean } = {},
): Promise<DocketFrame> {
  if (pending) return pending;
  const host = window.parent;
  if (host === window) return Promise.reject(new BridgeError("invalid_request", "not framed by Docket"));
  const calls = new Map<string, { resolve(v: unknown): void; reject(e: unknown): void }>();
  const streams = new Map<string, StreamHandlers>();
  const listeners = new Set<(context: FrameContext) => void>();
  let context: FrameContext | undefined;
  let sequence = 0;
  const post = (message: object) =>
    host.postMessage({ docket: PROTOCOL_VERSION, ...message }, "*");
  const theme = (value: FrameContext) => {
    if (options.applyTheme === false) return;
    const root = document.documentElement;
    root.dataset.theme = value.theme.scheme;
    root.style.colorScheme = value.theme.scheme;
    for (const [name, token] of Object.entries(value.theme.tokens))
      if (name.startsWith("--")) root.style.setProperty(name, token);
  };
  pending = new Promise<DocketFrame>((resolve) => {
    const call = <M extends FrameMethod>(method: M, params: FrameMethods[M]["params"]) =>
      new Promise<FrameMethods[M]["result"]>((done, fail) => {
        const id = `r${++sequence}`;
        calls.set(id, { resolve: done as (v: unknown) => void, reject: fail });
        post({ type: "request", id, method, params });
      });
    const fetchService = (path: string, init: Omit<ServiceRequest, "path"> = {}) =>
      call("service.fetch", { path, ...init });
    const frame: DocketFrame = {
      get context() {
        return context!;
      },
      onUpdate(listener) {
        listeners.add(listener);
        return () => listeners.delete(listener);
      },
      call,
      fetch: fetchService,
      async json(path, init) {
        const response = await fetchService(path, init);
        if (response.status < 200 || response.status > 299)
          throw new BridgeError("request_failed", `HTTP ${response.status}`);
        return JSON.parse(response.body);
      },
      stream(path, handlers) {
        let id = "";
        let closed = false;
        call("service.stream", { path }).then(
          ({ stream }) => {
            if (closed) return void call("stream.close", { stream });
            id = stream;
            streams.set(id, handlers);
          },
          () => handlers.error?.(),
        );
        return {
          close() {
            if (closed) return;
            closed = true;
            if (id) {
              streams.delete(id);
              void call("stream.close", { stream: id }).catch(() => undefined);
            }
          },
        };
      },
      setState: (value) => call("state.set", { value }).then(() => undefined),
      readTask: (taskId) => call("task.read", { taskId }),
      comment: (text, taskId) => call("task.comment", { text, taskId }),
      moveTask: (status, taskId) => call("task.move", { status, taskId }),
      navigate: (path) => call("navigate", { path }).then(() => undefined),
      openExternal: (url) => call("open.external", { url }).then((r) => r.opened),
    };
    window.addEventListener("message", (event) => {
      if (event.source !== host || !isHostMessage(event.data)) return;
      const message = event.data as HostMessage;
      if (message.type === "init" || message.type === "update") {
        const first = !context;
        context = message.context;
        theme(context);
        for (const listener of listeners) listener(context);
        if (first) resolve(frame);
      } else if (message.type === "response") {
        const entry = calls.get(message.id);
        if (!entry) return;
        calls.delete(message.id);
        if (message.ok) entry.resolve(message.value);
        else entry.reject(new BridgeError(message.error, message.message));
      } else if (message.type === "stream") {
        const handlers = streams.get(message.stream);
        if (!handlers) return;
        if (message.event === "message") handlers.message(message.data || "", message.name || "message");
        else if (message.event === "open") handlers.open?.();
        else {
          if (message.event === "closed") streams.delete(message.stream);
          handlers.error?.();
        }
      }
    });
    if (options.autoResize !== false && typeof ResizeObserver === "function") {
      let last = -1;
      const report = () => {
        // The element box, not scrollHeight: scrollHeight never shrinks below
        // the frame's current viewport, so content could only ever grow.
        const height = Math.ceil(document.documentElement.getBoundingClientRect().height);
        if (height !== last) post({ type: "resize", height: (last = height) });
      };
      const observer = new ResizeObserver(report);
      observer.observe(document.documentElement);
      if (document.body) observer.observe(document.body);
      report();
    }
    post({ type: "ready" });
  });
  return pending;
}
