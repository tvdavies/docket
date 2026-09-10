import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { api, listWorkspaces, workspacePath } from '../../api/client';
import type { PluginCatalogueEntry } from '../../api/plugin-settings';
import { explorerPath, instanceSettingsPath, statusSettingsPath, workspaceSettingsPath, type SettingsRoute } from '../../app/router';
import { PluginSettingsForm } from './PluginSettingsForm';
import { humanizeKey, targetKey, type FormTarget } from './model';
import { useCatalogue } from './useCatalogue';
import './settings.css';

type BoardMetadata = { statuses: string[]; plugins: { name: string; version: string }[] };

export async function readSettingsBoard(workspace: string): Promise<BoardMetadata> {
  const workspaces = await listWorkspaces();
  if (!Array.isArray(workspaces)) throw new Error('Workspace list is malformed');
  const current = workspaces.find((item) => item.name === workspace);
  if (!current) throw new Error(`Workspace ${workspace} is not registered.`);
  if (current.state !== 'watching') throw new Error(`Workspace ${workspace} is unavailable. ${current.last_error || 'Repair its configuration before saving.'}`);
  const board = await api<BoardMetadata>(`${workspacePath(workspace)}/board`);
  if (!board || !Array.isArray(board.statuses) || !board.statuses.every((item) => typeof item === 'string') || !Array.isArray(board.plugins) || !board.plugins.every((item) => typeof item.name === 'string' && typeof item.version === 'string')) throw new Error('Board metadata is malformed');
  return board;
}

