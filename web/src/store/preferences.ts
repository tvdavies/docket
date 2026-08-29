import type { BoardTask, StreamConfig } from '../types';

export type Order = 'updated-desc' | 'updated-asc' | 'created-desc' | 'created-asc' | 'id-asc' | 'id-desc' | 'title-asc' | 'title-desc';
export type ViewMode = 'board' | 'list';
export type Filters = { query: string; statuses: string[]; assignees: string[]; labels: string[]; projects: string[]; states: string[] };
export type SavedView = { name: string; filters: Filters; order: Order; view: ViewMode };
export type Preferences = {
  version: 2;
  view: ViewMode;
  showEmpty: boolean;
  hiddenStatuses: string[];
  order: Order;
  filters: Filters;
  fields: { assignee: boolean; labels: boolean; project: boolean; references: boolean; updated: boolean };
  savedViews: SavedView[];
  theme: 'system' | 'light' | 'dark';
};

const orders = new Set<Order>(['updated-desc', 'updated-asc', 'created-desc', 'created-asc', 'id-asc', 'id-desc', 'title-asc', 'title-desc']);
const emptyFilters = (): Filters => ({ query: '', statuses: [], assignees: [], labels: [], projects: [], states: [] });

export function allStatuses(config: StreamConfig, tasks: BoardTask[]) {
  const configured = [...config.statuses];
  const unknown = tasks.map((task) => task.status).filter((status) => status && !configured.includes(status));
  return [...configured, ...new Set(unknown)];
}

export function defaultPreferences(mobile = false): Preferences {
  return {
    version: 2,
    view: mobile ? 'list' : 'board',
    showEmpty: true,
    hiddenStatuses: [],
    order: 'updated-desc',
    filters: emptyFilters(),
    fields: { assignee: true, labels: true, project: false, references: true, updated: true },
    savedViews: [],
    theme: 'system',
  };
}

export function loadPreferences(workspace: string, config: StreamConfig, tasks: BoardTask[]): Preferences {
  let input: any = {};
  try { input = JSON.parse(localStorage.getItem(`docket.explorer.v2.${workspace}`) || localStorage.getItem(`docket.explorer.v1.${workspace}`) || '{}'); } catch { input = {}; }
  const defaults = defaultPreferences(matchMedia('(max-width: 720px)').matches);
  const filters = input.filters || {};
  const statuses = allStatuses(config, tasks);
  const available = {
    statuses: new Set(statuses),
    assignees: new Set(tasks.map((task) => task.assignee || '')),
    labels: new Set(tasks.flatMap((task) => task.labels || [])),
    projects: new Set(tasks.map((task) => task.project || '')),
    states: new Set(['open', 'terminal', 'waiting']),
  };
  const clean = (values: unknown, key: keyof typeof available) => [...new Set(Array.isArray(values) ? values.map(String) : [])].filter((value) => available[key].has(value));
  return {
    version: 2,
    view: input.view === 'list' ? 'list' : input.view === 'board' ? 'board' : defaults.view,
    showEmpty: input.showEmpty !== false,
    hiddenStatuses: clean(input.hiddenStatuses, 'statuses'),
    order: orders.has(input.order) ? input.order : defaults.order,
    filters: {
      query: String(filters.query || ''),
      statuses: clean(filters.statuses, 'statuses'),
      assignees: clean(filters.assignees, 'assignees'),
      labels: clean(filters.labels, 'labels'),
      projects: clean(filters.projects, 'projects'),
      states: clean(filters.states, 'states'),
    },
    fields: Object.fromEntries(Object.entries(defaults.fields).map(([key, fallback]) => [key, typeof input.fields?.[key] === 'boolean' ? input.fields[key] : fallback])) as Preferences['fields'],
    savedViews: Array.isArray(input.savedViews) ? input.savedViews.slice(0, 20).filter((saved: any) => saved && String(saved.name || '').trim()).map((saved: any) => ({
      name: String(saved.name).slice(0, 80),
      view: saved.view === 'list' ? 'list' : 'board',
      order: orders.has(saved.order) ? saved.order : defaults.order,
      filters: {
        query: String(saved.filters?.query || ''), statuses: clean(saved.filters?.statuses, 'statuses'), assignees: clean(saved.filters?.assignees, 'assignees'),
        labels: clean(saved.filters?.labels, 'labels'), projects: clean(saved.filters?.projects, 'projects'), states: clean(saved.filters?.states, 'states'),
      },
    })) : [],
    theme: ['system', 'light', 'dark'].includes(input.theme) ? input.theme : 'system',
  };
}

export function savePreferences(workspace: string, value: Preferences) {
  try { localStorage.setItem(`docket.explorer.v2.${workspace}`, JSON.stringify(value)); } catch { /* optional */ }
}

export function filterAndSort(tasks: BoardTask[], config: StreamConfig, preferences: Preferences) {
  const hidden = new Set(preferences.hiddenStatuses);
  const terminal = new Set(config.terminal);
  const filters = preferences.filters;
  const query = filters.query.trim().toLocaleLowerCase();
  return tasks.filter((task) => {
    if (hidden.has(task.status)) return false;
    if (query && !`${task.id} ${task.title} ${task.assignee || ''} ${task.project || ''} ${(task.labels || []).join(' ')}`.toLocaleLowerCase().includes(query)) return false;
    if (filters.statuses.length && !filters.statuses.includes(task.status)) return false;
    if (filters.assignees.length && !filters.assignees.includes(task.assignee || '')) return false;
    if (filters.projects.length && !filters.projects.includes(task.project || '')) return false;
    if (filters.labels.length && !filters.labels.some((label) => task.labels.includes(label))) return false;
    if (filters.states.length) {
      const states = [terminal.has(task.status) ? 'terminal' : 'open'];
      if (task.wait) states.push('waiting');
      if (!filters.states.some((state) => states.includes(state))) return false;
    }
    return true;
  }).sort(taskComparator(preferences.order));
}

export function taskComparator(order: Order) {
  const [field, direction] = order.split('-');
  const sign = direction === 'desc' ? -1 : 1;
  return (left: BoardTask, right: BoardTask) => {
    let a: string | number; let b: string | number;
    if (field === 'updated') { a = Date.parse(left.updated_at) || 0; b = Date.parse(right.updated_at) || 0; }
    else if (field === 'created') { a = Date.parse(left.created_at) || 0; b = Date.parse(right.created_at) || 0; }
    else if (field === 'title') { a = left.title.toLocaleLowerCase(); b = right.title.toLocaleLowerCase(); }
    else { a = left.id; b = right.id; }
    const compared = typeof a === 'number' ? a - Number(b) : a.localeCompare(String(b));
    return compared ? compared * sign : left.id.localeCompare(right.id);
  };
}

export const activeFilterCount = (filters: Filters) => (filters.query.trim() ? 1 : 0) + ['statuses', 'assignees', 'labels', 'projects', 'states'].reduce((sum, key) => sum + filters[key as keyof Filters].length, 0);
