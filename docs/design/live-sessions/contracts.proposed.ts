/**
 * PROPOSED live-session widget contracts — JOB-0092 design documentation.
 *
 * Nothing in this file is a shipped Docket API. The shipped plugin UI contract
 * is `docs/plugin-ui.d.ts` (`mount(el, {workspace, task, pluginBase, refresh})`,
 * `update(task)`, `destroy()`); it has no location, session snapshot,
 * freshness or lifecycle subscription. JOB-0093 owns the production versioned
 * API, the compatibility adapter/version transition from `update(task)`, the
 * shared themed kit and the custom-element adapter. JOB-0050 owns the real
 * Dispatch session widget and publisher.
 *
 * Names are illustrative. The fixture prototype in `./prototype` imports these
 * types so that the demo data and the documented contract cannot drift apart,
 * but that does not promote them to a public package.
 *
 * Type-check: `bun run typecheck` from `docs/design/live-sessions`.
 */

// ---------------------------------------------------------------------------
// Shipped shape this proposal must stay compatible with (copied for reference;
// the authoritative declaration is docs/plugin-ui.d.ts).
// ---------------------------------------------------------------------------

export interface BoardTaskLike {
  id: string;
  title: string;
  status: string;
  project?: string;
  labels: string[];
  assignee?: string;
  references: TaskReferenceLike[];
  created_at: string;
  updated_at: string;
}

export interface TaskReferenceLike {
  id: string;
  kind: string;
  url: string;
  title?: string;
}

// ---------------------------------------------------------------------------
// 1. Host context → plugin UI
//
// Supplied by Docket when an enabled workspace declaration instantiates a
// widget. Identity/location/service base changes RETIRE the instance; other
// context fields (preferences) update in place. Context never carries a copy
// of the current task or live payload — those arrive in snapshots.
// ---------------------------------------------------------------------------

export type WidgetLocation = "board" | "activity";

export interface WidgetIdentity {
  workspace: string;
  taskId: string;
  /** Namespaced `<plugin>/<name>`, matching the enabled workspace declaration. */
  widgetType: string;
  /** Stable per-instance key, e.g. a session ID. Independent of reconnects/renders. */
  instanceId: string;
}

export interface DisplayPreferences {
  theme: "light" | "dark";
  density: "compact" | "comfortable";
  reducedMotion: boolean;
}

/**
 * Narrow, instance-bound helpers. None of these grant task writes, agent
 * control, permission or approval capability.
 */
export interface WidgetHelpers {
  /** Ask the host to refetch the current task. The host owns the cache. */
  refreshTask(): void;
  /**
   * Resolve a safe same-origin href for a plugin reference. Returns null for
   * anything that is not an allowlisted demo/plugin path.
   */
  hrefFor(reference: WidgetReference): string | null;
  /**
   * Optional, activity location only: select THIS instance as the task
   * view's detail selection and receive bounded, read-only detail snapshots
   * through `listener`. Returns a release function, or `null` when detail
   * cannot be provided right now (no live data, missing service). Board
   * instances never receive this helper.
   *
   * Ownership is view-scoped, not instance-scoped: a task view holds at most
   * `PROPOSED_BUDGETS.detailLeasesPerTaskView` selections. Selecting another
   * session revokes the previous one (`onRevoked("reselected")`). The host
   * also revokes when availability leaves `available`, when live data is
   * absent, or when the instance retires. A listener is never called after
   * release, revocation or abort, and results that belong to a retired
   * instance generation are dropped.
   */
  requestDetail?(listener: (detail: DetailSnapshot) => void, onRevoked?: (reason: DetailRevocation) => void): (() => void) | null;
}

/** Why the host withdrew a detail selection the widget did not release itself. */
export type DetailRevocation = "reselected" | "unavailable" | "retired";

export interface WidgetContext {
  identity: WidgetIdentity;
  location: WidgetLocation;
  /** Same-origin plugin service base, e.g. `/plugins/dispatch`. */
  serviceBase: string;
  preferences: DisplayPreferences;
  /** Aborted when the instance is retired. Cleanup must also tolerate destroy(). */
  signal: AbortSignal;
  helpers: WidgetHelpers;
}

// ---------------------------------------------------------------------------
// 2. Update snapshot → plugin UI
//
// Every update is a full replacement. Arrival never means a new instance.
// Duplicate/older `data.revision` values are ignored by the host before they
// reach the widget; preference/freshness changes are still delivered even when
// the plugin data revision is unchanged.
// ---------------------------------------------------------------------------

export type ConnectionState = "connecting" | "live" | "disconnected";

export interface Freshness {
  connection: ConnectionState;
  /** Host receipt time of the current data (ms epoch on the host clock). */
  receivedAt: number | null;
  /** Host-computed expiry from receipt time + TTL; stale when in the past. */
  expiresAt: number | null;
  /** Publisher-reported last activity, distinct from heartbeat. */
  lastActivityAt?: number;
  /** True while a restored/empty live cache is awaiting owner rehydration. */
  rehydrating?: boolean;
}

