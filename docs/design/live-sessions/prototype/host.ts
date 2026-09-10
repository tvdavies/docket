// Fixture host: an in-memory stand-in for the Docket-owned host.
//
// It owns the fake scheduler, routes snapshots by identity, and — the part
// the review made explicit — keeps three things apart:
//
//   incoming frame   — whatever the fixture "publisher" sends next. It may be
//                      a duplicate, older, unsupported or freshness-only.
//   accepted payload — the last plugin data whose revision was newer than the
//                      previously accepted one. Remounts, preference changes
//                      and metadata-only updates are served from THIS, never
//                      by re-reading a rejected frame.
//   metadata         — task fields, freshness, availability and the durable
//                      fallback. Delivered on every frame, even when the data
//                      revision is unchanged or rejected.
//
// Detail selection is view-scoped: one task view holds at most one selected
// session detail. Selecting another session revokes the previous one; leaving
// `available`, losing live data or retiring the instance revokes it too.
//
// It tracks instance generations so late results cannot reach a replacement,
// counts leases and cleanup, and never touches the network. It is not a
// production loader, SDK or registry.

import {
  PROPOSED_BUDGETS,
  type BoardTaskLike,
  type DetailRevocation,
  type DetailSnapshot,
  type DisplayPreferences,
  type DurableFallback,
  type ExecutionState,
  type SessionPreviewV1,
  type WidgetContext,
  type WidgetHelpers,
  type WidgetInstance,
  type WidgetLocation,
  type WidgetReference,
  type WidgetSnapshot,
} from "../contracts.proposed";
import { makeAdapter } from "./adapter";
import { customElementBody, standardBody, throwingBody } from "./components/bodies";
import { mountWrapper, type BodyFactory } from "./components/wrapper";
import { BOARD_TASKS, SERVICE_BASE, TASK, WIDGET_TYPE, WORKSPACE, sessionRef, type Frame, type Scenario, type SessionFixture } from "./fixtures";
import { publishDetail, publishPreview } from "./publisher";
import { safeHref } from "./links";

export type BodyChoice = "standard" | "custom-element";

export interface HostCounters {
  leasesAcquired: number;
  leasesReleased: number;
  leasesRevoked: number;
  leasesDeclined: number;
  activeLeases: number;
  instancesMounted: number;
  instancesRetired: number;
  bodyErrors: number;
  lateResultsIgnored: number;
  duplicatesIgnored: number;
  olderIgnored: number;
  snapshotsApplied: number;
  metadataApplied: number;
  ledgerAppends: number;
}

interface Registered {
  instance: WidgetInstance<SessionPreviewV1>;
  context: WidgetContext;
  abort: AbortController;
  generation: number;
  location: WidgetLocation;
  sessionId: string;
  taskId: string;
  detailThroughSeq: number;
}

/** The last plugin payload the host accepted for one identity. */
export interface AcceptedPayload {
  revision: number;
  dataVersion: number;
  through: number;
  execution: ExecutionState;
  durationMs?: number;
  references?: WidgetReference[];
}

export interface SessionState {
  fixture: SessionFixture;
  frameIndex: number;
  /** Highest revision ever seen (for logging); acceptance uses `accepted.revision`. */
  revision: number;
  accepted: AcceptedPayload | null;
  receivedAt: number | null;
  expiresAt: number | null;
  fallback?: DurableFallback;
  finalRevision?: number;
  taskId: string;
}

interface DetailSelection {
  key: string;
  sessionId: string;
  generation: number;
  listener: (detail: DetailSnapshot) => void;
  onRevoked?: (reason: DetailRevocation) => void;
}

const DEFAULT_PREFERENCES: DisplayPreferences = { theme: "light", density: "comfortable", reducedMotion: false };

export class FixtureHost {
  readonly counters: HostCounters = { leasesAcquired: 0, leasesReleased: 0, leasesRevoked: 0, leasesDeclined: 0, activeLeases: 0, instancesMounted: 0, instancesRetired: 0, bodyErrors: 0, lateResultsIgnored: 0, duplicatesIgnored: 0, olderIgnored: 0, snapshotsApplied: 0, metadataApplied: 0, ledgerAppends: 0 };
  readonly log: string[] = [];
  now = 0;
  preferences: DisplayPreferences = { ...DEFAULT_PREFERENCES };
  body: BodyChoice = "standard";
  private generation = 0;
  private readonly registered = new Map<string, Registered>();
  readonly sessions = new Map<string, SessionState>();
  /** Task records as the host currently knows them (task fields arrive in snapshots). */
  readonly tasks = new Map<string, BoardTaskLike>();
  private scenario: Scenario | null = null;
  private listeners = new Set<() => void>();
  private pendingLate: Array<{ generation: number; run(): void }> = [];
  /** One selected detail per task view (`activity:<taskId>`). */
  private readonly selections = new Map<string, DetailSelection>();

