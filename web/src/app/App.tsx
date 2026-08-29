import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import type { BoardTask, CreateTaskInput, TaskDetail, TaskPatch, WorkspaceStatus } from '../types';
import { createTask, getActor, listWorkspaces, patchTask, setActor } from '../api/client';
import { connectWorkspaceStream } from '../stream/connect';
import { getBoardStore, useBoardStore, type PendingMutation } from '../store/board-store';
import { activeFilterCount, allStatuses, filterAndSort, loadPreferences, savePreferences, type Preferences } from '../store/preferences';
import { classicPath, explorerPath, parseRoute, resolveRoute, taskRoutePath } from './router';
import { BoardView } from '../views/board/BoardView';
import { ListView } from '../views/list/ListView';
import { TaskDetail as TaskDetailPanel } from '../views/task/TaskDetail';
import { CreateTaskDialog } from '../views/task/CreateTaskDialog';
import { CommandPalette, type PaletteAction } from '../views/palette/CommandPalette';
import { useBoardKeys } from '../keys/useBoardKeys';

const toBoardTask = (value: TaskDetail): BoardTask => ({
  id: value.id, title: value.title, status: value.status, project: value.project?.id, labels: value.labels || [], assignee: value.assignee,
  wait: value.wait, references: value.references || [], active_sessions: [], created_at: value.created_at, updated_at: value.updated_at,
  resource_count: (value.references?.length || 0) + (value.attachments?.length || 0),
});

