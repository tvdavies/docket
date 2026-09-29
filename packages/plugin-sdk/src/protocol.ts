import type {
  BoardTask,
  PluginCapability,
  WidgetData,
  WidgetFreshness,
  WidgetIdentity,
  WidgetPreferences,
  WidgetPresentation,
  WidgetRecordV1,
} from "./contracts";

/**
 * Host ↔ frame protocol. Every message carries `docket: PROTOCOL_VERSION`.
 * Frames run with an opaque origin, so both sides authenticate by
 * `event.source`, never by `event.origin`.
 */
export const PROTOCOL_VERSION = 1;

export type FrameViewKind = "widget" | "panel" | "page";

export interface FrameWidget {
  identity: WidgetIdentity;
  record: WidgetRecordV1;
  /** Latest live preview, when the widget is still running. */
  data?: WidgetData;
  freshness?: WidgetFreshness;
  presentation: WidgetPresentation;
}

export interface FrameTheme {
  scheme: "light" | "dark";
  /** CSS custom properties (`--docket-widget-*`) to apply to the frame root. */
  tokens: Record<string, string>;
}

export interface FrameContext {
  plugin: string;
  workspace: string;
  view: { kind: FrameViewKind; id: string };
  /** Present for task-scoped views when the plugin holds `task.read`. */
  task?: BoardTask;
  taskId?: string;
  widget?: FrameWidget;
  capabilities: PluginCapability[];
  preferences: WidgetPreferences;
  theme: FrameTheme;
  /** Host-held state saved by `state.set`; survives frame reloads. */
  state: unknown;
}

export interface ServiceRequest {
  path: string;
  method?: string;
  headers?: Record<string, string>;
  body?: string;
}

export interface ServiceResponse {
  status: number;
  headers: Record<string, string>;
  body: string;
}

/** Request methods, their params and results. */
export interface FrameMethods {
  "task.read": { params: { taskId?: string }; result: unknown };
  "task.comment": { params: { taskId?: string; text: string }; result: unknown };
  "task.move": { params: { taskId?: string; status: string }; result: unknown };
  "service.fetch": { params: ServiceRequest; result: ServiceResponse };
  "service.stream": { params: { path: string }; result: { stream: string } };
  "stream.close": { params: { stream: string }; result: null };
  navigate: { params: { path: string }; result: null };
  "open.external": { params: { url: string }; result: { opened: boolean } };
  "state.set": { params: { value: unknown }; result: null };
}
export type FrameMethod = keyof FrameMethods;

export type FrameErrorCode =
  | "capability_denied"
  | "invalid_request"
  | "unknown_method"
  | "too_many_requests"
  | "timeout"
  | "request_failed"
  | "cancelled";

export type HostMessage =
  | { docket: 1; type: "init"; context: FrameContext }
  | { docket: 1; type: "update"; context: FrameContext }
  | { docket: 1; type: "response"; id: string; ok: true; value: unknown }
  | {
      docket: 1;
      type: "response";
      id: string;
      ok: false;
      error: FrameErrorCode;
      message?: string;
    }
  | {
      docket: 1;
      type: "stream";
      stream: string;
      event: "open" | "message" | "error" | "closed";
      name?: string;
      data?: string;
    };

export type FrameMessage =
  | { docket: 1; type: "ready" }
  | { docket: 1; type: "resize"; height: number }
  | {
      docket: 1;
      type: "request";
      id: string;
      method: FrameMethod;
      params: unknown;
    };

const record = (value: unknown): value is Record<string, unknown> =>
  !!value && typeof value === "object" && !Array.isArray(value);

export function isFrameMessage(value: unknown): value is FrameMessage {
  if (!record(value) || value.docket !== PROTOCOL_VERSION) return false;
  switch (value.type) {
    case "ready":
      return true;
    case "resize":
      return typeof value.height === "number" && Number.isFinite(value.height);
    case "request":
      return (
        typeof value.id === "string" &&
        value.id.length > 0 &&
        value.id.length <= 64 &&
        typeof value.method === "string"
      );
    default:
      return false;
  }
}

export function isHostMessage(value: unknown): value is HostMessage {
  return (
    record(value) &&
    value.docket === PROTOCOL_VERSION &&
    ["init", "update", "response", "stream"].includes(String(value.type))
  );
}
