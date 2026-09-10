import { useCallback, useEffect, useRef } from 'react';

/** A history index lets cancellation restore Back/Forward without deleting either entry. */
export function useSettingsNavigation(dirty: boolean, busy: boolean, taskDirty = false) {
  const state = useRef({ dirty, busy, taskDirty });
  state.current = { dirty, busy, taskDirty };
  const index = useRef<number>(history.state?.docketIndex ?? 0);
  const restoring = useRef(false);
  const allow = useCallback(() => {
    if (state.current.busy) { window.alert('A configuration save is pending. Wait for its result before leaving; navigation cannot undo it.'); return false; }
    if (state.current.dirty) return window.confirm('Discard unsaved plugin settings?');
    return !state.current.taskDirty || window.confirm('Discard unsaved task input?');
  }, []);
  useEffect(() => {
    history.replaceState({ ...history.state, docketIndex: index.current }, '', window.location.href);
    const pop = (event: PopStateEvent) => {
      if (restoring.current) { restoring.current = false; event.stopImmediatePropagation(); return; }
      const next = event.state?.docketIndex;
      if (!allow()) {
        event.stopImmediatePropagation();
        if (typeof next === 'number') { restoring.current = true; history.go(index.current - next); }
        return;
      }
      index.current = typeof next === 'number' ? next : 0;
    };
    const unload = (event: BeforeUnloadEvent) => { if (state.current.dirty || state.current.busy || state.current.taskDirty) { event.preventDefault(); event.returnValue = ''; } };
    // Capture runs before the app's route listener.
    window.addEventListener('popstate', pop, true);
    window.addEventListener('beforeunload', unload);
    return () => { window.removeEventListener('popstate', pop, true); window.removeEventListener('beforeunload', unload); };
  }, [allow]);
  const push = useCallback((path: string) => {
    if (!allow()) return false;
    index.current += 1;
    history.pushState({ docketIndex: index.current }, '', path);
    return true;
  }, [allow]);
  return { push, allow };
}
