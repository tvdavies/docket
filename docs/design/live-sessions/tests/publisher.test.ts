// Unit checks for the fixture publisher: bounds and filtering happen BEFORE
// publication. Run: bun test tests/publisher.test.ts

import { describe, expect, test } from "bun:test";
import { PROPOSED_BUDGETS } from "../contracts.proposed";
import { chooseBoardSession, presentSession } from "../prototype/adapter";
import { LONG_SESSION_ENTRIES, SCENARIOS, sessionRef } from "../prototype/fixtures";
import { safeHref } from "../prototype/links";
import { currentActionFor, fullTranscript, orderEntries, publishDetail, publishPreview, type RawEvent, type SessionSource } from "../prototype/publisher";

const live = SCENARIOS.find((scenario) => scenario.id === "live")!.sessions[0];
const source = live.source;

describe("ordering", () => {
  test("tool updates merge into the tool's original position", () => {
    const entries = orderEntries(source.events, 12);
    const types = entries.map((entry) => entry.type);
    expect(types).toEqual(["user", "assistant", "tool", "assistant", "tool", "assistant", "assistant"]);
    const read = entries.find((entry) => entry.toolCallId === "t1")!;
    expect(read.status).toBe("completed");
    expect(read.seq).toBe(5); // original seq retained
    expect(read.durationMs).toBe(1200);
  });
  test("assistant deltas with the same id grow one entry", () => {
    const three = orderEntries(source.events, 3).filter((entry) => entry.type === "assistant");
    const four = orderEntries(source.events, 4).filter((entry) => entry.type === "assistant");
    expect(three).toHaveLength(1); expect(four).toHaveLength(1);
    expect(four[0].id).toBe(three[0].id);
    expect(four[0].text!.length).toBeGreaterThan(three[0].text!.length);
  });
  test("a tool.updated for an unknown id never invents a row", () => {
    const entries = orderEntries([{ seq: 1, type: "tool.updated", toolCallId: "ghost", status: "completed" }], 1);
    expect(entries).toEqual([]);
  });
});

describe("filtering before publication", () => {
  const preview = publishPreview({ sessionId: "demo-s01", source, through: 12, execution: "running", revision: 3 });
  const json = JSON.stringify(preview);
  test("no user prompt, reasoning, private paths or raw arguments in the preview payload", () => {
    expect(json).not.toContain("SECRET-MARKER");
    expect(json).not.toContain("PREVIEW-EXCLUDED-USER-MARKER");
    expect(json).not.toContain("/home/");
    expect(json).not.toContain("privateInput");
    expect(preview.previewEntries.every((entry) => entry.type !== ("user" as string))).toBe(true);
  });
  test("detail carries only the redacted input/output, still no private markers", () => {
    const detail = publishDetail({ workspace: "demo", taskId: "DEMO-0042", sessionId: "demo-s01", source, through: 12, execution: "running", revision: 3 });
    const detailJson = JSON.stringify(detail);
    expect(detailJson).not.toContain("SECRET-MARKER");
    expect(detailJson).not.toContain("PREVIEW-EXCLUDED-USER-MARKER");
    expect(detailJson).toContain("path: web/src/sessionProjection.ts");
  });
});

