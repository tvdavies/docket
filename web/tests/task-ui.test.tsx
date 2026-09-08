import { act, render } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { groupTaskRows } from '../src/views/list/ListView';
import { Activity, TaskDetail } from '../src/views/task/TaskDetail';
import type { BoardTask, TaskDetail as Detail } from '../src/types';

const tasks = [
  { id: 'A', status: 'done' }, { id: 'B', status: 'todo' }, { id: 'C', status: 'done' }, { id: 'D', status: 'custom' },
] as BoardTask[];
describe('status-grouped list', () => {
  test('uses configured group order and preserves sort order within each group', () => {
    const rows = groupTaskRows(tasks, ['todo', 'done', 'empty'], [], new Set(), false);
    expect(rows.map((row) => row.kind === 'status' ? `${row.status}:${row.count}` : row.task.id)).toEqual(['todo:1', 'B', 'done:2', 'A', 'C', 'custom:1', 'D']);
  });
  test('respects collapsed, hidden and empty groups', () => {
    const rows = groupTaskRows(tasks, ['todo', 'done', 'empty'], ['custom'], new Set(['done']), true);
    expect(rows.map((row) => row.kind === 'status' ? `${row.status}:${row.count}` : row.task.id)).toEqual(['todo:1', 'B', 'done:2', 'empty:0']);
  });
});

describe('task page', () => {
  test('renders inline rather than a modal and focuses the task identifier', async () => {
    let view!: ReturnType<typeof render>;
    await act(async () => { view = render(<TaskDetail workspace="demo" taskId="TASK-1" open config={{ statuses: [], terminal: [], labels: [] }} live={[]} onClose={() => {}} onPatch={async () => { throw new Error('not used'); }} onCursor={() => {}} />); });
    expect(view.container.querySelector('.task-detail-page')).not.toBeNull();
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(document.activeElement?.textContent).toBe('TASK-1');
    expect(view.getByText(/Could not load task:/)).toBeInTheDocument();
  });
  test('sorts oldest first without mutating source activity and distinguishes comments', () => {
    const activity = [
      { at: '2026-01-03T00:00:00Z', type: 'comment', kind: 'comment', actor: 'Tom', body: 'Latest comment' },
      { at: '2026-01-01T00:00:00Z', type: 'task.created', kind: 'event', actor: 'Tom' },
      { at: '2026-01-02T00:00:00Z', type: 'task.moved', kind: 'event', actor: 'Tom', data: { from: 'todo', to: 'done' } },
    ];
    const { container } = render(<Activity detail={{ activity } as Detail} />);
    expect([...container.querySelectorAll('time')].map((time) => time.dateTime)).toEqual(['2026-01-01T00:00:00Z', '2026-01-02T00:00:00Z', '2026-01-03T00:00:00Z']);
    expect(activity[0].body).toBe('Latest comment');
    expect(container.querySelectorAll('.comment-entry')).toHaveLength(1);
    expect(container.querySelector('ol > li:last-child')?.textContent).toContain('Latest comment');
  });
});