  onChange(listener: () => void): () => void { this.listeners.add(listener); return () => this.listeners.delete(listener); }
  private notify(): void { for (const listener of this.listeners) listener(); }

  loadScenario(scenario: Scenario): void {
    this.retireAll();
    this.scenario = scenario;
    this.sessions.clear();
    this.tasks.clear();
    for (const task of BOARD_TASKS) this.tasks.set(task.id, { ...task });
    this.now = 0;
    this.pendingLate = [];
    for (const fixture of scenario.sessions) {
      this.sessions.set(fixture.sessionId, { fixture, frameIndex: -1, revision: 0, accepted: null, receivedAt: null, expiresAt: null, taskId: TASK.id });
      this.counters.ledgerAppends += 1; // creation is the ONLY ledger event besides finalisation
      this.log.push(`ledger: session.created ${fixture.sessionId}`);
    }
    for (const fixture of scenario.sessions) this.step(fixture.sessionId);
    this.notify();
  }

  get currentScenario(): Scenario | null { return this.scenario; }

  frameOf(sessionId: string): Frame | null { const state = this.sessions.get(sessionId); return state ? state.fixture.frames[state.frameIndex] ?? null : null; }

  taskFor(taskId: string): BoardTaskLike | undefined { return this.tasks.get(taskId); }

  /** Advance every session one frame (deterministic). Returns false when all ended. */
  advance(): boolean {
    let advanced = false;
    for (const sessionId of this.sessions.keys()) advanced = this.step(sessionId) || advanced;
    this.notify();
    return advanced;
  }

  atEnd(): boolean { return [...this.sessions.values()].every((state) => state.frameIndex >= state.fixture.frames.length - 1); }

  private step(sessionId: string): boolean {
    const state = this.sessions.get(sessionId)!;
    if (state.frameIndex >= state.fixture.frames.length - 1) return false;
    state.frameIndex += 1;
    const frame = state.fixture.frames[state.frameIndex];
    this.now += frame.elapsedMs ?? 1000;
    // Metadata: task fields and preferences arrive regardless of data revision.
    if (frame.taskTitle) { const task = this.tasks.get(state.taskId); if (task) task.title = frame.taskTitle; }
    if (frame.preferences) this.preferences = { ...this.preferences, ...frame.preferences };
    if (frame.taskId && frame.taskId !== state.taskId) { state.taskId = frame.taskId; }
    if (frame.outcome && !state.fallback?.endedAt) {
      state.revision += 1; state.finalRevision = state.revision;
      state.fallback = this.fallbackFor(state, frame);
      this.counters.ledgerAppends += 1; this.log.push(`ledger: session.finalised ${sessionId} rev ${state.revision}`);
    } else if (!state.fallback) {
      state.fallback = this.fallbackFor(state, frame);
    }
    let dataAccepted = false;
    if (frame.rehydrating) {
      // The live cache was emptied: nothing is accepted until the owner republishes.
      state.accepted = null;
      this.log.push(`live cache empty for ${sessionId}; awaiting rehydration`);
    } else if (!frame.noReceipt) {
      const revision = frame.revision ?? (state.revision + 1);
      if (revision > state.revision) state.revision = revision;
      state.receivedAt = this.now; state.expiresAt = this.now + PROPOSED_BUDGETS.liveTtlMs;
      const current = state.accepted?.revision ?? 0;
      if (revision === current) { this.counters.duplicatesIgnored += 1; this.log.push(`ignored duplicate revision ${revision} for ${sessionId} (metadata still delivered)`); }
      else if (revision < current) { this.counters.olderIgnored += 1; this.log.push(`ignored older revision ${revision} for ${sessionId} (accepted stays ${current})`); }
      else { state.accepted = { revision, dataVersion: frame.dataVersion ?? 1, through: frame.through, execution: frame.execution, durationMs: frame.durationMs, references: frame.references }; dataAccepted = true; }
    }
    this.deliver(state, frame, dataAccepted);
    return true;
  }

