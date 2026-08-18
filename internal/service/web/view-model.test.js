import { describe, expect, test } from 'bun:test';
import { allStatuses, filterAndSortTasks, groupTasks, normalisePreferences } from './view-model.js';

const board = {
  statuses: ['ready', 'doing', 'done'], terminal: ['done'],
  tasks: [
    { id: 'TASK-0002', title: 'Beta', status: 'doing', assignee: 'tom', labels: ['bug'], project: 'P1', created_at: '2026-01-02T00:00:00Z', updated_at: '2026-01-03T00:00:00Z' },
    { id: 'TASK-0001', title: 'Alpha', status: 'ready', labels: ['feature'], created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-04T00:00:00Z', wait: { kind: 'review' } },
    { id: 'TASK-0003', title: 'Cancelled legacy', status: 'cancelled', labels: [], created_at: '2026-01-03T00:00:00Z', updated_at: '2026-01-02T00:00:00Z' },
    { id: 'TASK-0004', title: 'Done', status: 'done', labels: [], created_at: '2026-01-03T00:00:00Z', updated_at: '2026-01-05T00:00:00Z' },
  ],
};

describe('view model', () => {
  test('keeps unknown statuses and normalises stale preferences', () => {
    expect(allStatuses(board)).toEqual(['ready', 'doing', 'done', 'cancelled']);
    const preferences = normalisePreferences({ version: 1, view: 'x', order: 'bad', hiddenStatuses: ['cancelled', 'gone'], filters: { labels: ['bug', 'gone'] } }, board);
    expect(preferences.view).toBe('board');
    expect(preferences.order).toBe('updated-desc');
    expect(preferences.hiddenStatuses).toEqual(['cancelled']);
    expect(preferences.filters.labels).toEqual(['bug']);
  });

  test('combines filters and orders with stable ids', () => {
    const preferences = normalisePreferences({ version: 1, order: 'title-asc', filters: { states: ['open'], labels: ['bug', 'feature'] } }, board);
    expect(filterAndSortTasks(board, preferences).map((task) => task.id)).toEqual(['TASK-0001', 'TASK-0002']);
    preferences.filters.query = 'beta';
    expect(filterAndSortTasks(board, preferences).map((task) => task.id)).toEqual(['TASK-0002']);
  });

  test('supports waiting/terminal, unassigned, no project, hidden and all-hidden states', () => {
    let preferences = normalisePreferences({ version: 1, filters: { states: ['waiting'], assignees: [''], projects: [''] } }, board);
    expect(filterAndSortTasks(board, preferences).map((task) => task.id)).toEqual(['TASK-0001']);
    preferences = normalisePreferences({ version: 1, filters: { states: ['terminal'] } }, board);
    expect(filterAndSortTasks(board, preferences).map((task) => task.id)).toEqual(['TASK-0004']);
    preferences.hiddenStatuses = allStatuses(board);
    expect(filterAndSortTasks(board, preferences)).toEqual([]);
    expect([...groupTasks(board, preferences).keys()]).toEqual(allStatuses(board));
  });

  test('orders created and updated dates in both directions', () => {
    for (const [order, first] of [['created-asc', 'TASK-0001'], ['created-desc', 'TASK-0003'], ['updated-asc', 'TASK-0003'], ['updated-desc', 'TASK-0004'], ['id-desc', 'TASK-0004']]) {
      const preferences = normalisePreferences({ version: 1, order }, board);
      expect(filterAndSortTasks(board, preferences)[0].id).toBe(first);
    }
  });
});