export function App() {
  const [workspaces, setWorkspaces] = useState<WorkspaceStatus[]>([]);
  const [workspace, setWorkspace] = useState('');
  const [routeTask, setRouteTask] = useState('');
  const [selected, setSelected] = useState('');
  const [preferences, setPreferences] = useState<Preferences | null>(null);
  const [actor, setActorState] = useState(getActor());
  const [createOpen, setCreateOpen] = useState(false);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [taskDraft, setTaskDraft] = useState(false);
  const [notice, setNotice] = useState('');
  const search = useRef<HTMLInputElement>(null);
  const store = useMemo(() => getBoardStore(workspace || '__pending__'), [workspace]);
  const snapshot = useBoardStore(store);

  const refreshWorkspaces = useCallback(async () => {
    try {
      const values = await listWorkspaces(); setWorkspaces(values);
      if (values.length && (!workspace || !values.some((item) => item.name === workspace))) {
        let stored = ''; try { stored = localStorage.getItem('docket.workspace') || ''; } catch { /* optional */ }
        const route = resolveRoute(parseRoute(window.location), values.map((item) => item.name), stored);
        setWorkspace(route.workspace); setRouteTask(route.task); setPreferences(null);
        if (!route.valid || parseRoute(window.location).legacy || window.location.pathname === '/' || window.location.pathname.startsWith('/next')) {
          history.replaceState(null, '', route.task ? taskRoutePath(route.workspace, route.task) : explorerPath(route.workspace));
        }
      }
    } catch (cause) { setNotice(`Could not load workspaces: ${cause instanceof Error ? cause.message : String(cause)}`); }
  }, [workspace]);

  useEffect(() => { void refreshWorkspaces(); const timer = window.setInterval(() => void refreshWorkspaces(), 15_000); return () => window.clearInterval(timer); }, [refreshWorkspaces]);
  useEffect(() => { if (!workspace || workspace === '__pending__') return; try { localStorage.setItem('docket.workspace', workspace); } catch { /* optional */ } return connectWorkspaceStream(workspace, store); }, [workspace, store]);
  useEffect(() => {
    const pop = () => {
      if (taskDraft && routeTask && !window.confirm('Discard unsaved task input?')) { history.pushState(null, '', taskRoutePath(workspace, routeTask)); return; }
      const route = resolveRoute(parseRoute(window.location), workspaces.map((item) => item.name), workspace); setWorkspace(route.workspace); setRouteTask(route.task);
    };
    window.addEventListener('popstate', pop); return () => window.removeEventListener('popstate', pop);
  }, [workspaces, workspace, routeTask, taskDraft]);
  useEffect(() => {
    if (!workspace || !snapshot.config.statuses.length) return;
    setPreferences((current) => current || loadPreferences(workspace, snapshot.config, snapshot.tasks));
  }, [workspace, snapshot.config, snapshot.tasks]);
  useEffect(() => { if (preferences && workspace) { savePreferences(workspace, preferences); applyTheme(preferences.theme); } }, [preferences, workspace]);

  const visibleTasks = useMemo(() => preferences ? filterAndSort(snapshot.tasks, snapshot.config, preferences) : snapshot.tasks, [snapshot.tasks, snapshot.config, preferences]);
  useEffect(() => { if (!selected || !visibleTasks.some((task) => task.id === selected)) setSelected(visibleTasks[0]?.id || ''); }, [visibleTasks, selected]);

  const navigateTask = useCallback((task: string) => { if (!workspace) return; if (taskDraft && routeTask && task !== routeTask && !window.confirm('Discard unsaved task input?')) return; setSelected(task); setRouteTask(task); history.pushState(null, '', taskRoutePath(workspace, task)); }, [workspace, taskDraft, routeTask]);
  const closeTask = useCallback(() => { setRouteTask(''); history.pushState(null, '', explorerPath(workspace)); }, [workspace]);
  const switchWorkspace = (name: string) => { if (name === workspace) return; if (taskDraft && !window.confirm('Discard unsaved task input?')) return; setWorkspace(name); setRouteTask(''); setSelected(''); setPreferences(null); history.pushState(null, '', explorerPath(name)); };

  const performPatch = useCallback(async (taskId: string, patch: TaskPatch): Promise<TaskDetail> => {
    const boardPatch: Partial<BoardTask> = {};
    if (patch.title !== undefined) boardPatch.title = patch.title;
    if (patch.status !== undefined) boardPatch.status = patch.status;
    if (patch.assignee !== undefined) boardPatch.assignee = patch.assignee;
    if (patch.labels !== undefined) boardPatch.labels = patch.labels;
    const mutation = store.optimisticPatch(taskId, boardPatch);
    try {
      const updated = await patchTask(workspace, taskId, patch);
      store.acknowledge(mutation, updated.cursor, toBoardTask(updated)); setNotice(''); return updated;
    } catch (cause) {
      store.fail(mutation, cause instanceof Error ? cause.message : String(cause)); throw cause;
    }
  }, [store, workspace]);

  const moveTask = useCallback((task: BoardTask, status: string) => { if (task.status !== status) void performPatch(task.id, { status }).catch(() => undefined); }, [performPatch]);

  const performCreate = async (input: CreateTaskInput) => {
    const now = new Date().toISOString(); const tempId = `NEW-${Date.now()}`;
    const optimistic: BoardTask = { id: tempId, title: input.title, status: input.status || snapshot.config.statuses[0] || '', project: input.project, labels: input.labels || [], assignee: input.assignee, references: [], active_sessions: [], created_at: now, updated_at: now, resource_count: 0 };
    const mutation = store.optimisticCreate(optimistic);
    try { const created = await createTask(workspace, input); store.acknowledge(mutation, created.cursor, toBoardTask(created)); navigateTask(created.id); }
    catch (cause) { store.fail(mutation, cause instanceof Error ? cause.message : String(cause)); throw cause; }
  };

  const retryMutation = (mutation: PendingMutation) => {
    store.dismiss(mutation.id);
    if (mutation.kind === 'patch' && mutation.patch) void performPatch(mutation.taskId, mutation.patch as TaskPatch).catch(() => undefined);
    else if (mutation.created) void performCreate({ title: mutation.created.title, status: mutation.created.status, assignee: mutation.created.assignee, labels: mutation.created.labels, project: mutation.created.project }).catch(() => undefined);
  };

  const updatePreferences = (change: (value: Preferences) => Preferences) => setPreferences((current) => current ? change(current) : current);
  const statuses = allStatuses(snapshot.config, snapshot.tasks);
  useBoardKeys({ tasks: visibleTasks, statuses, selected, onSelect: setSelected, onOpen: navigateTask, onMove: moveTask, onPalette: () => setPaletteOpen(true), onCreate: () => setCreateOpen(true), onFilter: () => search.current?.focus(), onAssign: (task) => navigateTask(task.id), onLabel: (task) => navigateTask(task.id), onWaitView: () => updatePreferences((value) => ({ ...value, filters: { ...value.filters, states: value.filters.states.includes('waiting') ? [] : ['waiting'] } })) });

  const paletteActions: PaletteAction[] = useMemo(() => [
    { id: 'create', label: 'Create task', group: 'Tasks', shortcut: 'C', run: () => setCreateOpen(true) },
    { id: 'open', label: 'Open selected task', group: 'Tasks', shortcut: 'Enter', run: () => selected && navigateTask(selected) },
    { id: 'filter', label: 'Focus filter', group: 'View', shortcut: '/', run: () => search.current?.focus() },
    { id: 'assign', label: 'Edit selected assignee', group: 'Tasks', shortcut: 'A', run: () => selected && navigateTask(selected) },
    { id: 'label', label: 'Edit selected labels', group: 'Tasks', shortcut: 'L', run: () => selected && navigateTask(selected) },
    { id: 'waiting', label: 'Toggle waiting tasks view', group: 'View', shortcut: 'W', run: () => updatePreferences((value) => ({ ...value, filters: { ...value.filters, states: value.filters.states.includes('waiting') ? [] : ['waiting'] } })) },
    { id: 'board', label: 'Board view', group: 'View', run: () => updatePreferences((value) => ({ ...value, view: 'board' })) },
    { id: 'list', label: 'List view', group: 'View', run: () => updatePreferences((value) => ({ ...value, view: 'list' })) },
    ...workspaces.map((item) => ({ id: `workspace-${item.name}`, label: `Switch to ${item.name}`, group: 'Workspaces', run: () => switchWorkspace(item.name) })),
    ...statuses.map((status, index) => ({ id: `move-${status}`, label: `Move selected to ${humanize(status)}`, group: 'Move', shortcut: index < 9 ? `M ${index + 1}` : undefined, run: () => { const task = snapshot.tasks.find((item) => item.id === selected); if (task) moveTask(task, status); } })),
  ], [selected, navigateTask, workspaces, statuses, snapshot.tasks, moveTask]); // eslint-disable-line react-hooks/exhaustive-deps

  const activeWorkspace = workspaces.find((item) => item.name === workspace);
  if (!workspace) return <div className="boot-screen"><span className="brand-mark">D</span><p>{notice || 'Loading Docket…'}</p></div>;
  if (!preferences) return <div className="boot-screen"><span className="brand-mark">D</span><p>Opening {workspace}…</p></div>;

  return <div className="app-shell">
    <header className="topbar"><a className="brand" href={explorerPath(workspace)} onClick={(event) => { event.preventDefault(); closeTask(); }}><span className="brand-mark">D</span><b>Docket</b></a>
      <select className="workspace-select" aria-label="Workspace" value={workspace} onChange={(event) => switchWorkspace(event.target.value)}>{workspaces.map((item) => <option key={item.name} value={item.name}>{item.name}{item.state !== 'watching' ? ` · ${item.state}` : ''}</option>)}</select>
      <div className={`connection ${snapshot.connection}`}><i />{snapshot.connection === 'open' ? 'Live' : snapshot.connection}</div>
      <div className="topbar-spacer" /><a className="quiet-link" href={classicPath(workspace, routeTask)}>Classic</a><button className="command-button" onClick={() => setPaletteOpen(true)}>Search commands <kbd>⌘K</kbd></button><button className="primary-button" onClick={() => setCreateOpen(true)}>New task</button>
    </header>
    <section className="toolbar"><div><h1>{activeWorkspace?.name || workspace}</h1><p>{visibleTasks.length} of {snapshot.tasks.length} tasks</p></div><div className="toolbar-actions">
      <input ref={search} className="search-input" type="search" placeholder="Filter tasks…" value={preferences.filters.query} onChange={(event) => updatePreferences((value) => ({ ...value, filters: { ...value.filters, query: event.target.value } }))} />
      <button aria-pressed={preferences.view === 'board'} onClick={() => updatePreferences((value) => ({ ...value, view: 'board' }))}>Board</button><button aria-pressed={preferences.view === 'list'} onClick={() => updatePreferences((value) => ({ ...value, view: 'list' }))}>List</button>
      <select aria-label="Task order" value={preferences.order} onChange={(event) => updatePreferences((value) => ({ ...value, order: event.target.value as Preferences['order'] }))}><option value="updated-desc">Recently updated</option><option value="updated-asc">Least recently updated</option><option value="created-desc">Newest created</option><option value="created-asc">Oldest created</option><option value="id-asc">ID ascending</option><option value="id-desc">ID descending</option><option value="title-asc">Title A–Z</option><option value="title-desc">Title Z–A</option></select>
      <FilterMenu tasks={snapshot.tasks} preferences={preferences} config={snapshot.config} update={updatePreferences} />
      <ViewMenu statuses={statuses} preferences={preferences} update={updatePreferences} />
    </div></section>
    <div className="notices">{(notice || activeWorkspace?.last_error) && <div className="notice error-banner">{notice || activeWorkspace?.last_error}</div>}
      {snapshot.pending.filter((item) => item.failed).map((mutation) => <div className="notice error-banner" key={mutation.id}>Mutation failed · {mutation.failed}<span><button onClick={() => retryMutation(mutation)}>Retry</button><button onClick={() => store.dismiss(mutation.id)}>Dismiss</button></span></div>)}</div>
    <main className="explorer">{visibleTasks.length ? (preferences.view === 'board' ? <BoardView workspace={workspace} tasks={visibleTasks} config={snapshot.config} preferences={preferences} selected={selected} live={snapshot.live} onSelect={navigateTask} onMove={moveTask} /> : <ListView tasks={visibleTasks} selected={selected} onSelect={navigateTask} />) : <div className="empty-state"><h2>No tasks match this view</h2><p>Clear filters or create a task.</p><button onClick={() => updatePreferences((value) => ({ ...value, filters: { query: '', statuses: [], assignees: [], labels: [], projects: [], states: [] }, hiddenStatuses: [] }))}>Clear filters</button></div>}</main>
    <footer className="statusbar"><label>Acting as <input maxLength={100} value={actor} onChange={(event) => { setActorState(event.target.value); setActor(event.target.value); }} /></label><span>{activeFilterCount(preferences.filters)} active filters</span><span>J/K navigate · Enter open · M + lane move</span></footer>
    <TaskDetailPanel workspace={workspace} taskId={routeTask} open={Boolean(routeTask)} config={snapshot.config} live={snapshot.live} summaryUpdatedAt={snapshot.tasks.find((task) => task.id === routeTask)?.updated_at} onClose={closeTask} onPatch={performPatch} onCursor={() => undefined} onDraftChange={setTaskDraft} />
    <CreateTaskDialog open={createOpen} config={snapshot.config} onOpenChange={setCreateOpen} onCreate={performCreate} />
    <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} actions={paletteActions} />
  </div>;
}

