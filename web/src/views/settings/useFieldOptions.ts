import { useCallback, useEffect, useRef, useState } from 'react';
import { fetchFieldOptions, type PluginConfigField } from '../../api/plugin-settings';
import { hasDynamicOptions, parseFieldOptions, type FieldOption } from './model';

export type FieldOptionsState = {
  /** Null until the first successful read, and after a failure. */
  options: FieldOption[] | null;
  error: string;
  loading: boolean;
  refresh(): void;
};

type PluginSnapshot = { name: string; manifest_hash?: string; service?: { state?: string; restarts?: number } };
type Listener = (plugin: string) => void;

// One EventSource on /api/stream serves every options_from field on the page.
const listeners = new Set<Listener>();
let source: EventSource | null = null;
let signatures = new Map<string, string>();

function subscribePluginChanges(listener: Listener): () => void {
  listeners.add(listener);
  if (!source && typeof EventSource !== 'undefined') {
    source = new EventSource('/api/stream');
    source.addEventListener('plugins', (raw) => {
      let plugins: PluginSnapshot[];
      try { plugins = (JSON.parse((raw as MessageEvent<string>).data) as { plugins: PluginSnapshot[] }).plugins; } catch { return; }
      const next = new Map(plugins.map((plugin) => [plugin.name, `${plugin.manifest_hash}:${plugin.service?.state ?? ''}:${plugin.service?.restarts ?? 0}`]));
      // A service becoming ready or restarting, or a manifest edit, can change the options a plugin serves.
      for (const [name, signature] of next) if (signatures.has(name) && signatures.get(name) !== signature) for (const notify of listeners) notify(name);
      signatures = next;
    });
  }
  return () => {
    listeners.delete(listener);
    if (!listeners.size && source) { source.close(); source = null; signatures = new Map(); }
  };
}

/** Loads a field's options_from choices through the plugin proxy and keeps them current. */
export function useFieldOptions(plugin: string, field: PluginConfigField): FieldOptionsState {
  const dynamic = hasDynamicOptions(field);
  const path = field.options_from || '';
  const [state, setState] = useState<Omit<FieldOptionsState, 'refresh'>>({ options: null, error: '', loading: dynamic });
  const generation = useRef(0);
  const fieldRef = useRef(field);
  fieldRef.current = field;

  const refresh = useCallback(() => {
    if (!dynamic) return;
    const current = ++generation.current;
    setState((previous) => ({ ...previous, loading: true }));
    fetchFieldOptions(plugin, path).then((payload) => {
      if (current !== generation.current) return;
      setState({ options: parseFieldOptions(fieldRef.current, payload), error: '', loading: false });
    }).catch((cause: unknown) => {
      if (current !== generation.current) return;
      setState({ options: null, error: cause instanceof Error ? cause.message : String(cause), loading: false });
    });
  }, [dynamic, plugin, path]);

  useEffect(() => {
    if (!dynamic) return;
    refresh();
    const unsubscribe = subscribePluginChanges((name) => { if (name === plugin) refresh(); });
    return () => { unsubscribe(); generation.current += 1; };
  }, [dynamic, plugin, refresh]);

  return { ...state, refresh };
}