  private fallbackFor(state: SessionState, frame: Frame): DurableFallback {
    const references: WidgetReference[] = [sessionRef(state.fixture.sessionId), ...(frame.references ?? state.fallback?.references.filter((ref) => ref.kind !== "session") ?? [])];
    return {
      version: 1, revision: state.revision,
      label: `${state.fixture.source.persona} · ${state.fixture.source.stage}`,
      statusLabel: frame.outcome ? ({ completed: "Completed", failed: "Failed", cancelled: "Cancelled" })[frame.outcome] : "Last known " + frame.execution.replace("_", " "),
      summary: frame.summary ?? state.fallback?.summary,
      startedAt: state.fixture.source.startedAt,
      endedAt: frame.outcome ? new Date(Date.parse(state.fixture.source.startedAt) + (frame.durationMs ?? 0)).toISOString() : undefined,
      references,
    };
  }

  snapshotFor(sessionId: string): WidgetSnapshot<SessionPreviewV1> | null {
    const state = this.sessions.get(sessionId); if (!state) return null;
    const frame = state.fixture.frames[state.frameIndex]; if (!frame) return null;
    return this.buildSnapshot(state, frame);
  }

  /** Builds a snapshot from the ACCEPTED payload plus current metadata. Never reads rejected frame content. */
  private buildSnapshot(state: SessionState, frame: Frame): WidgetSnapshot<SessionPreviewV1> {
    const accepted = state.accepted;
    const data = accepted ? {
      version: accepted.dataVersion,
      revision: accepted.revision,
      value: publishPreview({
        sessionId: state.fixture.sessionId, source: state.fixture.source, through: accepted.through, execution: accepted.execution, revision: accepted.revision,
        activityAt: new Date(Date.parse(state.fixture.source.startedAt) + this.now).toISOString(), durationMs: accepted.durationMs,
        references: [sessionRef(state.fixture.sessionId), ...(accepted.references ?? [])],
      }),
    } : undefined;
    const task = this.tasks.get(state.taskId) ?? { ...BOARD_TASKS[1], id: state.taskId };
    return {
      task: { ...task },
      data,
      freshness: { connection: frame.connection ?? "live", receivedAt: state.receivedAt, expiresAt: state.expiresAt, rehydrating: frame.rehydrating },
      availability: frame.availability ?? "available",
      fallback: state.fallback,
    };
  }

  private detailAvailable(state: SessionState): boolean {
    const frame = state.fixture.frames[state.frameIndex];
    return Boolean(state.accepted) && (frame?.availability ?? "available") === "available";
  }

  /** Deliver the accepted payload plus current metadata to every instance of this session. */
  private deliver(state: SessionState, frame: Frame, dataChanged: boolean): void {
    const snapshot = this.buildSnapshot(state, frame);
    // Host-side enforcement: no live detail selection survives unavailability or an emptied cache.
    if (!this.detailAvailable(state)) this.revokeSession(state.fixture.sessionId, "unavailable");
    for (const entry of this.registered.values()) {
      if (entry.sessionId !== state.fixture.sessionId) continue;
      if (entry.taskId !== state.taskId) {
        // Simulate a detail response that was already in flight for the old
        // identity: it must be dropped by generation/abort checks, never applied.
        this.queueLateDetail(entry, state);
        this.retire(entry, "identity changed");
        continue;
      }
      const context = this.contextFor(entry);
      entry.context = context;
      if (dataChanged) this.counters.snapshotsApplied += 1; else this.counters.metadataApplied += 1;
      entry.instance.update(snapshot, context);
      if (dataChanged) this.publishDetailFor(entry, frame);
    }
    this.notify();
  }

  private queueLateDetail(entry: Registered, state: SessionState): void {
    const selection = this.selections.get(this.viewKey(entry));
    if (!selection || selection.key !== this.key(entry)) return;
    const accepted = state.accepted; if (!accepted) return;
    const detail = publishDetail({ workspace: WORKSPACE, taskId: entry.taskId, sessionId: entry.sessionId, source: state.fixture.source, through: accepted.through, execution: accepted.execution, revision: accepted.revision, reset: true, baseSeq: 0 });
    const generation = entry.generation;
    const listener = selection.listener;
    this.pendingLate.push({ generation, run: () => {
      const current = this.registered.get(this.key(entry));
      if (!current || current.generation !== generation || entry.abort.signal.aborted) { this.counters.lateResultsIgnored += 1; this.log.push(`ignored late detail for retired generation ${generation}`); return; }
      listener(detail);
    } });
  }