describe("bounds", () => {
  test("preview holds at most four entries", () => {
    const preview = publishPreview({ sessionId: "s", source, through: 12, execution: "running", revision: 1 });
    expect(preview.previewEntries.length).toBeLessThanOrEqual(PROPOSED_BUDGETS.previewEntries);
    expect(preview.truncated).toBe(true);
  });
  test("preview assistant text is capped at 600 characters, keeping the latest text", () => {
    const long: RawEvent[] = Array.from({ length: 4 }, (_, index) => ({ seq: index + 1, type: "content.delta" as const, id: `a${index}`, text: `${index}`.repeat(400) }));
    const preview = publishPreview({ sessionId: "s", source: { ...source, events: long }, through: 4, execution: "running", revision: 1 });
    const total = preview.previewEntries.reduce((sum, entry) => sum + (entry.type === "assistant" ? entry.text.length : 0), 0);
    expect(total).toBeLessThanOrEqual(PROPOSED_BUDGETS.previewAssistantChars + 1); // + ellipsis
    expect(preview.truncated).toBe(true);
    expect(preview.previewEntries.at(-1)!.type === "assistant" && (preview.previewEntries.at(-1) as { text: string }).text.endsWith("3")).toBe(true);
  });
  test("expanded detail is capped at 12 entries / 1,600 characters / 2,000-char tool detail", () => {
    const events: RawEvent[] = [];
    for (let index = 0; index < 20; index += 1) {
      events.push({ seq: events.length + 1, type: "tool.started", toolCallId: `t${index}`, label: `Tool ${index}`, input: "x".repeat(5000) });
      events.push({ seq: events.length + 1, type: "tool.updated", toolCallId: `t${index}`, status: "completed", output: "y".repeat(5000) });
      events.push({ seq: events.length + 1, type: "content.delta", id: `a${index}`, text: "z".repeat(300) });
    }
    const detail = publishDetail({ workspace: "demo", taskId: "DEMO-0042", sessionId: "s", source: { ...source, events }, through: events.length, execution: "running", revision: 1 });
    expect(detail.entries.length).toBeLessThanOrEqual(PROPOSED_BUDGETS.expandedEntries);
    const chars = detail.entries.reduce((sum, entry) => sum + (entry.type === "assistant" ? entry.text.length : 0), 0);
    expect(chars).toBeLessThanOrEqual(PROPOSED_BUDGETS.expandedAssistantChars + 1);
    for (const entry of detail.entries) if (entry.type === "tool") { expect((entry.input ?? "").length).toBeLessThanOrEqual(PROPOSED_BUDGETS.toolDetailChars); expect((entry.output ?? "").length).toBeLessThanOrEqual(PROPOSED_BUDGETS.toolDetailChars); }
  });
  test("preview payload stays under 16 KiB for every fixture frame", () => {
    for (const scenario of SCENARIOS) for (const session of scenario.sessions) for (const frame of session.frames) {
      const preview = publishPreview({ sessionId: session.sessionId, source: session.source, through: frame.through, execution: frame.execution, revision: 1, references: [sessionRef(session.sessionId), ...(frame.references ?? [])] });
      expect(new TextEncoder().encode(JSON.stringify(preview)).length).toBeLessThanOrEqual(PROPOSED_BUDGETS.previewPayloadBytes);
      expect((preview.currentAction ?? "").length).toBeLessThanOrEqual(PROPOSED_BUDGETS.currentActionChars);
    }
  });
  test("an oversized payload is rejected rather than published", () => {
    const events: RawEvent[] = [{ seq: 1, type: "tool.started", toolCallId: "t", label: "L".repeat(20_000) }];
    expect(() => publishPreview({ sessionId: "s", source: { ...source, events }, through: 1, execution: "running", revision: 1 })).toThrow(/exceeds/);
  });
});

describe("state and action", () => {
  test("current action prefers a running tool, then latest text, then an honest placeholder", () => {
    expect(currentActionFor(orderEntries(source.events, 9), "running")).toBe("Inspect · task navigation");
    expect(currentActionFor(orderEntries(source.events, 8), "running")).toContain("I'll keep the same ordered view");
    expect(currentActionFor([], "running")).toBe("Running; no activity yet");
    expect(currentActionFor([], "queued")).toBeUndefined();
  });
  test("attention is published for failed and awaiting input, with honest fallbacks", () => {
    const failed = publishPreview({ sessionId: "s", source: { ...source, events: [{ seq: 1, type: "session.error", message: "boom" }] }, through: 1, execution: "failed", revision: 1 });
    expect(failed.attention).toEqual({ id: "err:1", kind: "error", message: "boom" });
    const blocked = publishPreview({ sessionId: "s", source: { ...source, events: [] }, through: 0, execution: "awaiting_input", revision: 1 });
    expect(blocked.attention?.message).toBe("Input required; details unavailable");
    const running = publishPreview({ sessionId: "s", source, through: 12, execution: "running", revision: 1 });
    expect(running.attention).toBeUndefined();
  });
  test("a failed tool alone does not make the session failed", () => {
    const events: RawEvent[] = [{ seq: 1, type: "tool.started", toolCallId: "t", label: "Read" }, { seq: 2, type: "tool.updated", toolCallId: "t", status: "failed" }];
    const preview = publishPreview({ sessionId: "s", source: { ...source, events }, through: 2, execution: "running", revision: 1 });
    expect(preview.execution).toBe("running");
    expect(preview.attention).toBeUndefined();
  });
  test("the adapter never presents an unknown version or enum as running", () => {
    expect(presentSession({ version: 2, revision: 1, value: {} as never })).toBeNull();
    const preview = publishPreview({ sessionId: "s", source, through: 3, execution: "running", revision: 1 });
    expect(presentSession({ version: 1, revision: 1, value: { ...preview, execution: "weird" as never } })).toBeNull();
    expect(presentSession({ version: 1, revision: 1, value: preview })?.status.text).toBe("Running");
  });
  test("board picks awaiting input, then failed, then active, then latest terminal", () => {
    const pick = chooseBoardSession([
      { execution: "completed", createdAt: "2026-01-03", sessionId: "c" },
      { execution: "running", createdAt: "2026-01-02", sessionId: "b" },
      { execution: "awaiting_input", createdAt: "2026-01-01", sessionId: "a" },
    ]);
    expect(pick?.sessionId).toBe("a");
    expect(chooseBoardSession([{ execution: "completed", createdAt: "2026-01-01", sessionId: "old" }, { execution: "cancelled", createdAt: "2026-01-02", sessionId: "new" }])?.sessionId).toBe("new");
  });
});

