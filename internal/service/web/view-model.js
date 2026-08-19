export const DEFAULT_PREFERENCES = Object.freeze({
  version: 1,
  view: 'board',
  showEmpty: true,
  hiddenStatuses: [],
  order: 'updated-desc',
  filters: { query: '', statuses: [], assignees: [], labels: [], projects: [], states: [] },
});

const ORDER_VALUES = new Set([
  'updated-desc', 'updated-asc', 'created-desc', 'created-asc',
  'id-asc', 'id-desc', 'title-asc', 'title-desc',
]);

export function allStatuses(board) {
  const configured = [...(board?.statuses || [])];
  const unknown = (board?.tasks || []).map((task) => task.status).filter((status) => status && !configured.includes(status));
  return [...configured, ...new Set(unknown)];
}

export function sameBoardContent(left, right) {
  if (left === right) return true;
  if (!left || !right) return false;
  const { updated_at: leftUpdatedAt, ...leftContent } = left;
  const { updated_at: rightUpdatedAt, ...rightContent } = right;
  return JSON.stringify(leftContent) === JSON.stringify(rightContent);
}

export function normalisePreferences(input, board) {
  const source = input && input.version === 1 ? input : {};
  const filters = source.filters || {};
  const tasks = board?.tasks || [];
  const statuses = allStatuses(board);
  const available = {
    statuses: new Set(statuses),
    assignees: new Set(tasks.map((task) => task.assignee || '')),
    labels: new Set(tasks.flatMap((task) => task.labels || [])),
    projects: new Set(tasks.map((task) => task.project || '')),
    states: new Set(['open', 'terminal', 'waiting']),
  };
  const clean = (values, key) => [...new Set(Array.isArray(values) ? values : [])].filter((value) => available[key].has(value));
  return {
    version: 1,
    view: source.view === 'list' ? 'list' : 'board',
    showEmpty: source.showEmpty !== false,
    hiddenStatuses: [...new Set(Array.isArray(source.hiddenStatuses) ? source.hiddenStatuses : [])].filter((value) => available.statuses.has(value)),
    order: ORDER_VALUES.has(source.order) ? source.order : DEFAULT_PREFERENCES.order,
    filters: {
      query: String(filters.query || ''),
      statuses: clean(filters.statuses, 'statuses'),
      assignees: clean(filters.assignees, 'assignees'),
      labels: clean(filters.labels, 'labels'),
      projects: clean(filters.projects, 'projects'),
      states: clean(filters.states, 'states'),
    },
  };
}

export function filterOptions(board) {
  const tasks = board?.tasks || [];
  const unique = (values) => [...new Set(values)].sort((a, b) => String(a).localeCompare(String(b)));
  return {
    statuses: allStatuses(board),
    assignees: unique(tasks.map((task) => task.assignee || '')),
    labels: unique(tasks.flatMap((task) => task.labels || [])),
    projects: unique(tasks.map((task) => task.project || '')),
  };
}

function matchesMulti(selected, value) {
  return !selected.length || selected.includes(value);
}

export function filterAndSortTasks(board, preferences) {
  const terminal = new Set(board?.terminal || []);
  const hidden = new Set(preferences.hiddenStatuses || []);
  const filters = preferences.filters;
  const query = filters.query.trim().toLocaleLowerCase();
  const result = (board?.tasks || []).filter((task) => {
    if (hidden.has(task.status)) return false;
    if (query && !`${task.id} ${task.title}`.toLocaleLowerCase().includes(query)) return false;
    if (!matchesMulti(filters.statuses, task.status)) return false;
    if (!matchesMulti(filters.assignees, task.assignee || '')) return false;
    if (!matchesMulti(filters.projects, task.project || '')) return false;
    if (filters.labels.length && !filters.labels.some((label) => (task.labels || []).includes(label))) return false;
    if (filters.states.length) {
      const states = [];
      states.push(terminal.has(task.status) ? 'terminal' : 'open');
      if (task.wait) states.push('waiting');
      if (!filters.states.some((state) => states.includes(state))) return false;
    }
    return true;
  });
  return result.sort(taskComparator(preferences.order));
}

export function taskComparator(order) {
  const [field, direction = 'asc'] = String(order || '').split('-');
  const sign = direction === 'desc' ? -1 : 1;
  return (left, right) => {
    let a;
    let b;
    if (field === 'updated') {
      a = Date.parse(left.updated_at) || 0; b = Date.parse(right.updated_at) || 0;
    } else if (field === 'created') {
      a = Date.parse(left.created_at) || 0; b = Date.parse(right.created_at) || 0;
    } else if (field === 'title') {
      a = String(left.title).toLocaleLowerCase(); b = String(right.title).toLocaleLowerCase();
    } else {
      a = String(left.id); b = String(right.id);
    }
    const compared = typeof a === 'number' ? a - b : a.localeCompare(b);
    if (compared) return compared * sign;
    return String(left.id).localeCompare(String(right.id));
  };
}

export function groupTasks(board, preferences) {
  const tasks = filterAndSortTasks(board, preferences);
  const grouped = new Map(allStatuses(board).map((status) => [status, []]));
  for (const task of tasks) {
    if (!grouped.has(task.status)) grouped.set(task.status, []);
    grouped.get(task.status).push(task);
  }
  return grouped;
}

export function activeFilterCount(preferences) {
  const filters = preferences.filters;
  return (filters.query.trim() ? 1 : 0) + ['statuses', 'assignees', 'labels', 'projects', 'states']
    .reduce((count, key) => count + filters[key].length, 0);
}