  private publishDetailFor(entry: Registered, frame: Frame, forceReset = false): void {
    const selection = this.selections.get(this.viewKey(entry));
    if (!selection || selection.key !== this.key(entry)) return;
    const state = this.sessions.get(entry.sessionId)!;
    const accepted = state.accepted; if (!accepted) return;
    const baseSeq = frame.detailGap ? entry.detailThroughSeq + 2 : entry.detailThroughSeq;
    const detail = publishDetail({ workspace: WORKSPACE, taskId: state.taskId, sessionId: entry.sessionId, source: state.fixture.source, through: accepted.through, execution: accepted.execution, revision: accepted.revision, reset: forceReset || frame.reset || entry.detailThroughSeq === 0, baseSeq });
    const generation = entry.generation;
    const current = this.registered.get(this.key(entry));
    if (!current || current.generation !== generation || current.abort.signal.aborted || this.selections.get(this.viewKey(entry)) !== selection) { this.counters.lateResultsIgnored += 1; this.log.push(`ignored late detail for retired generation ${generation}`); return; }
    selection.listener(detail);
    if (detail.reset || detail.baseSeq === current.detailThroughSeq) current.detailThroughSeq = detail.throughSeq;
  }

  /** Flush simulated slow responses; retired generations are ignored. */
  flushLate(): void { const pending = this.pendingLate; this.pendingLate = []; for (const item of pending) item.run(); this.notify(); }

  private key(entry: { location: WidgetLocation; sessionId: string }): string { return `${entry.location}:${entry.sessionId}`; }
  private viewKey(entry: { location: WidgetLocation; taskId: string }): string { return `${entry.location}:${entry.taskId}`; }

  // --- Detail selection (view-scoped) ---------------------------------------

  private dropSelection(selection: DetailSelection, how: "released" | DetailRevocation): void {
    const viewKey = [...this.selections.entries()].find(([, value]) => value === selection)?.[0];
    if (!viewKey) return;
    this.selections.delete(viewKey);
    this.counters.activeLeases -= 1;
    if (how === "released") { this.counters.leasesReleased += 1; this.log.push(`lease released ${selection.key}`); }
    else { this.counters.leasesRevoked += 1; this.log.push(`lease revoked ${selection.key} (${how})`); selection.onRevoked?.(how); }
    queueMicrotask(() => this.notify());
  }

  private revokeSession(sessionId: string, reason: DetailRevocation): void {
    for (const selection of [...this.selections.values()]) if (selection.sessionId === sessionId) this.dropSelection(selection, reason);
  }

  private contextFor(entry: Registered): WidgetContext {
    const key = this.key(entry);
    const viewKey = this.viewKey(entry);
    const helpers: WidgetHelpers = {
      refreshTask: () => { this.log.push(`refreshTask() from ${key}`); },
      hrefFor: (ref) => safeHref(ref),
      requestDetail: entry.location === "activity" ? (listener, onRevoked) => {
        const current = this.registered.get(key);
        if (!current || current.generation !== entry.generation || entry.abort.signal.aborted) return null;
        const state = this.sessions.get(entry.sessionId)!;
        if (!this.detailAvailable(state)) { this.counters.leasesDeclined += 1; this.log.push(`detail declined for ${key}: unavailable`); queueMicrotask(() => this.notify()); return null; }
        const existing = this.selections.get(viewKey);
        if (existing?.key === key) throw new Error("one detail selection per instance");
        if (existing) this.dropSelection(existing, "reselected"); // at most PROPOSED_BUDGETS.detailLeasesPerTaskView per view
        const selection: DetailSelection = { key, sessionId: entry.sessionId, generation: entry.generation, listener, onRevoked };
        this.selections.set(viewKey, selection);
        this.counters.leasesAcquired += 1; this.counters.activeLeases += 1; this.log.push(`lease acquired ${key} (view ${viewKey}, ${this.selections.size}/${PROPOSED_BUDGETS.detailLeasesPerTaskView})`);
        queueMicrotask(() => this.notify());
        // Deliver the current window immediately as a reset, unless the owner is
        // "slow" (detail-gap fixture): then the snapshot arrives on the next frame.
        const frame = state.fixture.frames[state.frameIndex];
        entry.detailThroughSeq = 0;
        if (!frame.detailGap) this.publishDetailFor(entry, frame, true);
        else this.log.push(`owner snapshot requested for ${key}; arrives next frame`);
        return () => { if (this.selections.get(viewKey) === selection) this.dropSelection(selection, "released"); };
      } : undefined,
    };
    return {
      identity: { workspace: WORKSPACE, taskId: entry.taskId, widgetType: WIDGET_TYPE, instanceId: entry.sessionId },
      location: entry.location, serviceBase: SERVICE_BASE, preferences: { ...this.preferences }, signal: entry.abort.signal, helpers,
    };
  }

