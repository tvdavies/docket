export function buildExplorerPath(workspace) {
  return `/workspaces/${encodeURIComponent(workspace)}`;
}

export function buildTaskPath(workspace, task) {
  return `${buildExplorerPath(workspace)}/tasks/${encodeURIComponent(task)}`;
}

export function routeContext(workspace, task, generation) {
  return Object.freeze({ workspace, task, generation });
}

export function sameRouteContext(left, right) {
  return Boolean(left && right && left.workspace === right.workspace && left.task === right.task && left.generation === right.generation);
}

export function buildTaskAPIPath(context, suffix = '') {
  return `/api/workspaces/${encodeURIComponent(context.workspace)}/tasks/${encodeURIComponent(context.task)}${suffix}`;
}

export function resolveRoute(route, workspaceNames, fallbackWorkspace = '') {
  const names = new Set(workspaceNames || []);
  const fallback = names.has(fallbackWorkspace) ? fallbackWorkspace : (workspaceNames?.[0] || '');
  if (!route.valid) return { valid: false, workspace: fallback, task: '', legacy: false, reason: 'invalid-route' };
  const workspace = route.workspace || fallback;
  if (!workspace || !names.has(workspace)) return { valid: false, workspace: fallback, task: '', legacy: false, reason: 'unknown-workspace' };
  return { valid: true, workspace, task: route.task || '', legacy: route.legacy, reason: '' };
}

export function parseLocation(location) {
  const search = new URLSearchParams(location.search || '');
  const legacyWorkspace = search.get('workspace') || '';
  const legacyTask = search.get('task') || '';
  if (legacyWorkspace || legacyTask) {
    return { workspace: legacyWorkspace, task: legacyTask, legacy: true, valid: true };
  }
  const parts = String(location.pathname || '/').split('/').filter(Boolean);
  if (!parts.length) return { workspace: '', task: '', legacy: false, valid: true };
  if (parts[0] !== 'workspaces' || (parts.length !== 2 && parts.length !== 4)) {
    return { workspace: '', task: '', legacy: false, valid: false };
  }
  if (parts.length === 4 && parts[2] !== 'tasks') {
    return { workspace: '', task: '', legacy: false, valid: false };
  }
  try {
    return {
      workspace: decodeURIComponent(parts[1]),
      task: parts.length === 4 ? decodeURIComponent(parts[3]) : '',
      legacy: false,
      valid: true,
    };
  } catch {
    return { workspace: '', task: '', legacy: false, valid: false };
  }
}
