import { useEffect, useRef, type PointerEvent as ReactPointerEvent } from 'react';

// Move the real card, keeping its measured virtual row as the placeholder.
// No native drag image or second React tree (including plugin content) is created.
export function useCardDrag(onDragging: (active: boolean) => void, onDrop: (status: string) => void) {
  const cleanup = useRef<(() => void) | null>(null);
  const suppressClick = useRef(false);
  useEffect(() => () => cleanup.current?.(), []);

  const onPointerDown = (event: ReactPointerEvent<HTMLElement>) => {
    if (event.button !== 0 || !event.isPrimary || (event.target as HTMLElement).closest('button, a, input, select, textarea, [role="menuitem"]')) return;
    cleanup.current?.();
    suppressClick.current = false;
    const card = event.currentTarget;
    const board = card.closest<HTMLElement>('.board-scroll')!;
    const bounds = card.getBoundingClientRect();
    const originalStyle = card.getAttribute('style');
    const row = card.closest<HTMLElement>('.virtual-row');
    const rowHeight = row?.style.height || '';
    const startX = event.clientX, startY = event.clientY;
    let x = startX, y = startY, active = false, frame = 0;
    let destination: HTMLElement | null = null;
    const pointerId = event.pointerId;
    const highlight = () => {
      const lane = document.elementFromPoint(x, y)?.closest<HTMLElement>('.lane') || null;
      const next = lane && board.contains(lane) ? lane : null;
      if (next !== destination) {
        if (destination) delete destination.dataset.drag;
        destination = next;
        if (destination) destination.dataset.drag = 'true';
      }
    };
    const tick = () => {
      highlight();
      const rect = board.getBoundingClientRect();
      const speed = (point: number, min: number, max: number) => point < min + 40 ? -12 : point > max - 40 ? 12 : 0;
      if (x >= rect.left && x <= rect.right && y >= rect.top && y <= rect.bottom) {
        board.scrollLeft += speed(x, rect.left, rect.right);
        const lane = destination?.querySelector<HTMLElement>('.lane-list');
        if (lane) { const area = lane.getBoundingClientRect(); lane.scrollTop += speed(y, area.top, area.bottom); }
      }
      frame = requestAnimationFrame(tick);
    };
    const restore = () => {
      cancelAnimationFrame(frame);
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', end);
      window.removeEventListener('pointercancel', cancel);
      window.removeEventListener('keydown', escape);
      window.removeEventListener('blur', cancel);
      if (destination) delete destination.dataset.drag;
      if (active) {
        if (originalStyle === null) card.removeAttribute('style'); else card.setAttribute('style', originalStyle);
        if (row) row.style.height = rowHeight;
        card.removeAttribute('data-dragging');
        document.body.classList.remove('dragging-task');
        onDragging(false);
      }
      cleanup.current = null;
    };
    const move = (pointer: PointerEvent) => {
      if (pointer.pointerId !== pointerId) return;
      x = pointer.clientX; y = pointer.clientY;
      if (!active && Math.hypot(x - startX, y - startY) < 6) return;
      pointer.preventDefault();
      if (!active) {
        // Freeze synchronously, before ResizeObserver can measure an empty row.
        if (row) row.style.height = `${row.getBoundingClientRect().height}px`;
        active = true; suppressClick.current = true; onDragging(true);
        Object.assign(card.style, { position: 'fixed', left: `${bounds.left}px`, top: `${bounds.top}px`, width: `${bounds.width}px`, height: `${bounds.height}px`, zIndex: '45', pointerEvents: 'none', margin: '0' });
        card.dataset.dragging = 'true'; document.body.classList.add('dragging-task');
        frame = requestAnimationFrame(tick);
      }
      card.style.transform = `translate3d(${x - startX}px, ${y - startY}px, 0)`;
      highlight();
    };
    const end = (pointer: PointerEvent) => {
      if (pointer.pointerId !== pointerId) return;
      x = pointer.clientX; y = pointer.clientY;
      if (active) highlight();
      const status = active ? destination?.dataset.status : undefined;
      restore();
      if (status) onDrop(status);
      // The synthesized click follows pointerup; don't open the dropped task.
      setTimeout(() => { suppressClick.current = false; }, 0);
    };
    const cancel = () => { restore(); suppressClick.current = active; };
    const escape = (key: KeyboardEvent) => { if (key.key === 'Escape') { key.preventDefault(); cancel(); } };
    cleanup.current = restore;
    window.addEventListener('pointermove', move, { passive: false });
    window.addEventListener('pointerup', end);
    window.addEventListener('pointercancel', cancel);
    window.addEventListener('keydown', escape);
    window.addEventListener('blur', cancel);
  };
  return { onPointerDown, suppressClick };
}
