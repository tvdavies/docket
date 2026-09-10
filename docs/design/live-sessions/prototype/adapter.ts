// Illustrative Dispatch display adapter: maps the versioned preview payload to
// the wrapper's presentation values. The HOST never sees the execution enum;
// it renders whatever label/tone the adapter returns. JOB-0050/JOB-0093 own
// the production equivalent.

import type { DisplayAdapter, ExecutionState, SessionPreviewV1, Tone, VersionedData, WidgetPresentation } from "../contracts.proposed";

const EXECUTION: Record<ExecutionState, { text: string; tone: Tone; terminal: boolean }> = {
  queued: { text: "Queued", tone: "neutral", terminal: false },
  starting: { text: "Starting", tone: "neutral", terminal: false },
  running: { text: "Running", tone: "info", terminal: false },
  awaiting_input: { text: "Awaiting input", tone: "warning", terminal: false },
  completed: { text: "Completed", tone: "positive", terminal: true },
  failed: { text: "Failed", tone: "danger", terminal: true },
  cancelled: { text: "Cancelled", tone: "warning", terminal: true },
};

export function presentSession(data: VersionedData<SessionPreviewV1>, summary?: string): WidgetPresentation | null {
  if (data.version !== 1) return null;
  const value = data.value;
  const execution = EXECUTION[value.execution];
  if (!execution) return null; // unknown enum: generic saved record, never "running"
  return {
    label: `${value.persona} · ${value.stage}`,
    status: { text: execution.text, tone: execution.tone },
    terminal: execution.terminal,
    action: value.currentAction ?? (value.execution === "running" ? "Running; no activity yet" : undefined),
    attention: value.attention,
    entries: value.previewEntries,
    truncated: value.truncated,
    summary,
    startedAt: value.startedAt,
    durationMs: value.durationMs,
    usage: value.usage,
    references: value.references ?? [],
  };
}

export function makeAdapter(body: "standard" | "custom-element", summaryFor: (sessionId: string) => string | undefined): DisplayAdapter<SessionPreviewV1> {
  return {
    supportedVersions: [1],
    present: (data) => presentSession(data, summaryFor(data.value.sessionId)),
    body: body === "standard" ? { kind: "standard" } : { kind: "custom-element", tagName: "demo-session-body" },
  };
}

/** Board choice among several sessions: awaiting input, failed, active, then latest terminal. */
export function chooseBoardSession<T extends { execution: ExecutionState; createdAt: string; sessionId: string }>(sessions: T[]): T | null {
  const rank = (execution: ExecutionState): number => execution === "awaiting_input" ? 0 : execution === "failed" ? 1 : ["running", "starting", "queued"].includes(execution) ? 2 : 3;
  return [...sessions].sort((a, b) => rank(a.execution) - rank(b.execution) || b.createdAt.localeCompare(a.createdAt) || a.sessionId.localeCompare(b.sessionId))[0] ?? null;
}
