// Fixture publisher: turns hand-authored synthetic session events into the
// bounded, filtered payloads described in ../contracts.proposed.ts.
//
// This is demo/documentation code. It is NOT a second production session
// projection; JOB-0050 must reuse Dispatch's tested projection across the
// plugin boundary. It exists so the prototype can prove that filtering and
// bounds happen BEFORE publication (not by hiding DOM with CSS).

import {
  PROPOSED_BUDGETS,
  type Attention,
  type DetailEntry,
  type DetailSnapshot,
  type ExecutionState,
  type PreviewEntry,
  type SessionPreviewV1,
  type ToolStatus,
  type WidgetReference,
} from "../contracts.proposed";

/** Synthetic raw events, loosely shaped like Dispatch canonical events. */
export type RawEvent =
  | { seq: number; type: "user.prompt"; text: string }
  | { seq: number; type: "reasoning"; text: string }
  | { seq: number; type: "content.delta"; id: string; text: string }
  /** `input` is the already-redacted, project-relative detail; `privateInput` stands in for raw arguments that must never be published. */
  | { seq: number; type: "tool.started"; toolCallId: string; label: string; input?: string; privateInput?: string }
  | { seq: number; type: "tool.updated"; toolCallId: string; status: ToolStatus; summary?: string; output?: string; durationMs?: number }
  | { seq: number; type: "session.error"; message: string }
  | { seq: number; type: "permission.requested"; message: string };

export interface SessionSource {
  persona: string;
  stage: string;
  startedAt: string;
  events: RawEvent[];
}

/** Ordered entries after tool updates are merged into their original position. */
export interface OrderedEntry {
  id: string;
  seq: number;
  type: "assistant" | "tool" | "user";
  text?: string;
  toolCallId?: string;
  label?: string;
  status?: ToolStatus;
  summary?: string;
  durationMs?: number;
  input?: string;
  output?: string;
}

/**
 * Compacts raw events into ordered entries. Assistant deltas with the same id
 * append to one entry; tool updates merge into the tool.started row so a tool
 * that completes or fails stays where it began.
 */
export function orderEntries(events: RawEvent[], through: number): OrderedEntry[] {
  const entries: OrderedEntry[] = [];
  const byId = new Map<string, OrderedEntry>();
  for (const event of events.slice(0, through)) {
    switch (event.type) {
      case "content.delta": {
        const existing = byId.get(`a:${event.id}`);
        if (existing) { existing.text = (existing.text ?? "") + event.text; break; }
        const entry: OrderedEntry = { id: `a:${event.id}`, seq: event.seq, type: "assistant", text: event.text };
        byId.set(entry.id, entry); entries.push(entry); break;
      }
      case "tool.started": {
        const entry: OrderedEntry = { id: `t:${event.toolCallId}`, seq: event.seq, type: "tool", toolCallId: event.toolCallId, label: event.label, status: "running", input: event.input };
        byId.set(entry.id, entry); entries.push(entry); break;
      }
      case "tool.updated": {
        const existing = byId.get(`t:${event.toolCallId}`);
        if (!existing) break; // unknown tool id: ignore rather than invent a row
        existing.status = event.status;
        if (event.summary) existing.summary = event.summary;
        if (event.output) existing.output = event.output;
        if (event.durationMs !== undefined) existing.durationMs = event.durationMs;
        break;
      }
      case "user.prompt": {
        entries.push({ id: `u:${event.seq}`, seq: event.seq, type: "user", text: event.text }); break;
      }
      default: break; // reasoning, errors and permission requests never become entries
    }
  }
  return entries;
}

function trimAssistant(entries: OrderedEntry[], maxChars: number): { entries: PreviewEntry[]; truncated: boolean } {
  let budget = maxChars; let truncated = false;
  const out: PreviewEntry[] = [];
  // Walk from the newest entry backwards so the latest text survives.
  for (let index = entries.length - 1; index >= 0; index -= 1) {
    const entry = entries[index];
    if (entry.type === "tool") {
      out.unshift({ id: entry.id, seq: entry.seq, type: "tool", toolCallId: entry.toolCallId!, label: entry.label!, status: entry.status!, summary: entry.summary, durationMs: entry.durationMs });
      continue;
    }
    if (entry.type !== "assistant") continue;
    const text = entry.text ?? "";
    if (budget <= 0) { truncated = true; continue; }
    if (text.length > budget) { truncated = true; out.unshift({ id: entry.id, seq: entry.seq, type: "assistant", text: `…${text.slice(text.length - budget)}` }); budget = 0; continue; }
    budget -= text.length; out.unshift({ id: entry.id, seq: entry.seq, type: "assistant", text });
  }
  return { entries: out, truncated };
}