export type Availability =
  | "available"
  | "missing_service"
  | "plugin_removed";

export interface VersionedData<T = unknown> {
  version: number;
  /** Monotonic per identity. Same revision → no-op; lower → ignored. */
  revision: number;
  /** Opaque, bounded plugin display data (≤16 KiB proposed for previews). */
  value: T;
}

/** Plain durable record readable without any plugin code. */
export interface DurableFallback {
  version: number;
  revision: number;
  label: string;
  statusLabel: string;
  summary?: string;
  startedAt?: string;
  endedAt?: string;
  references: WidgetReference[];
}

export interface WidgetReference {
  kind: "session" | "task" | "plan" | "pr" | "other";
  label: string;
  /** Same-origin path for session/task; safe https URL for plan/pr. */
  url: string;
}

export interface WidgetSnapshot<T = unknown> {
  task: BoardTaskLike;
  /** Absent when the live cache holds nothing for this identity. */
  data?: VersionedData<T>;
  freshness: Freshness;
  availability: Availability;
  /** Absent before the publisher has created a durable record. */
  fallback?: DurableFallback;
}

// ---------------------------------------------------------------------------
// 3. Framework-neutral widget instance (mount/update/destroy preserved)
// ---------------------------------------------------------------------------

export interface WidgetModule<T = unknown> {
  type: string;
  mount(slot: HTMLElement, context: WidgetContext): WidgetInstance<T>;
}

export interface WidgetInstance<T = unknown> {
  update(snapshot: WidgetSnapshot<T>, context: WidgetContext): void;
  destroy(): void;
}

// ---------------------------------------------------------------------------
// 3b. Display adapter boundary inside the Docket-owned wrapper
//
// Docket owns the wrapper (header, notices, body slot, footer, fallback). A
// plugin supplies bounded presentation VALUES and chooses ONE body kind. The
// host never interprets ACP or session enums; it renders the labels/tones the
// adapter returns. Returning null from present() means "unsupported data" and
// the wrapper shows the generic saved record instead.
// ---------------------------------------------------------------------------

export type Tone = "neutral" | "info" | "positive" | "warning" | "danger";

export interface WidgetPresentation {
  /** e.g. "Planner · Plan" */
  label: string;
  /** Domain execution status as text + tone; never colour alone. */
  status: { text: string; tone: Tone };
  /** True for terminal outcomes; lets the wrapper collapse to the summary. */
  terminal: boolean;
  /** One meaningful current action for the board (≤120 chars). */
  action?: string;
  attention?: Attention;
  /** Ordered bounded entries for the activity body. */
  entries: PreviewEntry[];
  truncated: boolean;
  summary?: string;
  startedAt?: string;
  durationMs?: number;
  usage?: Usage;
  references: WidgetReference[];
}

export type BodyKind =
  | { kind: "standard" }
  /** Namespaced, statically registered element, e.g. `demo-session-body`. */
  | { kind: "custom-element"; tagName: string };

export interface DisplayAdapter<T = unknown> {
  supportedVersions: number[];
  present(data: VersionedData<T>): WidgetPresentation | null;
  body: BodyKind;
}

// ---------------------------------------------------------------------------
// 4. Publisher → durable host record (creation / finalisation)
//
// Identity is (workspace, kind, sessionId). Same-revision writes are no-ops;
// a lower revision cannot overwrite newer final state. Intermediate text/tool
// updates NEVER touch this record or the event ledger.
// ---------------------------------------------------------------------------

export interface DurableSessionRecord {
  version: 1;
  kind: string;
  sessionId: string;
  workspace: string;
  taskId: string;
  createdAt: string;
  revision: number;
  /** Safe same-origin path, e.g. `/plugins/dispatch/sessions/<id>`. */
  sessionPath: string;
  fallback: Omit<DurableFallback, "version" | "revision">;
  /** Present only on finalisation. */
  outcome?: "completed" | "failed" | "cancelled";
}

// ---------------------------------------------------------------------------
// 5. Dispatch → ephemeral card payload (workspace live channel)
//
// Carried by the existing `{kind, task, session, payload, ttl_ms}` envelope.
// Filtered BEFORE publication: no user prompts, reasoning, raw arguments,
// results, absolute paths or credentials.
// ---------------------------------------------------------------------------

export type ExecutionState =
  | "queued"
  | "starting"
  | "running"
  | "awaiting_input"
  | "completed"
  | "failed"
  | "cancelled";

export type ToolStatus = "running" | "completed" | "failed" | "cancelled";

export interface PreviewTextEntry {
  id: string;
  seq: number;
  type: "assistant";
  text: string;
}

export interface PreviewToolEntry {
  id: string;
  seq: number;
  type: "tool";
  toolCallId: string;
  label: string;
  status: ToolStatus;
  summary?: string;
  durationMs?: number;
}