function FilterMenu({ tasks, preferences, config, update }: { tasks: BoardTask[]; preferences: Preferences; config: any; update(change: (value: Preferences) => Preferences): void }) {
  const options = { statuses: allStatuses(config, tasks), assignees: [...new Set(tasks.map((task) => task.assignee || ''))], labels: [...new Set(tasks.flatMap((task) => task.labels))], projects: [...new Set(tasks.map((task) => task.project || ''))], states: ['open', 'terminal', 'waiting'] };
  return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button>Filter{activeFilterCount(preferences.filters) ? ` · ${activeFilterCount(preferences.filters)}` : ''}</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="menu-content filter-menu" align="end">
    {Object.entries(options).map(([key, values]) => <DropdownMenu.Sub key={key}><DropdownMenu.SubTrigger className="menu-item">{humanize(key)}</DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="menu-content">{values.map((item) => { const selected = (preferences.filters as any)[key].includes(item); return <DropdownMenu.CheckboxItem className="menu-item" checked={selected} key={item || 'empty'} onCheckedChange={(checked) => update((value) => ({ ...value, filters: { ...value.filters, [key]: checked ? [...new Set([...(value.filters as any)[key], item])] : (value.filters as any)[key].filter((entry: string) => entry !== item) } }))}><DropdownMenu.ItemIndicator>✓</DropdownMenu.ItemIndicator>{item || `No ${key.slice(0, -1)}`}</DropdownMenu.CheckboxItem>; })}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>)}
    <DropdownMenu.Separator className="menu-separator" /><DropdownMenu.Item className="menu-item" onSelect={() => update((value) => ({ ...value, filters: { query: '', statuses: [], assignees: [], labels: [], projects: [], states: [] } }))}>Clear filters</DropdownMenu.Item>
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>;
}