export function currentActionFor(entries: OrderedEntry[], execution: ExecutionState): string | undefined {
  const runningTool = [...entries].reverse().find((entry) => entry.type === "tool" && entry.status === "running");
  if (runningTool) return `${runningTool.label}`.slice(0, PROPOSED_BUDGETS.currentActionChars);
  const lastText = [...entries].reverse().find((entry) => entry.type === "assistant" && entry.text);
  if (lastText?.text) return lastText.text.replace(/\s+/g, " ").trim().slice(0, PROPOSED_BUDGETS.currentActionChars);
  if (execution === "running") return "Running; no activity yet";
  return undefined;
}

export interface PublishInput {
  sessionId: string;
  source: SessionSource;
  through: number;
  execution: ExecutionState;
  revision: number;
  activityAt?: string;
  durationMs?: number;
  references?: WidgetReference[];
}

/** Publishes the bounded board/task preview. Private content never enters it. */
export function publishPreview(input: PublishInput): SessionPreviewV1 {
  const ordered = orderEntries(input.source.events, input.through).filter((entry) => entry.type !== "user");
  const latest = ordered.slice(-PROPOSED_BUDGETS.previewEntries);
  const { entries, truncated } = trimAssistant(latest, PROPOSED_BUDGETS.previewAssistantChars);
  const attention = attentionFor(input.source.events, input.through, input.execution);
  const preview: SessionPreviewV1 = {
    version: 1,
    revision: input.revision,
    sessionId: input.sessionId,
    execution: input.execution,
    persona: input.source.persona,
    stage: input.source.stage,
    currentAction: currentActionFor(ordered, input.execution),
    activityAt: input.activityAt,
    startedAt: input.source.startedAt,
    previewEntries: entries,
    truncated: truncated || ordered.length > latest.length,
    attention,
    durationMs: input.durationMs,
    references: input.references,
  };
  const bytes = new TextEncoder().encode(JSON.stringify(preview)).length;
  if (bytes > PROPOSED_BUDGETS.previewPayloadBytes) throw new Error(`preview payload ${bytes} bytes exceeds ${PROPOSED_BUDGETS.previewPayloadBytes}`);
  return preview;
}

function attentionFor(events: RawEvent[], through: number, execution: ExecutionState): Attention | undefined {
  const visible = events.slice(0, through);
  if (execution === "failed") {
    const error = [...visible].reverse().find((event) => event.type === "session.error");
    return { id: `err:${error?.seq ?? 0}`, kind: "error", message: error && error.type === "session.error" ? error.message : "Session failed; details unavailable" };
  }
  if (execution === "awaiting_input") {
    const request = [...visible].reverse().find((event) => event.type === "permission.requested");
    return { id: `input:${request?.seq ?? 0}`, kind: "input", message: request && request.type === "permission.requested" ? request.message : "Input required; details unavailable" };
  }
  return undefined;
}

/** Publishes the selected-session detail (task view expansion). */
export function publishDetail(input: PublishInput & { workspace: string; taskId: string; reset?: boolean; baseSeq?: number }): DetailSnapshot {
  const ordered = orderEntries(input.source.events, input.through).filter((entry) => entry.type !== "user");
  const latest = ordered.slice(-PROPOSED_BUDGETS.expandedEntries);
  const { entries, truncated } = trimAssistant(latest, PROPOSED_BUDGETS.expandedAssistantChars);
  const detail: DetailEntry[] = entries.map((entry) => {
    if (entry.type !== "tool") return entry;
    const original = ordered.find((item) => item.id === entry.id);
    return { ...entry, input: original?.input?.slice(0, PROPOSED_BUDGETS.toolDetailChars), output: original?.output?.slice(0, PROPOSED_BUDGETS.toolDetailChars) };
  });
  return {
    workspace: input.workspace,
    taskId: input.taskId,
    taskPath: `/workspaces/${input.workspace}/tasks/${input.taskId}`,
    sessionId: input.sessionId,
    revision: input.revision,
    baseSeq: input.baseSeq ?? 0,
    throughSeq: input.source.events[Math.max(0, input.through - 1)]?.seq ?? 0,
    reset: Boolean(input.reset),
    entries: detail,
    truncated: truncated || ordered.length > latest.length,
  };
}

/** Full-session transcript entries (user messages permitted here only). */
export function fullTranscript(source: SessionSource, through: number): OrderedEntry[] {
  return orderEntries(source.events, through);
}
