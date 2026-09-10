export type SettingsScope = 'instance' | 'workspace' | 'status';
export type SettingsRoute = { scope: SettingsScope; workspace: string; status: string };
export type Route = { workspace: string; task: string; valid: boolean; legacy: boolean; settings?: SettingsRoute };

export const explorerPath = (workspace: string) => `/workspaces/${encodeURIComponent(workspace)}`;
export const taskRoutePath = (workspace: string, task: string) => `${explorerPath(workspace)}/tasks/${encodeURIComponent(task)}`;
export const classicPath = (workspace: string, task = '') => task ? `/classic/workspaces/${encodeURIComponent(workspace)}/tasks/${encodeURIComponent(task)}` : `/classic/workspaces/${encodeURIComponent(workspace)}`;
export const instanceSettingsPath = () => '/settings/plugins';
export const workspaceSettingsPath = (workspace: string) => `${explorerPath(workspace)}/settings/plugins`;
export const statusSettingsPath = (workspace: string, status: string) => `${workspaceSettingsPath(workspace)}/statuses/${encodeURIComponent(status)}`;
export const settingsPath = (route: SettingsRoute) => route.scope === 'instance' ? instanceSettingsPath() : route.scope === 'workspace' ? workspaceSettingsPath(route.workspace) : statusSettingsPath(route.workspace, route.status);

const invalid: Route = { workspace: '', task: '', valid: false, legacy: false };

export function parseRoute(location: Pick<Location, 'pathname' | 'search'>): Route {
  const search = new URLSearchParams(location.search);
  const legacyWorkspace = search.get('workspace') || '';
  const legacyTask = search.get('task') || '';
  if (legacyWorkspace || legacyTask) return { workspace: legacyWorkspace, task: legacyTask, valid: true, legacy: true };
  const parts = location.pathname.split('/').filter(Boolean);
  if (parts[0] === 'next') parts.shift();
  if (!parts.length) return { workspace: '', task: '', valid: true, legacy: false };
  if (parts[0] === 'settings') {
    return parts.length === 2 && parts[1] === 'plugins' ? { workspace: '', task: '', valid: true, legacy: false, settings: { scope: 'instance', workspace: '', status: '' } } : invalid;
  }
  if (parts[0] !== 'workspaces' || parts.length < 2) return invalid;
  try {
    const workspace = decodeURIComponent(parts[1]);
    if (parts.length === 2) return { workspace, task: '', valid: true, legacy: false };
    if (parts.length === 4 && parts[2] === 'tasks') return { workspace, task: decodeURIComponent(parts[3]), valid: true, legacy: false };
    if (parts[2] === 'settings' && parts[3] === 'plugins') {
      if (parts.length === 4) return { workspace, task: '', valid: true, legacy: false, settings: { scope: 'workspace', workspace, status: '' } };
      if (parts.length === 6 && parts[4] === 'statuses' && parts[5]) return { workspace, task: '', valid: true, legacy: false, settings: { scope: 'status', workspace, status: decodeURIComponent(parts[5]) } };
    }
    return invalid;
  } catch {
    return invalid;
  }
}

export type ResolvedRoute = { workspace: string; task: string; valid: boolean; settings?: SettingsRoute };

/**
 * Chooses the board workspace for a parsed route. Settings routes survive an
 * unknown workspace: the settings page explains the missing registration
 * instead of the app silently redirecting to another board.
 */
export function resolveRoute(route: Route, workspaces: string[], preferred = ''): ResolvedRoute {
  const names = new Set(workspaces);
  const fallback = names.has(preferred) ? preferred : workspaces[0] || '';
  if (!route.valid) return { workspace: fallback, task: '', valid: false };
  if (route.settings) {
    const workspace = route.settings.workspace && names.has(route.settings.workspace) ? route.settings.workspace : fallback;
    return { workspace, task: '', valid: true, settings: route.settings };
  }
  const workspace = route.workspace || fallback;
  if (!names.has(workspace)) return { workspace: fallback, task: '', valid: false };
  return { workspace, task: route.task, valid: true };
}

export const shouldHandleLink = (event: Pick<MouseEvent, 'button' | 'defaultPrevented' | 'metaKey' | 'ctrlKey' | 'shiftKey' | 'altKey'>) => event.button === 0 && !event.defaultPrevented && !event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey;
