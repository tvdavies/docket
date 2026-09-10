import { useCallback, useEffect, useRef, useState } from 'react';
import { listPluginCatalogue, type PluginCatalogueEntry } from '../../api/plugin-settings';

export type CatalogueState = {
  entries: PluginCatalogueEntry[] | null;
  /** Message from the last failed read. Retained entries are stale while this is set. */
  error: string;
  loading: boolean;
  loadedAt: number;
  reload(): Promise<PluginCatalogueEntry[]>;
};

/**
 * Loads GET /api/plugins on entry, window focus and explicit reload. Settings
 * values are not carried by the task stream, so this is the only source of
 * truth for the forms. A failed read keeps the previous entries but flags them
 * as stale; it never presents an empty catalogue as success.
 */
export function useCatalogue(): CatalogueState {
  const [entries, setEntries] = useState<PluginCatalogueEntry[] | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const [loadedAt, setLoadedAt] = useState(0);
  const inflight = useRef<Promise<PluginCatalogueEntry[]> | null>(null);
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);

  const reload = useCallback(() => {
    if (inflight.current) return inflight.current;
    setLoading(true);
    const request = listPluginCatalogue().then((values) => {
      if (mounted.current) { setEntries(values); setError(''); setLoadedAt(Date.now()); }
      return values;
    }, (cause: unknown) => {
      if (mounted.current) setError(cause instanceof Error ? cause.message : String(cause));
      throw cause;
    }).finally(() => { inflight.current = null; if (mounted.current) setLoading(false); });
    inflight.current = request;
    return request;
  }, []);

  useEffect(() => {
    void reload().catch(() => undefined);
    const onFocus = () => { if (document.visibilityState === 'visible') void reload().catch(() => undefined); };
    window.addEventListener('focus', onFocus);
    document.addEventListener('visibilitychange', onFocus);
    return () => { window.removeEventListener('focus', onFocus); document.removeEventListener('visibilitychange', onFocus); };
  }, [reload]);

  return { entries, error, loading, loadedAt, reload };
}