export function SettingsPage({ route, selectedWorkspace, onNavigate, onDirtyChange, onBusyChange }: {
  route: SettingsRoute; selectedWorkspace: string; onNavigate(path: string): void;
  onDirtyChange(dirty: boolean): void; onBusyChange(busy: boolean): void;
}) {
  const catalogue = useCatalogue();
  const [board, setBoard] = useState<BoardMetadata | null>(null);
  const [boardError, setBoardError] = useState('');
  const mounted = useRef(true);
  const heading = useRef<HTMLHeadingElement>(null);
  const forms = useRef(new Map<string, PluginCatalogueEntry>());
  const dirty = useRef(new Set<string>());
  const busy = useRef(new Set<string>());
  const workspace = route.scope === 'instance' ? selectedWorkspace : route.workspace;
  useEffect(() => { heading.current?.focus(); mounted.current = true; return () => { mounted.current = false; onDirtyChange(false); onBusyChange(false); }; }, [onDirtyChange, onBusyChange]);
  const reportDirty = useCallback((key: string, value: boolean) => { if (value) dirty.current.add(key); else dirty.current.delete(key); onDirtyChange(dirty.current.size > 0); }, [onDirtyChange]);
  const reportBusy = useCallback((key: string, value: boolean) => { if (value) busy.current.add(key); else busy.current.delete(key); onBusyChange(busy.current.size > 0); }, [onBusyChange]);
  const refreshBoard = useCallback(async () => {
    if (route.scope === 'instance') return null;
    try {
      const fresh = await readSettingsBoard(route.workspace);
      if (mounted.current) { setBoard(fresh); setBoardError(''); }
      return fresh;
    } catch (cause) {
      if (mounted.current) setBoardError(cause instanceof Error ? cause.message : String(cause));
      throw cause;
    }
  }, [route.scope, route.workspace]);
  useEffect(() => { void refreshBoard().catch(() => undefined); }, [refreshBoard, catalogue.loadedAt]);
  const reload = useCallback(async () => {
    const entries = await catalogue.reload();
    await refreshBoard();
    return entries;
  }, [catalogue.reload, refreshBoard]);
  const entries = useMemo(() => {
    for (const entry of catalogue.entries || []) {
      if (route.scope === 'instance' || entry.workspace_values[route.workspace] || board?.plugins.some((item) => item.name === entry.name)) forms.current.set(entry.name, entry);
    }
    // Keep mounted forms on removal/failure so their drafts survive. Missing entries become read-only.
    return [...forms.current.values()];
  }, [catalogue.entries, board, route]);
  const missing = board?.plugins.filter((plugin) => !catalogue.entries?.some((entry) => entry.name === plugin.name)) || [];
  const unknownLane = route.scope === 'status' && board && !board.statuses.includes(route.status);
  const pageBlock = catalogue.error || boardError || (unknownLane ? `Lane ${route.status} is not a composed status.` : '') || (route.scope !== 'instance' && !board ? 'Checking workspace availability…' : '');
  const navigateLink = (path: string, text: string, current = false) => <a href={path} aria-current={current ? 'page' : undefined} onClick={(event) => { event.preventDefault(); onNavigate(path); }}>{text}</a>;
  return <main className="settings-page">
    <header className="settings-page-head"><div><p className="settings-eyebrow">Plugin configuration</p><h1 ref={heading} tabIndex={-1}>{route.scope === 'instance' ? 'Instance plugin settings' : route.scope === 'workspace' ? `Board plugin settings · ${route.workspace}` : `Lane plugin settings · ${humanizeKey(route.status)}`}</h1><p>Generated from installed plugin schemas. Each form saves only to its named scope.</p></div><button onClick={() => void reload().catch(() => undefined)} disabled={catalogue.loading || busy.current.size > 0}>Reload configuration</button></header>
    <nav className="settings-nav" aria-label="Settings scope">
      {navigateLink(instanceSettingsPath(), 'Instance settings', route.scope === 'instance')}
      {workspace && navigateLink(workspaceSettingsPath(workspace), `Board settings · ${workspace}`, route.scope === 'workspace')}
      {workspace && navigateLink(explorerPath(workspace), 'Back to board')}
      {board && <label>Lane <select aria-label="Lane settings" value={route.scope === 'status' ? route.status : ''} onChange={(event) => { if (event.target.value) onNavigate(statusSettingsPath(route.workspace, event.target.value)); }}><option value="">Choose a lane…</option>{unknownLane && <option value={route.status}>{route.status} (missing)</option>}{board.statuses.map((status) => <option key={status} value={status}>{humanizeKey(status)}</option>)}</select></label>}
    </nav>
    {pageBlock && <div className="settings-banner error-banner" role="alert">{pageBlock} Cached forms cannot be saved. Repair unavailable plugins/workspaces if needed, then retry Reload configuration.</div>}
    {missing.length > 0 && <div className="settings-banner error-banner" role="alert">Board plugins missing from the catalogue: {missing.map((item) => item.name).join(', ')}. No settings can be invented for missing plugins.</div>}
    {!catalogue.entries && !catalogue.error && <p role="status">Loading plugin schemas…</p>}
    {catalogue.entries && !entries.length && !pageBlock && <p className="settings-empty">{route.scope === 'instance' ? 'No plugins are installed on this instance.' : 'No plugins are enabled on this board.'}</p>}
    {entries.map((entry) => {
      const target: FormTarget = { ...route, plugin: entry.name };
      const fresh = catalogue.entries?.find((item) => item.name === entry.name);
      const absent = !fresh ? `Plugin ${entry.name} is no longer installed.` : route.scope !== 'instance' && (!fresh.workspace_values[route.workspace] || !board?.plugins.some((item) => item.name === entry.name)) ? `Plugin ${entry.name} is not enabled on this board.` : '';
      return <ScopedForm key={targetKey(target)} entry={entry} target={target} blocked={pageBlock || absent} reload={reload} refreshBoard={refreshBoard} reportDirty={reportDirty} reportBusy={reportBusy} />;
    })}
    <p className="settings-footnote">Saving configuration does not verify plugin service health. Acting as is attribution, not access control; these endpoints do not create task audit events. There is no reset/delete API. Discard only removes unsaved edits. Concurrent edits to the same field may still be last-writer-wins.</p>
  </main>;
}

function ScopedForm({ target, refreshBoard, reportDirty, reportBusy, ...props }: {
  target: FormTarget; entry: PluginCatalogueEntry; blocked: string; reload(): Promise<PluginCatalogueEntry[]>;
  refreshBoard(): Promise<BoardMetadata | null>;
  reportDirty(key: string, value: boolean): void; reportBusy(key: string, value: boolean): void;
}) {
  const key = targetKey(target);
  const onDirtyChange = useCallback((value: boolean) => reportDirty(key, value), [key, reportDirty]);
  const onBusyChange = useCallback((value: boolean) => reportBusy(key, value), [key, reportBusy]);
  const checkTarget = useCallback(async (entries: PluginCatalogueEntry[]) => {
    if (target.scope === 'instance') return '';
    const board = await refreshBoard();
    if (!board?.plugins.some((item) => item.name === target.plugin) || !entries.find((item) => item.name === target.plugin)?.workspace_values[target.workspace]) return 'Plugin is no longer enabled on this board.';
    if (target.scope === 'status' && !board.statuses.includes(target.status)) return 'Lane is no longer a composed status.';
    return '';
  }, [target, refreshBoard]);
  return <PluginSettingsForm {...props} target={target} checkTarget={checkTarget} onDirtyChange={onDirtyChange} onBusyChange={onBusyChange} />;
}
