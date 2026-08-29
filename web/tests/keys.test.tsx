import { fireEvent, render } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { useBoardKeys } from '../src/keys/useBoardKeys';
import type { BoardTask } from '../src/types';

const tasks: BoardTask[] = ['todo', 'done'].map((status, index) => ({ id: `TASK-${index + 1}`, title: `Task ${index + 1}`, status, labels: [], references: [], active_sessions: [], created_at: '', updated_at: '', resource_count: 0 }));

function Harness(props: any) { useBoardKeys({ tasks, statuses: ['todo', 'done'], selected: 'TASK-1', ...props }); return <input aria-label="typing" />; }

describe('keyboard model', () => {
  test('navigates, opens, moves, and exposes action shortcuts outside inputs', () => {
    const handlers = { onSelect: vi.fn(), onOpen: vi.fn(), onMove: vi.fn(), onPalette: vi.fn(), onCreate: vi.fn(), onFilter: vi.fn(), onAssign: vi.fn(), onLabel: vi.fn(), onWaitView: vi.fn() };
    render(<Harness {...handlers} />);
    fireEvent.keyDown(window, { key: 'j' }); expect(handlers.onSelect).toHaveBeenCalledWith('TASK-2');
    fireEvent.keyDown(window, { key: 'Enter' }); expect(handlers.onOpen).toHaveBeenCalledWith('TASK-1');
    fireEvent.keyDown(window, { key: 'ArrowRight', shiftKey: true }); expect(handlers.onMove).toHaveBeenCalledWith(tasks[0], 'done');
    handlers.onMove.mockClear(); fireEvent.keyDown(window, { key: 'm' }); fireEvent.keyDown(window, { key: '2' }); expect(handlers.onMove).toHaveBeenCalledWith(tasks[0], 'done');
    fireEvent.keyDown(window, { key: 'a' }); expect(handlers.onAssign).toHaveBeenCalledWith(tasks[0]);
    fireEvent.keyDown(window, { key: 'l' }); expect(handlers.onLabel).toHaveBeenCalledWith(tasks[0]);
    fireEvent.keyDown(window, { key: 'w' }); expect(handlers.onWaitView).toHaveBeenCalled();
    fireEvent.keyDown(window, { key: 'k', metaKey: true }); expect(handlers.onPalette).toHaveBeenCalled();
  });

  test('does not trigger single-key commands while typing', () => {
    const handlers = { onSelect: vi.fn(), onOpen: vi.fn(), onMove: vi.fn(), onPalette: vi.fn(), onCreate: vi.fn(), onFilter: vi.fn(), onAssign: vi.fn(), onLabel: vi.fn(), onWaitView: vi.fn() };
    const view = render(<Harness {...handlers} />);
    fireEvent.keyDown(view.getByLabelText('typing'), { key: 'c' });
    expect(handlers.onCreate).not.toHaveBeenCalled();
  });
});
