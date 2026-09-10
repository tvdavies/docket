import { fireEvent, render } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { BoardView, TaskCard } from '../src/views/board/BoardView';
import { defaultPreferences } from '../src/store/preferences';
import type { BoardTask } from '../src/types';

const task: BoardTask = { id: 'JOB-0001', title: 'Move me', status: 'todo', labels: [], references: [], active_sessions: [], created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z', resource_count: 0 };
const config = { statuses: ['todo', 'done'], terminal: ['done'], labels: [] };
function pointer(target: Element | Window, type: string, x: number, y: number) {
  const event = new MouseEvent(type, { bubbles: true, cancelable: true, clientX: x, clientY: y, button: 0 });
  Object.defineProperties(event, { pointerId: { value: 1 }, isPrimary: { value: true } });
  fireEvent(target, event);
}
function setup() {
  const onMove = vi.fn(), onSelect = vi.fn(), onDragging = vi.fn();
  const view = render(<div className="board-scroll"><section className="lane" data-status="todo"><TaskCard workspace="demo" task={task} config={config} preferences={defaultPreferences()} selected live={[]} onMove={onMove} onSelect={onSelect} onDragging={onDragging} /></section><section className="lane" data-status="done" /></div>);
  const card = view.container.querySelector<HTMLElement>('.task-card')!;
  const destination = view.container.querySelector<HTMLElement>('[data-status="done"]')!;
  Object.defineProperty(document, 'elementFromPoint', { configurable: true, value: () => destination });
  return { ...view, card, destination, onMove, onSelect, onDragging };
}

test('settings actions belong only to composed lanes, including empty lanes', () => {
  const onSettings = vi.fn();
  const view = render(<BoardView workspace="demo" tasks={[{ ...task, status: 'unknown' }]} config={config} preferences={{ ...defaultPreferences(), showEmpty: true }} selected="" live={[]} onSelect={() => undefined} onMove={() => undefined} onSettings={onSettings} />);
  fireEvent.click(view.getByRole('button', { name: 'Plugin settings for Todo' }));
  expect(onSettings).toHaveBeenCalledWith('todo');
  expect(view.getByRole('button', { name: 'Plugin settings for Done' })).toBeTruthy();
  expect(view.queryByRole('button', { name: 'Plugin settings for Unknown' })).toBeNull();
});

describe('same-card pointer dragging', () => {
  test('moves the existing element, drops into another lane, and suppresses navigation', () => {
    const { card, container, onMove, onSelect, destination } = setup();
    pointer(card, 'pointerdown', 10, 10);
    pointer(window, 'pointermove', 200, 100);
    expect(container.querySelectorAll('.task-card')).toHaveLength(1);
    expect(container.querySelector('.task-card')).toBe(card);
    expect(card.style.position).toBe('fixed');
    expect(card.style.transform).toBe('translate3d(190px, 90px, 0)');
    expect(destination.dataset.drag).toBe('true');
    pointer(window, 'pointerup', 200, 100);
    fireEvent.click(card);
    expect(onMove).toHaveBeenCalledWith(task, 'done');
    expect(onSelect).not.toHaveBeenCalled();
    expect(card.style.position).toBe('');
    expect(destination.dataset.drag).toBeUndefined();
  });
  test('clicking without a drag opens the task and Enter opens it once', () => {
    const { card, onMove, onSelect } = setup();
    pointer(card, 'pointerdown', 10, 10); pointer(window, 'pointerup', 11, 11);
    fireEvent.click(card); fireEvent.keyDown(card, { key: 'Enter' });
    expect(onSelect).toHaveBeenCalledTimes(2);
    expect(onMove).not.toHaveBeenCalled();
  });
  test.each(['escape', 'pointercancel', 'outside'])('cleans up %s without moving', (kind) => {
    const { card, onMove, onSelect, destination } = setup();
    pointer(card, 'pointerdown', 10, 10); pointer(window, 'pointermove', 100, 100);
    if (kind === 'escape') fireEvent.keyDown(window, { key: 'Escape' });
    else if (kind === 'pointercancel') pointer(window, 'pointercancel', 100, 100);
    else { Object.defineProperty(document, 'elementFromPoint', { configurable: true, value: () => null }); pointer(window, 'pointerup', 900, 900); }
    fireEvent.click(card);
    expect(onSelect).not.toHaveBeenCalled();
    expect(onMove).not.toHaveBeenCalled();
    expect(card.style.position).toBe('');
    expect(destination.dataset.drag).toBeUndefined();
    expect(document.body.classList.contains('dragging-task')).toBe(false);
  });
  test('unmount during a drag restores the global cursor', () => {
    const { card, unmount } = setup();
    pointer(card, 'pointerdown', 10, 10); pointer(window, 'pointermove', 100, 100); unmount();
    expect(document.body.classList.contains('dragging-task')).toBe(false);
  });
});