function ViewMenu({ statuses, preferences, update }: { statuses: string[]; preferences: Preferences; update(change: (value: Preferences) => Preferences): void }) {
  return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button>View</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="menu-content" align="end">
    <DropdownMenu.CheckboxItem className="menu-item" checked={preferences.showEmpty} onCheckedChange={(checked) => update((value) => ({ ...value, showEmpty: Boolean(checked) }))}>Show empty lanes</DropdownMenu.CheckboxItem>
    {statuses.map((status) => <DropdownMenu.CheckboxItem className="menu-item" key={status} checked={!preferences.hiddenStatuses.includes(status)} onCheckedChange={(checked) => update((value) => ({ ...value, hiddenStatuses: checked ? value.hiddenStatuses.filter((item) => item !== status) : [...new Set([...value.hiddenStatuses, status])] }))}>Lane · {humanize(status)}</DropdownMenu.CheckboxItem>)}
    <DropdownMenu.Separator className="menu-separator" />
    {Object.keys(preferences.fields).map((field) => <DropdownMenu.CheckboxItem className="menu-item" key={field} checked={(preferences.fields as any)[field]} onCheckedChange={(checked) => update((value) => ({ ...value, fields: { ...value.fields, [field]: Boolean(checked) } }))}>Show {humanize(field)}</DropdownMenu.CheckboxItem>)}
    <DropdownMenu.Separator className="menu-separator" /><DropdownMenu.Label className="menu-label">Theme</DropdownMenu.Label><DropdownMenu.RadioGroup value={preferences.theme} onValueChange={(next) => update((value) => ({ ...value, theme: next as Preferences['theme'] }))}>{(['system', 'light', 'dark'] as const).map((theme) => <DropdownMenu.RadioItem key={theme} className="menu-item" value={theme}><DropdownMenu.ItemIndicator>●</DropdownMenu.ItemIndicator>{humanize(theme)}</DropdownMenu.RadioItem>)}</DropdownMenu.RadioGroup>
    <DropdownMenu.Separator className="menu-separator" /><DropdownMenu.Item className="menu-item" onSelect={() => { const name = prompt('Saved view name'); if (name) update((value) => ({ ...value, savedViews: [...value.savedViews, { name, filters: value.filters, order: value.order, view: value.view }].slice(-20) })); }}>Save current view…</DropdownMenu.Item>
    {preferences.savedViews.map((saved) => <DropdownMenu.Item className="menu-item" key={saved.name} onSelect={() => update((value) => ({ ...value, filters: saved.filters, order: saved.order, view: saved.view }))}>{saved.name}</DropdownMenu.Item>)}
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>;
}

function applyTheme(theme: Preferences['theme']) { document.documentElement.dataset.theme = theme === 'system' ? '' : theme; }
function humanize(value: string) { return value.replace(/[._-]+/g, ' ').replace(/\b\w/g, (letter) => letter.toUpperCase()); }