describe("links", () => {
  test("only same-origin plugin session/task paths and https references are rendered", () => {
    expect(safeHref({ kind: "session", label: "s", url: "/plugins/dispatch/sessions/demo-s01" })).toBe("/plugins/dispatch/sessions/demo-s01");
    expect(safeHref({ kind: "session", label: "s", url: "http://localhost:7464/sessions/x" })).toBeNull();
    expect(safeHref({ kind: "session", label: "s", url: "javascript:alert(1)" })).toBeNull();
    expect(safeHref({ kind: "task", label: "t", url: "/workspaces/demo/tasks/DEMO-0042" })).toBe("/workspaces/demo/tasks/DEMO-0042");
    expect(safeHref({ kind: "plan", label: "p", url: "https://example.com/plans/demo" })).toBe("https://example.com/plans/demo");
    expect(safeHref({ kind: "plan", label: "p", url: "file:///etc/passwd" })).toBeNull();
    expect(safeHref({ kind: "pr", label: "p", url: "http://example.com" })).toBeNull();
  });
});

describe("fixtures", () => {
  test("every scenario has at least one frame and a deterministic session id", () => {
    for (const scenario of SCENARIOS) {
      expect(scenario.sessions.length).toBeGreaterThan(0);
      for (const session of scenario.sessions) { expect(session.frames.length).toBeGreaterThan(0); expect(session.sessionId).toMatch(/^demo-s\d+$/); }
    }
  });
  test("the scenario set covers every required execution/freshness/availability state", () => {
    const executions = new Set<string>(); const connections = new Set<string>(); const availability = new Set<string>();
    for (const scenario of SCENARIOS) for (const session of scenario.sessions) for (const frame of session.frames) { executions.add(frame.execution); connections.add(frame.connection ?? "live"); availability.add(frame.availability ?? "available"); }
    for (const state of ["queued", "starting", "running", "awaiting_input", "completed", "failed", "cancelled"]) expect(executions.has(state)).toBe(true);
    for (const state of ["connecting", "live", "disconnected"]) expect(connections.has(state)).toBe(true);
    for (const state of ["available", "missing_service", "plugin_removed"]) expect(availability.has(state)).toBe(true);
  });
  test("the long-session fixture exceeds the full-session window and needs more than two output chunks", () => {
    const long = SCENARIOS.find((scenario) => scenario.id === "long-session")!.sessions[0];
    const first = long.frames[0];
    const entries = fullTranscript(long.source, first.through);
    expect(entries.length).toBe(LONG_SESSION_ENTRIES + 1); // 205 assistant entries + 1 tool
    expect(entries.length).toBeGreaterThan(PROPOSED_BUDGETS.fullSessionMountedEntries);
    const tool = entries.find((entry) => entry.type === "tool")!;
    expect(tool.output!.length).toBe(5_000);
    expect(tool.output!.length).toBeGreaterThan(2 * PROPOSED_BUDGETS.fullSessionOutputChunkChars);
    // Its preview/detail payloads stay bounded even for a long session.
    const preview = publishPreview({ sessionId: long.sessionId, source: long.source, through: long.frames.at(-1)!.through, execution: "running", revision: 4 });
    expect(preview.previewEntries.length).toBeLessThanOrEqual(PROPOSED_BUDGETS.previewEntries);
    expect(JSON.stringify(preview)).not.toContain("line 100:");
    const detail = publishDetail({ workspace: "demo", taskId: "DEMO-0042", sessionId: long.sessionId, source: long.source, through: long.frames.at(-1)!.through, execution: "running", revision: 4 });
    expect(detail.entries.length).toBeLessThanOrEqual(PROPOSED_BUDGETS.expandedEntries);
    const detailTool = detail.entries.find((entry) => entry.type === "tool") as { output?: string } | undefined;
    expect((detailTool?.output ?? "").length).toBeLessThanOrEqual(PROPOSED_BUDGETS.toolDetailChars);
  });
});

void (null as unknown as SessionSource);