export type PreviewEntry = PreviewTextEntry | PreviewToolEntry;

export interface Attention {
  id: string;
  kind: "error" | "input";
  message: string;
}

export interface Usage {
  tokens?: number;
  cost?: number;
  currency?: string;
  source: string;
}

export interface SessionPreviewV1 {
  version: 1;
  revision: number;
  sessionId: string;
  execution: ExecutionState;
  persona: string;
  stage: string;
  /** ≤120 characters; one meaningful current action for the board. */
  currentAction?: string;
  activityAt?: string;
  startedAt?: string;
  /** Latest ≤4 ordered entries, ≤600 assistant characters total. */
  previewEntries: PreviewEntry[];
  truncated: boolean;
  attention?: Attention;
  /** Only with known bounds; unknown means absent, not zero. */
  durationMs?: number;
  /** Only when recorded. */
  usage?: Usage;
  /** Published only if actually available. */
  references?: WidgetReference[];
}

// ---------------------------------------------------------------------------
// 6. Dispatch → selected session detail (one selection per task view)
//
// The host stores the last ACCEPTED preview payload per identity separately
// from whatever arrives next. Duplicate/older revisions never replace it, and
// a remount, preference change or metadata-only update is served from the
// accepted payload, never by re-reading the rejected input.
// ---------------------------------------------------------------------------

export interface DetailToolEntry extends PreviewToolEntry {
  /** Redacted, ≤2,000 characters in task view. */
  input?: string;
  output?: string;
}

export type DetailEntry = PreviewTextEntry | DetailToolEntry;

export interface DetailSnapshot {
  workspace: string;
  taskId: string;
  /** Safe canonical same-origin task path. */
  taskPath: string;
  sessionId: string;
  revision: number;
  /**
   * The `throughSeq` this window continues from. A widget applies the
   * snapshot only when `reset` is true or `baseSeq` equals its current
   * `throughSeq`; otherwise it has a gap, pauses and asks for a reset rather
   * than concatenating unknown history.
   */
  baseSeq: number;
  throughSeq: number;
  /** True when the projection was replaced (reconnect/reset), not appended. */
  reset: boolean;
  /** Up to 12 entries / 1,600 assistant characters in task view. */
  entries: DetailEntry[];
  /** True when earlier entries or text were dropped to meet those bounds. */
  truncated: boolean;
}

// ---------------------------------------------------------------------------
// 7. Proposed UX budgets (JOB-0093 owns final transport/render budgets; a
// change must retain these UX bounds or receive design review).
// ---------------------------------------------------------------------------

export const PROPOSED_BUDGETS = {
  previewEntries: 4,
  previewAssistantChars: 600,
  expandedEntries: 12,
  expandedAssistantChars: 1_600,
  toolDetailChars: 2_000,
  previewPayloadBytes: 16 * 1024,
  currentActionChars: 120,
  boardActionLines: 2,
  /** Full session mounts one explicit window of this many entries; earlier/later windows are reader-driven. */
  fullSessionMountedEntries: 200,
  /** Full-session tool output is revealed in chunks of this size behind an explicit Show more. */
  fullSessionOutputChunkChars: 2_000,
  detailLeasesPerTaskView: 1,
  detailLeasesPerBoardCard: 0,
  summaryRenewMs: 1_000,
  heartbeatMs: 10_000,
  liveTtlMs: 30_000,
} as const;

// ---------------------------------------------------------------------------
// 8. Illustrative example — deliberately separate context and snapshot inputs.
// ---------------------------------------------------------------------------

export function exampleUsage(
  instance: WidgetInstance<SessionPreviewV1>,
  fixtureTask: BoardTaskLike,
  safeOrderedPreview: SessionPreviewV1,
  helpers: WidgetHelpers,
): void {
  const fixtureAbort = new AbortController();
  const context: WidgetContext = {
    identity: {
      workspace: "demo",
      taskId: "DEMO-0042",
      widgetType: "dispatch/session",
      instanceId: "demo-s01",
    },
    location: "activity",
    serviceBase: "/plugins/dispatch",
    preferences: { theme: "dark", density: "comfortable", reducedMotion: true },
    signal: fixtureAbort.signal,
    helpers,
  };
  const snapshot: WidgetSnapshot<SessionPreviewV1> = {
    task: fixtureTask,
    data: { version: 1, revision: 7, value: safeOrderedPreview },
    freshness: { connection: "live", receivedAt: 0, expiresAt: PROPOSED_BUDGETS.liveTtlMs },
    availability: "available",
    fallback: {
      version: 1,
      revision: 1,
      label: "Planner session",
      statusLabel: "Running",
      startedAt: "2026-09-10T12:04:00Z",
      references: [{ kind: "session", label: "Open session", url: "/plugins/dispatch/sessions/demo-s01" }],
    },
  };
  // Real API shape and legacy update(task) transition belong to JOB-0093.
  instance.update(snapshot, context);
}
