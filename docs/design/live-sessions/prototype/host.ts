// Fixture host: an in-memory stand-in for the Docket-owned host.
//
// It owns the fake scheduler, routes/deduplicates snapshots by identity,
// tracks instance generations so late results cannot reach a replacement,
// counts detail leases and cleanup, and never touches the network.
// It is not a production loader, SDK or registry.

import {
  PROPOSED_BUDGETS,
  type DetailSnapshot,
  type DisplayPreferences,
  type DurableFallback,
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
import { SERVICE_BASE, TASK, WIDGET_TYPE, WORKSPACE, sessionRef, type Frame, type Scenario, type SessionFixture } from "./fixtures";
import { publishDetail, publishPreview } from "./publisher";
import { safeHref } from "./links";

export type BodyChoice = "standard" | "custom-element";

export interface HostCounters {
  leasesAcquired: number;
  leasesReleased: number;
  activeLeases: number;
  instancesMounted: number;
  instancesRetired: number;
  bodyErrors: number;
  lateResultsIgnored: number;
  duplicatesIgnored: number;
  olderIgnored: number;
  snapshotsApplied: number;
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
  lastRevision: number;
  detailThroughSeq: number;
}

export interface SessionState {
  fixture: SessionFixture;
  frameIndex: number;
  revision: number;
  receivedAt: number | null;
  expiresAt: number | null;
  fallback?: DurableFallback;
  finalRevision?: number;
  taskTitle: string;
  taskId: string;
}

const DEFAULT_PREFERENCES: DisplayPreferences = { theme: "light", density: "comfortable", reducedMotion: false };

export class FixtureHost {
  readonly counters: HostCounters = { leasesAcquired: 0, leasesReleased: 0, activeLeases: 0, instancesMounted: 0, instancesRetired: 0, bodyErrors: 0, lateResultsIgnored: 0, duplicatesIgnored: 0, olderIgnored: 0, snapshotsApplied: 0, ledgerAppends: 0 };
  readonly log: string[] = [];
  now = 0;
  preferences: DisplayPreferences = { ...DEFAULT_PREFERENCES };
  body: BodyChoice = "standard";
  private generation = 0;
  private readonly registered = new Map<string, Registered>();
  readonly sessions = new Map<string, SessionState>();
  private scenario: Scenario | null = null;
  private listeners = new Set<() => void>();
  private pendingLate: Array<{ generation: number; run(): void }> = [];

  onChange(listener: () => void): () => void { this.listeners.add(listener); return () => this.listeners.delete(listener); }
  private notify(): void { for (const listener of this.listeners) listener(); }

  loadScenario(scenario: Scenario): void {
    this.retireAll();
    this.scenario = scenario;
    this.sessions.clear();
    this.now = 0;
    this.pendingLate = [];
    for (const fixture of scenario.sessions) {
      this.sessions.set(fixture.sessionId, { fixture, frameIndex: -1, revision: 0, receivedAt: null, expiresAt: null, taskTitle: TASK.title, taskId: TASK.id });
      this.counters.ledgerAppends += 1; // creation is the ONLY ledger event besides finalisation
      this.log.push(`ledger: session.created ${fixture.sessionId}`);
    }
    for (const fixture of scenario.sessions) this.step(fixture.sessionId);
    this.notify();
  }

  get currentScenario(): Scenario | null { return this.scenario; }

  frameOf(sessionId: string): Frame | null { const state = this.sessions.get(sessionId); return state ? state.fixture.frames[state.frameIndex] ?? null : null; }

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
    if (frame.taskTitle) state.taskTitle = frame.taskTitle;
    if (frame.preferences) this.preferences = { ...this.preferences, ...frame.preferences };
    if (frame.taskId && frame.taskId !== state.taskId) { state.taskId = frame.taskId; }
    if (frame.outcome && !state.fallback?.endedAt) {
      state.revision += 1; state.finalRevision = state.revision;
      state.fallback = this.fallbackFor(state, frame);
      this.counters.ledgerAppends += 1; this.log.push(`ledger: session.finalised ${sessionId} rev ${state.revision}`);
    } else if (!state.fallback) {
      state.fallback = this.fallbackFor(state, frame);
    }
    if (!frame.noReceipt) {
      const revision = frame.revision ?? (state.revision + 1);
      if (revision > state.revision) state.revision = revision; // the host's view tracks the highest revision; older ones are still delivered so instances can ignore them
      state.receivedAt = this.now; state.expiresAt = this.now + PROPOSED_BUDGETS.liveTtlMs;
      this.deliver(state, frame, revision);
    } else {
      this.deliver(state, frame, state.revision, true);
    }
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
    return this.buildSnapshot(state, frame, state.revision);
  }

  private buildSnapshot(state: SessionState, frame: Frame, revision: number): WidgetSnapshot<SessionPreviewV1> {
    const preview = publishPreview({
      sessionId: state.fixture.sessionId, source: state.fixture.source, through: frame.through, execution: frame.execution, revision,
      activityAt: new Date(Date.parse(state.fixture.source.startedAt) + this.now).toISOString(), durationMs: frame.durationMs,
      references: [sessionRef(state.fixture.sessionId), ...(frame.references ?? [])],
    });
    const dataVersion = frame.dataVersion ?? 1;
    return {
      task: { ...TASK, id: state.taskId, title: state.taskTitle },
      data: frame.rehydrating ? undefined : { version: dataVersion, revision, value: preview },
      freshness: { connection: frame.connection ?? "live", receivedAt: state.receivedAt, expiresAt: state.expiresAt, rehydrating: frame.rehydrating },
      availability: frame.availability ?? "available",
      fallback: state.fallback,
    };
  }

  private deliver(state: SessionState, frame: Frame, revision: number, freshnessOnly = false): void {
    const snapshot = this.buildSnapshot(state, frame, revision);
    for (const entry of this.registered.values()) {
      if (entry.sessionId !== state.fixture.sessionId) continue;
      if (entry.taskId !== state.taskId) {
        // Simulate a detail response that was already in flight for the old
        // identity: it must be dropped by generation/abort checks, never applied.
        this.queueLateDetail(entry, frame, revision);
        this.retire(entry, "identity changed");
        continue;
      }
      if (!freshnessOnly && snapshot.data) {
        if (snapshot.data.revision === entry.lastRevision) { this.counters.duplicatesIgnored += 1; this.log.push(`ignored duplicate revision ${revision} for ${entry.location}`); continue; }
        if (snapshot.data.revision < entry.lastRevision) { this.counters.olderIgnored += 1; this.log.push(`ignored older revision ${revision} for ${entry.location}`); continue; }
        entry.lastRevision = snapshot.data.revision;
      }
      const context = this.contextFor(entry);
      entry.context = context;
      this.counters.snapshotsApplied += 1;
      entry.instance.update(snapshot, context);
      if (frame.detailGap || frame.reset) this.publishDetailFor(entry, frame, revision);
      else this.publishDetailFor(entry, frame, revision);
    }
    this.notify();
  }

  private detailListeners = new Map<string, (detail: DetailSnapshot) => void>();

  private queueLateDetail(entry: Registered, frame: Frame, revision: number): void {
    const listener = this.detailListeners.get(this.key(entry)) ?? (() => undefined);
    const state = this.sessions.get(entry.sessionId)!;
    const detail = publishDetail({ workspace: WORKSPACE, taskId: entry.taskId, sessionId: entry.sessionId, source: state.fixture.source, through: frame.through, execution: frame.execution, revision, reset: true, baseSeq: 0 });
    const generation = entry.generation;
    this.pendingLate.push({ generation, run: () => {
      const current = this.registered.get(this.key(entry));
      if (!current || current.generation !== generation || entry.abort.signal.aborted) { this.counters.lateResultsIgnored += 1; this.log.push(`ignored late detail for retired generation ${generation}`); return; }
      listener(detail);
    } });
  }

  private publishDetailFor(entry: Registered, frame: Frame, revision: number): void {
    const listener = this.detailListeners.get(this.key(entry)); if (!listener) return;
    const state = this.sessions.get(entry.sessionId)!;
    const baseSeq = frame.detailGap ? entry.detailThroughSeq + 2 : entry.detailThroughSeq;
    const detail = publishDetail({ workspace: WORKSPACE, taskId: state.taskId, sessionId: entry.sessionId, source: state.fixture.source, through: frame.through, execution: frame.execution, revision, reset: frame.reset || entry.detailThroughSeq === 0, baseSeq });
    const generation = entry.generation;
    const run = () => {
      const current = this.registered.get(this.key(entry));
      if (!current || current.generation !== generation || current.abort.signal.aborted) { this.counters.lateResultsIgnored += 1; this.log.push(`ignored late detail for retired generation ${generation}`); return; }
      listener(detail);
      if (detail.reset || detail.baseSeq === current.detailThroughSeq) current.detailThroughSeq = detail.throughSeq;
    };
    run();
  }

  /** Flush simulated slow responses; retired generations are ignored. */
  flushLate(): void { const pending = this.pendingLate; this.pendingLate = []; for (const item of pending) item.run(); this.notify(); }

  private key(entry: { location: WidgetLocation; sessionId: string }): string { return `${entry.location}:${entry.sessionId}`; }

  private contextFor(entry: Registered): WidgetContext {
    const key = this.key(entry);
    const helpers: WidgetHelpers = {
      refreshTask: () => { this.log.push(`refreshTask() from ${key}`); },
      hrefFor: (ref) => safeHref(ref),
      requestDetail: entry.location === "activity" ? (listener) => {
        if (this.detailListeners.has(key)) throw new Error("one detail lease per instance");
        this.detailListeners.set(key, listener);
        this.counters.leasesAcquired += 1; this.counters.activeLeases += 1; this.log.push(`lease acquired ${key}`);
        queueMicrotask(() => this.notify());
        // Deliver the current window immediately as a reset, unless the owner is
        // "slow" (detail-gap fixture): then the snapshot arrives on the next frame.
        const state = this.sessions.get(entry.sessionId)!; const frame = state.fixture.frames[state.frameIndex];
        entry.detailThroughSeq = 0;
        if (!frame.detailGap) this.publishDetailFor(entry, { ...frame, reset: true }, state.revision);
        else this.log.push(`owner snapshot requested for ${key}; arrives next frame`);
        return () => { if (this.detailListeners.delete(key)) { this.counters.leasesReleased += 1; this.counters.activeLeases -= 1; this.log.push(`lease released ${key}`); queueMicrotask(() => this.notify()); } };
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
    const entry: Registered = { instance: null as unknown as WidgetInstance<SessionPreviewV1>, context: null as unknown as WidgetContext, abort, generation: ++this.generation, location, sessionId, taskId: state.taskId, lastRevision: 0, detailThroughSeq: 0 };
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
    const snapshot = this.buildSnapshot(state, frame, state.revision);
    entry.lastRevision = snapshot.data?.revision ?? 0;
    entry.instance.update(snapshot, entry.context);
    this.notify();
  }

  private retire(entry: Registered, reason: string): void {
    const key = this.key(entry);
    if (this.registered.get(key) !== entry) return;
    this.registered.delete(key);
    entry.abort.abort();
    try { entry.instance.destroy(); } catch { /* idempotent */ }
    const release = this.detailListeners.get(key);
    if (release) { this.detailListeners.delete(key); this.counters.leasesReleased += 1; this.counters.activeLeases -= 1; this.log.push(`lease released ${key} (retired)`); }
    this.counters.instancesRetired += 1;
    this.log.push(`retired ${key} (${reason})`);
  }

  unmount(location: WidgetLocation, sessionId: string): void {
    const entry = this.registered.get(`${location}:${sessionId}`);
    if (entry) this.retire(entry, "unmounted");
    this.notify();
  }

  retireAll(): void { for (const entry of [...this.registered.values()]) this.retire(entry, "scenario change"); }

  /** Re-deliver current context (preferences) to every instance without remount. */
  applyPreferences(preferences: Partial<DisplayPreferences>): void {
    this.preferences = { ...this.preferences, ...preferences };
    for (const entry of this.registered.values()) {
      const state = this.sessions.get(entry.sessionId)!; const frame = state.fixture.frames[state.frameIndex];
      entry.context = this.contextFor(entry);
      entry.instance.update(this.buildSnapshot(state, frame, state.revision), entry.context);
    }
    this.notify();
  }

  /** Switch body kind: remounts the body under the same wrapper identity semantics (demo control). */
  setBody(body: BodyChoice, remount: (host: FixtureHost) => void): void { if (this.body === body) return; this.body = body; this.retireAll(); remount(this); }

  mountedKeys(): string[] { return [...this.registered.keys()]; }
}

