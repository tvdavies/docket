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
 * Loads GET /api/plugins on entry, on explicit reload, when the instance
 * stream (GET /api/stream) reports a changed plugin manifest, and on window
 * focus (config values edited from the CLI do not change a manifest). Settings
 * values are not carried by the task stream, so this is the only source of
 * truth for the forms. A failed read keeps the previous
 * entries but flags them as stale; it never presents an empty catalogue as
 * success.
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
    const refresh = () => void reload().catch(() => undefined);
    refresh();
    const onFocus = () => { if (document.visibilityState === 'visible') refresh(); };
    window.addEventListener('focus', onFocus);
    document.addEventListener('visibilitychange', onFocus);
    const unsubscribe = subscribePluginManifests(refresh);
    return () => {
      unsubscribe();
      window.removeEventListener('focus', onFocus);
      document.removeEventListener('visibilitychange', onFocus);
    };
  }, [reload]);

  return { entries, error, loading, loadedAt, reload };
}

type PluginsEvent = { plugins: { name: string; manifest_hash: string }[] };

/** Calls onChange when any installed plugin's manifest hash changes. */
function subscribePluginManifests(onChange: () => void): () => void {
  if (typeof EventSource === 'undefined') return () => undefined;
  const source = new EventSource('/api/stream');
  let known: string | null = null;
  source.addEventListener('plugins', (raw) => {
    let value: PluginsEvent;
    try { value = JSON.parse((raw as MessageEvent<string>).data) as PluginsEvent; } catch { return; }
    const signature = value.plugins.map(p => `${p.name}:${p.manifest_hash}`).join('|');
    if (known !== null && known !== signature) onChange();
    known = signature;
  });
  // Each (re)connect starts with a full snapshot, so edits made while the stream
  // was down still differ from `known`. The first snapshot matches the initial load.
  return () => source.close();
}
