export type Route = { workspace: string; task: string; valid: boolean; legacy: boolean };

export const explorerPath = (workspace: string) => `/workspaces/${encodeURIComponent(workspace)}`;
export const taskRoutePath = (workspace: string, task: string) => `${explorerPath(workspace)}/tasks/${encodeURIComponent(task)}`;
export const classicPath = (workspace: string, task = '') => task ? `/classic/workspaces/${encodeURIComponent(workspace)}/tasks/${encodeURIComponent(task)}` : `/classic/workspaces/${encodeURIComponent(workspace)}`;

export function parseRoute(location: Pick<Location, 'pathname' | 'search'>): Route {
  const search = new URLSearchParams(location.search);
  const legacyWorkspace = search.get('workspace') || '';
  const legacyTask = search.get('task') || '';
  if (legacyWorkspace || legacyTask) return { workspace: legacyWorkspace, task: legacyTask, valid: true, legacy: true };
  const parts = location.pathname.split('/').filter(Boolean);
  if (parts[0] === 'next') parts.shift();
  if (!parts.length) return { workspace: '', task: '', valid: true, legacy: false };
  if (parts[0] !== 'workspaces' || (parts.length !== 2 && parts.length !== 4) || (parts.length === 4 && parts[2] !== 'tasks')) {
    return { workspace: '', task: '', valid: false, legacy: false };
  }
  try {
    return { workspace: decodeURIComponent(parts[1]), task: parts.length === 4 ? decodeURIComponent(parts[3]) : '', valid: true, legacy: false };
  } catch {
    return { workspace: '', task: '', valid: false, legacy: false };
  }
}

export function resolveRoute(route: Route, workspaces: string[], preferred = '') {
  const names = new Set(workspaces);
  const fallback = names.has(preferred) ? preferred : workspaces[0] || '';
  if (!route.valid) return { workspace: fallback, task: '', valid: false };
  const workspace = route.workspace || fallback;
  if (!names.has(workspace)) return { workspace: fallback, task: '', valid: false };
  return { workspace, task: route.task, valid: true };
}

export const shouldHandleLink = (event: Pick<MouseEvent, 'button' | 'defaultPrevented' | 'metaKey' | 'ctrlKey' | 'shiftKey' | 'altKey'>) => event.button === 0 && !event.defaultPrevented && !event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey;
