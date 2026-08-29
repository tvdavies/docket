import { useEffect, useRef } from 'react';
import type { BoardTask } from '../types';

const inOverlay = (target: EventTarget | null) => target instanceof HTMLElement && Boolean(target.closest('[role="dialog"], [role="menu"], [role="menuitem"]'));
const isInteractiveContext = (target: EventTarget | null) => target instanceof HTMLElement && (
  target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT', 'BUTTON', 'A'].includes(target.tagName)
);

export function useBoardKeys({ tasks, statuses, selected, onSelect, onOpen, onMove, onPalette, onCreate, onFilter, onAssign, onLabel, onWaitView }: {
  tasks: BoardTask[]; statuses: string[]; selected: string;
  onSelect(task: string): void; onOpen(task: string): void; onMove(task: BoardTask, status: string): void;
  onPalette(): void; onCreate(): void; onFilter(): void; onAssign(task: BoardTask): void; onLabel(task: BoardTask): void; onWaitView(): void;
}) {
  const moveMode = useRef<number | null>(null);
  useEffect(() => {
    const handler = (event: KeyboardEvent) => {
      if (inOverlay(event.target)) return;
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') { event.preventDefault(); onPalette(); return; }
      if (isInteractiveContext(event.target) || event.altKey || event.metaKey || event.ctrlKey) return;
      const index = Math.max(0, tasks.findIndex((task) => task.id === selected));
      const task = tasks[index];
      if (moveMode.current && /^\d$/.test(event.key)) {
        const lane = Number(event.key) - 1; window.clearTimeout(moveMode.current); moveMode.current = null;
        if (task && statuses[lane]) { event.preventDefault(); onMove(task, statuses[lane]); } return;
      }
      if (event.key === 'm' && task) { event.preventDefault(); moveMode.current = window.setTimeout(() => { moveMode.current = null; }, 1400); return; }
      if (event.shiftKey && event.key === 'ArrowRight' && task) { event.preventDefault(); const lane = statuses.indexOf(task.status); if (statuses[lane + 1]) onMove(task, statuses[lane + 1]); }
      else if (event.shiftKey && event.key === 'ArrowLeft' && task) { event.preventDefault(); const lane = statuses.indexOf(task.status); if (statuses[lane - 1]) onMove(task, statuses[lane - 1]); }
      else if (event.key === 'j' || event.key === 'ArrowDown') { event.preventDefault(); onSelect(tasks[Math.min(tasks.length - 1, index + 1)]?.id || ''); }
      else if (event.key === 'k' || event.key === 'ArrowUp') { event.preventDefault(); onSelect(tasks[Math.max(0, index - 1)]?.id || ''); }
      else if (event.key === 'Enter' && task) { event.preventDefault(); onOpen(task.id); }
      else if (event.key === '/') { event.preventDefault(); onFilter(); }
      else if (event.key === 'c') { event.preventDefault(); onCreate(); }
      else if (event.key === 'a' && task) { event.preventDefault(); onAssign(task); }
      else if (event.key === 'l' && task) { event.preventDefault(); onLabel(task); }
      else if (event.key === 'w') { event.preventDefault(); onWaitView(); }
    };
    window.addEventListener('keydown', handler);
    return () => { window.removeEventListener('keydown', handler); if (moveMode.current) window.clearTimeout(moveMode.current); };
  }, [tasks, statuses, selected, onSelect, onOpen, onMove, onPalette, onCreate, onFilter, onAssign, onLabel, onWaitView]);
}
