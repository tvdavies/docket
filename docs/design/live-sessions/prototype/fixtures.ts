// Deterministic synthetic fixtures for the JOB-0092 prototype.
//
// Everything here is hand-authored. There is no copied live transcript, no real
// task, agent, plan or PR. Marker strings exist only so tests can prove
// filtering happens BEFORE publication (never merely CSS-hidden):
//   SECRET-MARKER-*              must never appear in any payload or DOM
//   PREVIEW-EXCLUDED-USER-MARKER user text: full session only, never previews

import type { Availability, BoardTaskLike, ConnectionState, ExecutionState, WidgetReference } from "../contracts.proposed";
import type { RawEvent, SessionSource } from "./publisher";

export const WORKSPACE = "demo";
export const TASK_ID = "DEMO-0042";
export const WIDGET_TYPE = "dispatch/session";
export const SERVICE_BASE = "/plugins/dispatch";

export interface Frame {
  /** Number of raw events visible to the publisher at this frame. */
  through: number;
  execution: ExecutionState;
  connection?: ConnectionState;
  availability?: Availability;
  /** Explicit revision (for duplicate/older tests); otherwise auto-increment. */
  revision?: number;
  /** When true the host receives nothing this frame (freshness ages). */
  noReceipt?: boolean;
  /** Simulated milliseconds since the previous frame (default 1000). */
  elapsedMs?: number;
  /** Data schema version to publish (default 1; 2 = unsupported). */
  dataVersion?: number;
  /** Live cache empty and awaiting owner rehydration. */
  rehydrating?: boolean;
  /** Detail projection replaced rather than appended. */
  reset?: boolean;
  /** Simulate a missing range in the detailed stream. */
  detailGap?: boolean;
  /** Body deliberately throws during update (body-error fixture). */
  bodyThrows?: boolean;
  /** Task field override delivered in the same snapshot. */
  taskTitle?: string;
  /** Display preference override delivered in the same context update. */
  preferences?: { theme?: "light" | "dark"; density?: "compact" | "comfortable"; reducedMotion?: boolean };
  /** Switch host identity (retires the old instance). */
  taskId?: string;
  /** Terminal outcome published to the durable fallback. */
  outcome?: "completed" | "failed" | "cancelled";
  summary?: string;
  durationMs?: number;
  references?: WidgetReference[];
  note?: string;
}

export interface SessionFixture {
  sessionId: string;
  createdAt: string;
  source: SessionSource;
  frames: Frame[];
}

export interface Scenario {
  id: string;
  title: string;
  description: string;
  autoplay: boolean;
  sessions: SessionFixture[];
}

export const TASK: BoardTaskLike = {
  id: TASK_ID,
  title: "Make session progress easy to follow",
  status: "plan",
  project: "PROJ-DEMO",
  labels: ["design", "interface"],
  assignee: "planner",
  references: [{ id: "r1", kind: "plan", url: "https://example.com/plans/demo", title: "Design plan (demo)" }],
  created_at: "2026-09-10T11:58:00Z",
  updated_at: "2026-09-10T12:04:00Z",
};

export const BOARD_TASKS: BoardTaskLike[] = [
  { ...TASK, id: "DEMO-0041", title: "Tidy the workspace switcher", status: "plan", assignee: "planner", labels: ["interface"], references: [] },
  TASK,
  { ...TASK, id: "DEMO-0043", title: "Write the settings migration note", status: "implement", assignee: "implementer", labels: ["docs"], references: [] },
  { ...TASK, id: "DEMO-0044", title: "Review the filter popover", status: "review", assignee: "reviewer", labels: ["interface"], references: [] },
];

export const BOARD_STATUSES = [
  { id: "plan", label: "Plan" },
  { id: "implement", label: "Implement" },
  { id: "review", label: "Review" },
];

const SESSION_REF = (id: string): WidgetReference => ({ kind: "session", label: "Open session", url: `${SERVICE_BASE}/sessions/${encodeURIComponent(id)}` });
const PLAN_REF: WidgetReference = { kind: "plan", label: "Design plan (demo)", url: "https://example.com/plans/demo" };

