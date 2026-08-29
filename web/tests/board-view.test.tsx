import { fireEvent, render } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { BoardView } from '../src/views/board/BoardView';
import { defaultPreferences } from '../src/store/preferences';
import type { BoardTask } from '../src/types';

const task: BoardTask = {
  id: 'JOB-0001',
  title: 'Move me',
  status: 'todo',
  labels: [],
  references: [],
  active_sessions: [],
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  resource_count: 0,
};

describe('BoardView drag and drop', () => {
  test('moves a task found outside the destination lane', () => {
    const onMove = vi.fn();
    const { container } = render(<BoardView
      workspace="demo"
      tasks={[task]}
      config={{ statuses: ['todo', 'done'], terminal: ['done'], labels: [] }}
      preferences={defaultPreferences()}
      selected={task.id}
      live={[]}
      onSelect={() => undefined}
      onMove={onMove}
    />);
    const destination = container.querySelector<HTMLElement>('[data-status="done"]');
    expect(destination).not.toBeNull();
    fireEvent.drop(destination!, {
      dataTransfer: { getData: (type: string) => type === 'text/plain' ? task.id : '' },
    });
    expect(onMove).toHaveBeenCalledWith(task, 'done');
  });
});
