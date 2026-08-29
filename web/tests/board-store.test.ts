import { describe, expect, test, vi } from 'vitest';
import { BoardStore } from '../src/store/board-store';
import type { BoardTask } from '../src/types';

const task = (overrides: Partial<BoardTask> = {}): BoardTask => ({
  id: 'TASK-0001', title: 'Original', status: 'todo', labels: [], references: [], active_sessions: [], created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z', resource_count: 0, ...overrides,
});

describe('BoardStore fold and optimistic overlay', () => {
  test('resets from init and applies idempotent full-summary patches', () => {
    const store = new BoardStore('demo');
    store.applyInit({ workspace: 'demo', config: { statuses: ['todo', 'done'], terminal: ['done'], labels: [] }, tasks: [task()], cursor: 'c0' }, 'c0');
    store.applyPatch({ event: { seq: 1, time: '', type: 'task.moved', task: 'TASK-0001' }, task: task({ status: 'done' }) }, 'c1');
    store.applyPatch({ event: { seq: 1, time: '', type: 'task.moved', task: 'TASK-0001' }, task: task({ status: 'todo' }) }, 'c1');
    expect(store.getSnapshot().tasks[0].status).toBe('done');
    store.applyInit({ workspace: 'demo', config: { statuses: ['todo'], terminal: [], labels: [] }, tasks: [task({ title: 'Reset' })], cursor: 'c2' }, 'c2');
    expect(store.getSnapshot().tasks[0].title).toBe('Reset');
  });

  test('renders optimistic patches, retires on cursor, and rolls back visibly on failure', () => {
    const store = new BoardStore('demo');
    store.applyInit({ workspace: 'demo', config: { statuses: ['todo', 'done'], terminal: [], labels: [] }, tasks: [task()], cursor: 'c0' }, 'c0');
    const first = store.optimisticPatch('TASK-0001', { status: 'done' });
    expect(store.getSnapshot().tasks[0].status).toBe('done');
    store.acknowledge(first, 'c1');
    expect(store.getSnapshot().pending).toHaveLength(1);
    store.applyPatch({ event: { seq: 1, time: '', type: 'task.moved' }, task: task({ status: 'done' }) }, 'c1');
    expect(store.getSnapshot().pending).toHaveLength(0);

    const second = store.optimisticPatch('TASK-0001', { status: 'todo' });
    store.fail(second, 'conflict');
    expect(store.getSnapshot().tasks[0].status).toBe('done');
    expect(store.getSnapshot().pending[0].failed).toBe('conflict');
  });

  test('keeps the complete API patch for retries when only card fields are optimistic', () => {
    const store = new BoardStore('demo');
    store.applyInit({ workspace: 'demo', config: { statuses: ['todo'], terminal: [], labels: [] }, tasks: [task()], cursor: 'c0' }, 'c0');
    const mutation = store.optimisticPatch('TASK-0001', {}, { description: 'Preserve this draft' });
    store.fail(mutation, 'offline');
    expect(store.getSnapshot().pending[0].requestPatch).toEqual({ description: 'Preserve this draft' });

    const optimistic = task({ id: 'NEW-1', title: 'Created draft' });
    const create = store.optimisticCreate(optimistic, { title: 'Created draft', description: 'Preserve create description', status: 'todo' });
    store.fail(create, 'offline');
    expect(store.getSnapshot().pending.find((item) => item.id === create)?.createInput?.description).toBe('Preserve create description');
  });

  test('acknowledges a mutation whose stream cursor arrived before HTTP success', () => {
    const store = new BoardStore('demo');
    store.applyInit({ workspace: 'demo', config: { statuses: ['todo'], terminal: [], labels: [] }, tasks: [task()], cursor: 'c0' }, 'c0');
    const mutation = store.optimisticPatch('TASK-0001', { title: 'Local' });
    store.applyPatch({ event: { seq: 1, time: '', type: 'task.updated' }, task: task({ title: 'Server' }) }, 'c1');
    store.acknowledge(mutation, 'c1', task({ title: 'Server' }));
    expect(store.getSnapshot().pending).toHaveLength(0);
    expect(store.getSnapshot().tasks[0].title).toBe('Server');
  });

  test('retires an acknowledged overlay when a reset snapshot already covers it', () => {
    const store = new BoardStore('demo');
    store.applyInit({ workspace: 'demo', config: { statuses: ['todo'], terminal: [], labels: [] }, tasks: [task()], cursor: 'c0' }, 'c0');
    const mutation = store.optimisticPatch('TASK-0001', { title: 'Saved' });
    const authoritative = task({ title: 'Saved', updated_at: '2026-01-02T00:00:00Z' });
    store.acknowledge(mutation, 'mutation-cursor', authoritative);
    expect(store.getSnapshot().pending).toHaveLength(1);
    store.applyInit({ workspace: 'demo', config: { statuses: ['todo'], terminal: [], labels: [] }, tasks: [{ ...authoritative, title: 'Newer server state', updated_at: '2026-01-02T00:00:00.000000001Z' }], cursor: 'later-init' }, 'later-init');
    expect(store.getSnapshot().pending).toHaveLength(0);
    expect(store.getSnapshot().tasks[0].title).toBe('Newer server state');
  });

  test('expires live payloads without touching base tasks', () => {
    vi.useFakeTimers();
    const store = new BoardStore('demo');
    store.applyLive({ kind: 'session', task: 'TASK-0001', payload: { state: 'working' }, ttl_ms: 20 });
    expect(store.getSnapshot().live).toHaveLength(1);
    vi.advanceTimersByTime(21);
    expect(store.getSnapshot().live).toHaveLength(0);
    vi.useRealTimers();
  });
});
