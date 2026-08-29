import { useSyncExternalStore } from 'react';
import type { BoardTask, CreateTaskInput, LivePayload, StreamConfig, StreamInit, StreamPatch, TaskPatch } from '../types';

export type ConnectionState = 'idle' | 'connecting' | 'open' | 'reconnecting' | 'closed';
export type PendingMutation = {
  id: string;
  kind: 'patch' | 'create';
  taskId: string;
  patch?: Partial<BoardTask>;
  requestPatch?: TaskPatch;
  created?: BoardTask;
  createInput?: CreateTaskInput;
  cursor?: string;
  failed?: string;
};

export type BoardSnapshot = {
  version: number;
  workspace: string;
  config: StreamConfig;
  tasks: BoardTask[];
  cursor: string;
  connection: ConnectionState;
  pending: PendingMutation[];
  live: LivePayload[];
};

const emptyConfig: StreamConfig = { statuses: [], terminal: [], labels: [] };
let mutationSequence = 0;

export class BoardStore {
  readonly workspace: string;
  private config: StreamConfig = emptyConfig;
  private base = new Map<string, BoardTask>();
  private pending: PendingMutation[] = [];
  private live = new Map<string, { value: LivePayload; timer: number }>();
  private observedCursors: string[] = [];
  private cursor = '';
  private connection: ConnectionState = 'idle';
  private version = 0;
  private listeners = new Set<() => void>();
  private snapshot: BoardSnapshot;

  constructor(workspace: string) {
    this.workspace = workspace;
    this.snapshot = this.buildSnapshot();
  }

  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  getSnapshot = () => this.snapshot;

  setConnection(connection: ConnectionState) {
    if (this.connection === connection) return;
    this.connection = connection;
    this.emit();
  }

  applyInit(value: StreamInit, cursor: string) {
    this.config = value.config;
    this.base = new Map(value.tasks.map((task) => [task.id, task]));
    this.cursor = cursor || value.cursor;
    this.rememberCursor(this.cursor);
    this.pending = this.pending.filter((mutation) => !mutation.cursor || !this.baseCovers(mutation));
    this.connection = 'open';
    this.emit();
  }

  applyPatch(value: StreamPatch, cursor: string) {
    if (cursor && this.observedCursors.includes(cursor)) return;
    if (value.task) this.base.set(value.task.id, value.task);
    this.cursor = cursor || this.cursor;
    this.rememberCursor(cursor);
    if (cursor) this.pending = this.pending.filter((mutation) => mutation.cursor !== cursor);
    this.connection = 'open';
    this.emit();
  }

  applyConfig(config: StreamConfig) {
    this.config = config;
    this.emit();
  }

  applyLive(value: LivePayload) {
    const key = `${value.kind}\0${value.task || ''}\0${value.session || ''}`;
    const previous = this.live.get(key);
    if (previous) window.clearTimeout(previous.timer);
    const timer = window.setTimeout(() => {
      this.live.delete(key);
      this.emit();
    }, Math.max(1, value.ttl_ms));
    this.live.set(key, { value, timer });
    this.emit();
  }

  optimisticPatch(taskId: string, patch: Partial<BoardTask>, requestPatch: TaskPatch = patch) {
    const id = `mutation-${++mutationSequence}`;
    this.pending.push({ id, kind: 'patch', taskId, patch, requestPatch });
    this.emit();
    return id;
  }

  optimisticCreate(task: BoardTask, createInput?: CreateTaskInput) {
    const id = `mutation-${++mutationSequence}`;
    this.pending.push({ id, kind: 'create', taskId: task.id, created: task, createInput });
    this.emit();
    return id;
  }

  acknowledge(id: string, cursor?: string, authoritative?: BoardTask) {
    const mutation = this.pending.find((item) => item.id === id);
    if (!mutation) return;
    if (authoritative) {
      mutation.taskId = authoritative.id;
      mutation.created = authoritative;
      mutation.patch = authoritative;
    }
    mutation.cursor = cursor;
    mutation.failed = undefined;
    if (!cursor || this.observedCursors.includes(cursor) || this.baseCovers(mutation)) {
      this.pending = this.pending.filter((item) => item.id !== id);
    }
    this.emit();
  }

  fail(id: string, message: string) {
    const mutation = this.pending.find((item) => item.id === id);
    if (!mutation) return;
    mutation.failed = message;
    this.emit();
  }

  dismiss(id: string) {
    this.pending = this.pending.filter((item) => item.id !== id);
    this.emit();
  }

  task(id: string): BoardTask | undefined {
    return this.visibleTasks().find((task) => task.id === id);
  }

  destroy() {
    for (const item of this.live.values()) window.clearTimeout(item.timer);
    this.live.clear();
    this.listeners.clear();
    this.connection = 'closed';
  }

  private rememberCursor(cursor: string) {
    if (!cursor || this.observedCursors.includes(cursor)) return;
    this.observedCursors.push(cursor);
    if (this.observedCursors.length > 256) this.observedCursors.shift();
  }

  private baseCovers(mutation: PendingMutation): boolean {
    const authoritative = mutation.created;
    if (!authoritative) return false;
    const current = this.base.get(authoritative.id);
    if (!current) return false;
    const currentTime = timestampNanos(current.updated_at);
    const mutationTime = timestampNanos(authoritative.updated_at);
    if (currentTime > mutationTime) return true;
    return current.updated_at === authoritative.updated_at && JSON.stringify(current) === JSON.stringify(authoritative);
  }

  private visibleTasks(): BoardTask[] {
    const tasks = new Map(this.base);
    for (const mutation of this.pending) {
      if (mutation.failed) continue;
      if (mutation.kind === 'create' && mutation.created) {
        tasks.set(mutation.created.id, mutation.created);
        continue;
      }
      const current = tasks.get(mutation.taskId);
      if (current && mutation.patch) tasks.set(mutation.taskId, { ...current, ...mutation.patch });
    }
    return [...tasks.values()];
  }

  private buildSnapshot(): BoardSnapshot {
    return {
      version: this.version,
      workspace: this.workspace,
      config: this.config,
      tasks: this.visibleTasks(),
      cursor: this.cursor,
      connection: this.connection,
      pending: this.pending.map((mutation) => ({ ...mutation })),
      live: [...this.live.values()].map((item) => item.value),
    };
  }

  private emit() {
    this.version += 1;
    this.snapshot = this.buildSnapshot();
    for (const listener of this.listeners) listener();
  }
}

function timestampNanos(value: string): bigint {
  const match = value.match(/^(.*?)(?:\.(\d+))?Z$/);
  if (!match) return BigInt(Date.parse(value) || 0) * 1_000_000n;
  const seconds = Date.parse(`${match[1]}Z`) || 0;
  const fraction = BigInt((match[2] || '').padEnd(9, '0').slice(0, 9) || '0');
  return BigInt(seconds) * 1_000_000n + fraction;
}

const stores = new Map<string, BoardStore>();
export function getBoardStore(workspace: string) {
  let store = stores.get(workspace);
  if (!store) {
    store = new BoardStore(workspace);
    stores.set(workspace, store);
  }
  return store;
}

export function useBoardStore(store: BoardStore): BoardSnapshot {
  return useSyncExternalStore(store.subscribe, store.getSnapshot, store.getSnapshot);
}
