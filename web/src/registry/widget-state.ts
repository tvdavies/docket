import {
  jsonBytes,
  validWidgetPayload,
  WIDGET_BUDGETS,
  type LivePayload,
  type WidgetData,
  type WidgetFreshness,
} from "@docket/plugin-ui";
export interface AcceptedPreview {
  data: WidgetData;
  freshness: WidgetFreshness;
  bytes: number;
  signature: string;
  timer?: ReturnType<typeof setTimeout>;
}
export const previewKey = (type: string, task: string, instance: string) =>
  JSON.stringify([type, task, instance]);
/** One router per workspace; widgets never open preview transports. */
export class WidgetRouter {
  private entries = new Map<string, AcceptedPreview>();
  private bytes = 0;
  private connection = "idle";
  generation = 0;
  constructor(
    readonly workspace: string,
    private changed: () => void = () => {},
    private now: () => number = () => performance.now(),
  ) {}
  get(type: string, task: string, instance: string) {
    return this.entries.get(previewKey(type, task, instance));
  }
  accept(frame: LivePayload): boolean {
    if (
      !validWidgetPayload(frame.payload) ||
      !frame.task ||
      !frame.session ||
      !Number.isFinite(frame.ttl_ms) ||
      frame.ttl_ms < 1 ||
      frame.ttl_ms > 600000
    )
      return false;
    const payload = frame.payload,
      key = previewKey(frame.kind, frame.task, frame.session),
      old = this.entries.get(key);
    const signature = canonical(payload);
    const bytes = jsonBytes(payload);
    if (
      old &&
      (payload.revision < old.data.revision ||
        (payload.revision === old.data.revision && signature !== old.signature))
    )
      return false;
    if (
      (!old && this.entries.size >= WIDGET_BUDGETS.cacheEntries) ||
      this.bytes - (old?.bytes || 0) + bytes > WIDGET_BUDGETS.cacheBytes
    )
      return false;
    if (old?.timer) clearTimeout(old.timer);
    const receivedAt = this.now();
    const accepted: AcceptedPreview = {
      data: {
        version: payload.data.version,
        revision: payload.revision,
        value: payload.data.value,
      },
      bytes,
      signature,
      freshness: {
        connection: this.connection,
        receivedAt,
        expiresAt: receivedAt + frame.ttl_ms,
        stale: false,
        rehydrating: false,
        lastActivityAt: payload.last_activity_at,
      },
    };
    accepted.timer = setTimeout(() => {
      if (this.entries.get(key) !== accepted) return;
      accepted.freshness = { ...accepted.freshness, stale: true };
      accepted.timer = undefined;
      this.changed();
    }, frame.ttl_ms);
    this.bytes += bytes - (old?.bytes || 0);
    this.entries.set(key, accepted);
    this.changed();
    return true;
  }
  finalise(type: string, task: string, instance: string) {
    const key = previewKey(type, task, instance),
      entry = this.entries.get(key);
    if (!entry) return;
    clearTimeout(entry.timer);
    this.bytes -= entry.bytes;
    this.entries.delete(key);
    this.changed();
  }
  setConnection(connection: string) {
    this.connection = connection;
    for (const e of this.entries.values())
      e.freshness = {
        ...e.freshness,
        connection,
        stale: e.freshness.stale || connection !== "open",
      };
    this.changed();
  }
  reset() {
    this.generation++;
    for (const e of this.entries.values()) {
      clearTimeout(e.timer);
      e.timer = undefined;
      e.freshness = { ...e.freshness, stale: true, rehydrating: true };
    }
    this.changed();
  }
  destroy() {
    for (const e of this.entries.values()) clearTimeout(e.timer);
    this.entries.clear();
    this.bytes = 0;
    this.generation++;
  }
}
function canonical(v: unknown): string {
  if (v === null || typeof v !== "object") return JSON.stringify(v);
  if (Array.isArray(v)) return "[" + v.map(canonical).join(",") + "]";
  return (
    "{" +
    Object.keys(v)
      .sort()
      .map(
        (k) =>
          JSON.stringify(k) +
          ":" +
          canonical((v as Record<string, unknown>)[k]),
      )
      .join(",") +
    "}"
  );
}