const plannerEvents: RawEvent[] = [
  { seq: 1, type: "user.prompt", text: "Plan the live widget. PREVIEW-EXCLUDED-USER-MARKER" },
  { seq: 2, type: "reasoning", text: "SECRET-MARKER-REASONING private chain of thought must never be published" },
  { seq: 3, type: "content.delta", id: "a1", text: "I found the existing timeline pattern." },
  { seq: 4, type: "content.delta", id: "a1", text: " I'm checking how the session link keeps its task context." },
  { seq: 5, type: "tool.started", toolCallId: "t1", label: "Read · web/src/sessionProjection.ts", input: "path: web/src/sessionProjection.ts", privateInput: "/home/demo/private/checkout SECRET-MARKER-PATH" },
  { seq: 6, type: "tool.updated", toolCallId: "t1", status: "running", summary: "Reading 180 lines" },
  { seq: 7, type: "tool.updated", toolCallId: "t1", status: "completed", summary: "180 lines", output: "export function compactEvents(source) {\n  // merges tool updates into their original position\n}", durationMs: 1200 },
  { seq: 8, type: "content.delta", id: "a2", text: "I'll keep the same ordered view and separate connection freshness from execution state." },
  { seq: 9, type: "tool.started", toolCallId: "t2", label: "Inspect · task navigation", input: "routes: board, task, session" },
  { seq: 10, type: "content.delta", id: "a3", text: "Next I'll check how Back restores the expanded card." },
  { seq: 11, type: "tool.updated", toolCallId: "t2", status: "completed", summary: "3 routes", output: "board → task → session → task", durationMs: 2100 },
  { seq: 12, type: "content.delta", id: "a4", text: "Documented the route behavior and three edge cases." },
];

const planner = (events: RawEvent[] = plannerEvents): SessionSource => ({ persona: "Planner", stage: "Plan", startedAt: "2026-09-10T12:04:00Z", events });

const runningThrough = (through: number, extra: Partial<Frame> = {}): Frame => ({ through, execution: "running", connection: "live", ...extra });

const LIVE_FRAMES: Frame[] = [
  runningThrough(0, { note: "No activity yet" }),
  runningThrough(3),
  runningThrough(4),
  runningThrough(5),
  runningThrough(6),
  runningThrough(7),
  runningThrough(8),
  runningThrough(9),
  runningThrough(10),
  runningThrough(11),
  runningThrough(12),
  { through: 12, execution: "completed", connection: "live", outcome: "completed", summary: "Documented the route behavior and three edge cases.", durationMs: 138_000, references: [PLAN_REF], note: "Finalised" },
];

const s01 = (frames: Frame[], events?: RawEvent[]): SessionFixture => ({ sessionId: "demo-s01", createdAt: "2026-09-10T12:04:00Z", source: planner(events), frames });

const reviewerEvents: RawEvent[] = [
  { seq: 1, type: "content.delta", id: "a1", text: "Checking the fixture directory before I review the routes." },
  { seq: 2, type: "permission.requested", message: "May I read the fixture directory?" },
];

const s02Awaiting: SessionFixture = {
  sessionId: "demo-s02",
  createdAt: "2026-09-10T12:06:00Z",
  source: { persona: "Reviewer", stage: "Review", startedAt: "2026-09-10T12:06:00Z", events: reviewerEvents },
  frames: [{ through: 2, execution: "awaiting_input", connection: "live" }],
};

const s03Completed: SessionFixture = {
  sessionId: "demo-s03",
  createdAt: "2026-09-10T11:40:00Z",
  source: planner(),
  frames: [{ through: 12, execution: "completed", connection: "live", outcome: "completed", summary: "Documented the route behavior and three edge cases.", durationMs: 138_000, references: [PLAN_REF] }],
};

// Deterministic long session: 205 short assistant entries, then one tool whose
// bounded output (5,000 synthetic characters) needs two explicit "Show more"
// chunks in the full session, then three more entries streamed one per frame.
// 125 lines × 39 characters + 124 newlines + 1 trailing character = exactly 5,000 characters.
const LONG_OUTPUT = `${Array.from({ length: 125 }, (_, line) => `line ${String(line + 1).padStart(3, "0")}: synthetic bounded output`.padEnd(39, ".")).join("\n")}.`;
const longEvents: RawEvent[] = [
  ...Array.from({ length: 205 }, (_, index): RawEvent => ({ seq: index + 1, type: "content.delta", id: `long-${index + 1}`, text: `Entry ${index + 1}` })),
  { seq: 206, type: "tool.started", toolCallId: "t-long", label: "Run · bounded output", input: "cmd: synthetic" },
  { seq: 207, type: "tool.updated", toolCallId: "t-long", status: "completed", summary: "125 lines", output: LONG_OUTPUT, durationMs: 3_000 },
  { seq: 208, type: "content.delta", id: "long-206", text: "Entry 206" },
  { seq: 209, type: "content.delta", id: "long-207", text: "Entry 207" },
  { seq: 210, type: "content.delta", id: "long-208", text: "Entry 208" },
];
export const LONG_SESSION_ENTRIES = 205;