  /** Mount a widget for (location, session). Identity switches retire the old instance. */
  mount(slot: HTMLElement, location: WidgetLocation, sessionId: string): void {
    const state = this.sessions.get(sessionId); if (!state) return;
    const key = `${location}:${sessionId}`;
    const existing = this.registered.get(key);
    if (existing) { if (existing.taskId === state.taskId && existing.context.identity.workspace === WORKSPACE) return; this.retire(existing, "identity changed"); }
    const abort = new AbortController();
    const entry: Registered = { instance: null as unknown as WidgetInstance<SessionPreviewV1>, context: null as unknown as WidgetContext, abort, generation: ++this.generation, location, sessionId, taskId: state.taskId, detailThroughSeq: 0 };
    entry.context = this.contextFor(entry);
    const frame = state.fixture.frames[state.frameIndex];
    const factory: BodyFactory = frame?.bodyThrows !== undefined || state.fixture.frames.some((f) => f.bodyThrows)
      ? throwingBody(() => Boolean(this.frameOf(sessionId)?.bodyThrows))
      : this.body === "standard" ? standardBody : customElementBody;
    const adapter = makeAdapter(this.body, (id) => this.sessions.get(id)?.fallback?.summary);
    entry.instance = mountWrapper(slot, entry.context, adapter, factory, {
      now: () => this.now,
      onBodyError: (error) => { this.counters.bodyErrors += 1; this.log.push(`body error: ${(error as Error)?.message ?? error}`); this.notify(); },
      onAnnounce: (message) => this.log.push(`announce: ${message}`),
    });
    this.registered.set(key, entry);
    this.counters.instancesMounted += 1;
    // Served from the accepted payload: a remount never replays a rejected frame.
    entry.instance.update(this.buildSnapshot(state, frame), entry.context);
    this.notify();
  }

  private retire(entry: Registered, reason: string): void {
    const key = this.key(entry);
    if (this.registered.get(key) !== entry) return;
    this.registered.delete(key);
    entry.abort.abort();
    try { entry.instance.destroy(); } catch { /* idempotent */ }
    const selection = this.selections.get(this.viewKey(entry));
    if (selection && selection.key === key) this.dropSelection(selection, "retired");
    this.counters.instancesRetired += 1;
    this.log.push(`retired ${key} (${reason})`);
  }

  unmount(location: WidgetLocation, sessionId: string): void {
    const entry = this.registered.get(`${location}:${sessionId}`);
    if (entry) this.retire(entry, "unmounted");
    this.notify();
  }

  retireAll(): void { for (const entry of [...this.registered.values()]) this.retire(entry, "scenario change"); }

  /** Re-deliver current context (preferences) to every instance without remount, from the accepted payload. */
  applyPreferences(preferences: Partial<DisplayPreferences>): void {
    this.preferences = { ...this.preferences, ...preferences };
    for (const entry of this.registered.values()) {
      const state = this.sessions.get(entry.sessionId)!; const frame = state.fixture.frames[state.frameIndex];
      entry.context = this.contextFor(entry);
      this.counters.metadataApplied += 1;
      entry.instance.update(this.buildSnapshot(state, frame), entry.context);
    }
    this.notify();
  }

  /** Switch body kind: remounts the body under the same wrapper identity semantics (demo control). */
  setBody(body: BodyChoice, remount: (host: FixtureHost) => void): void { if (this.body === body) return; this.body = body; this.retireAll(); remount(this); }

  mountedKeys(): string[] { return [...this.registered.keys()]; }
  selectedDetailKeys(): string[] { return [...this.selections.values()].map((selection) => selection.key); }
}
