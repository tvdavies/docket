import { describe, expect, test } from 'vitest';
import { allStatuses, defaultPreferences, filterAndSort, loadPreferences } from '../src/store/preferences';
import type { BoardTask } from '../src/types';

const tasks: BoardTask[] = [
  { id: 'TASK-0001', title: 'Alpha', status: 'todo', assignee: '', project: '', labels: ['bug'], references: [], active_sessions: [], created_at: '2026-01-01', updated_at: '2026-01-02', resource_count: 0 },
  { id: 'TASK-0002', title: 'Beta', status: 'legacy', assignee: 'agent', project: 'PROJ-1', labels: ['feature'], wait: { id: 'wait-1', kind: 'review', reason: 'x', since: '2026-01-01' }, references: [], active_sessions: [], created_at: '2026-01-02', updated_at: '2026-01-03', resource_count: 0 },
];
const config = { statuses: ['todo', 'done'], terminal: ['done'], labels: ['bug', 'feature'] };

describe('preferences and selectors', () => {
  test('preserves unknown task statuses', () => expect(allStatuses(config, tasks)).toEqual(['todo', 'done', 'legacy']));
  test('filters across labels, waiting state, and text then sorts stably', () => {
    const preferences = defaultPreferences(); preferences.filters.labels = ['feature']; preferences.filters.states = ['waiting']; preferences.filters.query = 'agent';
    expect(filterAndSort(tasks, config, preferences).map((task) => task.id)).toEqual(['TASK-0002']);
  });
  test('migrates classic v1 storage while removing stale filter values', () => {
    localStorage.setItem('docket.explorer.v1.demo', JSON.stringify({ version: 1, view: 'list', filters: { statuses: ['missing'], labels: ['bug'] } }));
    const value = loadPreferences('demo', config, tasks);
    expect(value.view).toBe('list'); expect(value.filters.statuses).toEqual([]); expect(value.filters.labels).toEqual(['bug']);
  });
});