export const SCENARIOS: Scenario[] = [
  {
    id: "live",
    title: "Live preview before expansion",
    description: "Default. Assistant text grows and tools start/complete in order inside the unexpanded task entry, then the session finalises to a durable summary.",
    autoplay: true,
    sessions: [s01(LIVE_FRAMES)],
  },
  {
    id: "multi-session",
    title: "Multiple sessions",
    description: "Running, awaiting input and completed sessions on one task. The board chooses awaiting input first and links the rest.",
    autoplay: false,
    sessions: [s01([runningThrough(9)]), s02Awaiting, s03Completed],
  },
  {
    id: "pending",
    title: "Pending → running",
    description: "Queued, then starting, then running with no activity yet. No fabricated progress.",
    autoplay: true,
    sessions: [s01([
      { through: 0, execution: "queued", connection: "live" },
      { through: 0, execution: "starting", connection: "live" },
      runningThrough(0),
      runningThrough(3),
    ])],
  },
  {
    id: "awaiting-input",
    title: "Awaiting input → running",
    description: "A persistent input request stays visible outside the clipped body, then clears when work resumes.",
    autoplay: true,
    sessions: [s01([
      runningThrough(4),
      { through: 5, execution: "awaiting_input", connection: "live" },
      { through: 5, execution: "awaiting_input", connection: "live", noReceipt: true },
      runningThrough(7, { note: "Request resolved elsewhere" }),
      runningThrough(8),
    ], [
      plannerEvents[0], plannerEvents[1], plannerEvents[2], plannerEvents[3],
      { seq: 5, type: "permission.requested", message: "May I read the fixture directory?" },
      { seq: 6, type: "tool.started", toolCallId: "t1", label: "Read · fixtures/", input: "path: fixtures/" },
      { seq: 7, type: "tool.updated", toolCallId: "t1", status: "completed", summary: "12 files", durationMs: 800 },
      plannerEvents[7],
    ])],
  },
  {
    id: "tool-failure",
    title: "Running → tool failure → failed",
    description: "A tool fails at its original position without failing the session; a later session error makes the whole session failed with a prominent callout.",
    autoplay: true,
    sessions: [s01([
      runningThrough(4),
      runningThrough(5),
      runningThrough(6),
      runningThrough(7, { note: "Tool failed; session still running" }),
      runningThrough(8),
      { through: 9, execution: "failed", connection: "live", outcome: "failed", summary: "Stopped after the read failed twice.", durationMs: 41_000 },
    ], [
      plannerEvents[0], plannerEvents[1], plannerEvents[2], plannerEvents[3],
      plannerEvents[4],
      { seq: 6, type: "tool.updated", toolCallId: "t1", status: "running", summary: "Retrying" },
      { seq: 7, type: "tool.updated", toolCallId: "t1", status: "failed", summary: "Permission denied", output: "EACCES: permission denied (project-relative path only)", durationMs: 900 },
      { seq: 8, type: "content.delta", id: "a2", text: "The read failed; I'll try one more time." },
      { seq: 9, type: "session.error", message: "Engine exited: the read failed twice." },
    ])],
  },
  {
    id: "cancelled",
    title: "Running → cancelled",
    description: "Explicit cancelled label with the last useful outcome; never a green success.",
    autoplay: true,
    sessions: [s01([
      runningThrough(7),
      runningThrough(8),
      { through: 8, execution: "cancelled", connection: "live", outcome: "cancelled", summary: "Cancelled after reading the projection; nothing was documented.", durationMs: 22_000 },
    ])],
  },
  {
    id: "stale",
    title: "Disconnected → stale → rehydrated",
    description: "Transport freshness changes while execution stays last-known running. Reconnect replaces the projection without a second card.",
    autoplay: true,
    sessions: [s01([
      runningThrough(8),
      { through: 8, execution: "running", connection: "disconnected", noReceipt: true, note: "Connection lost; content retained" },
      { through: 8, execution: "running", connection: "disconnected", noReceipt: true, elapsedMs: 35_000, note: "TTL expired: stale" },
      { through: 8, execution: "running", connection: "connecting", noReceipt: true, rehydrating: true, note: "Reconnected; live cache empty, awaiting rehydration" },
      runningThrough(11, { reset: true, note: "Owner republished current summary" }),
      runningThrough(12),
    ])],
  },
  {
    id: "missing-service",
    title: "Missing service",
    description: "The saved summary remains; details are unavailable and Retry is read-only.",
    autoplay: false,
    sessions: [s01([
      { through: 12, execution: "completed", connection: "live", outcome: "completed", summary: "Documented the route behavior and three edge cases.", durationMs: 138_000, references: [PLAN_REF] },
      { through: 12, execution: "completed", connection: "disconnected", availability: "missing_service", noReceipt: true, note: "Plugin service unreachable" },
      { through: 12, execution: "completed", connection: "live", availability: "available", note: "Service back" },
    ])],
  },
  {
    id: "plugin-removed",
    title: "Plugin removed",
    description: "Workspace declaration absent: the generic host renders only the durable record, with no plugin code or subscription.",
    autoplay: false,
    sessions: [s01([
      { through: 12, execution: "completed", connection: "live", outcome: "completed", summary: "Documented the route behavior and three edge cases.", durationMs: 138_000, references: [PLAN_REF] },
      { through: 12, execution: "completed", connection: "disconnected", availability: "plugin_removed", noReceipt: true, note: "Declaration removed" },
    ])],
  },
  {
    id: "unknown-version",
    title: "Unknown data version",
    description: "An unsupported schema version renders the generic saved record and never assumes running.",
    autoplay: false,
    sessions: [s01([
      runningThrough(8),
      { through: 9, execution: "running", connection: "live", dataVersion: 2, note: "Publisher sent version 2" },
    ])],
  },
  {
    id: "duplicate-older",
    title: "Duplicate and older snapshots",
    description: "Revisions 5, 5 (duplicate), 3 (older) and 6 arrive; only 5 and 6 are applied.",
    autoplay: false,
    sessions: [s01([
      runningThrough(5, { revision: 5 }),
      runningThrough(5, { revision: 5, note: "Duplicate revision 5 — ignored" }),
      runningThrough(3, { revision: 3, note: "Older revision 3 — ignored" }),
      runningThrough(8, { revision: 6 }),
    ])],
  },
  {
    id: "detail-gap-reset",
    title: "Detail gap and reset",
    description: "With Expand open, a missing range pauses application and requests a snapshot; the next frame replaces the projection.",
    autoplay: false,
    sessions: [s01([
      runningThrough(7),
      runningThrough(9, { detailGap: true, note: "Detail events 8–9 missing" }),
      runningThrough(11, { reset: true, note: "Owner snapshot replaced detail" }),
      runningThrough(12),
    ])],
  },
  {
    id: "body-error",
    title: "Body error",
    description: "The plugin body throws during an update. The wrapper retires the body, keeps the generic fallback and does not remount in a loop.",
    autoplay: false,
    sessions: [s01([
      runningThrough(7),
      runningThrough(8, { bodyThrows: true, note: "Body throws on this update" }),
      runningThrough(9, { note: "Later snapshot must not resurrect the body" }),
    ])],
  },
  {
    id: "context-update",
    title: "Context and snapshot updates",
    description: "Task fields and display preferences update in place; an identity switch retires the instance and its late detail result is ignored.",
    autoplay: false,
    sessions: [s01([
      runningThrough(7),
      runningThrough(8, { taskTitle: "Make session progress easy to follow (renamed)", note: "Task title changed in the same snapshot" }),
      runningThrough(8, { preferences: { theme: "dark", density: "compact" }, note: "Preferences changed; no remount" }),
      runningThrough(9, { taskId: "DEMO-0043", note: "Identity switch: retire old instance, ignore its late result" }),
      runningThrough(10, { taskId: "DEMO-0043" }),
    ])],
  },
  {
    id: "long-session",
    title: "Long session and long output",
    description: "206 entries then streaming: the full session mounts one explicit 200-entry window (Show earlier / Show later), and a 5,000-character tool result is revealed in bounded Show more chunks.",
    autoplay: false,
    sessions: [s01([
      runningThrough(207, { note: "205 entries + completed tool with long output" }),
      runningThrough(208, { note: "Entry 206 streamed" }),
      runningThrough(209, { note: "Entry 207 streamed" }),
      runningThrough(210, { note: "Entry 208 streamed" }),
    ], longEvents)],
  },
  {
    id: "anatomy",
    title: "Anatomy comparison",
    description: "Static running state for comparing the standard composition with the custom-element body under the same wrapper, in board and activity locations.",
    autoplay: false,
    sessions: [s01([runningThrough(9)])],
  },
];

export const DEFAULT_SCENARIO = "live";

export function scenarioById(id: string | null): Scenario {
  return SCENARIOS.find((scenario) => scenario.id === id) ?? SCENARIOS[0];
}

export function sessionRef(id: string): WidgetReference { return SESSION_REF(id); }
