export function buildExplorerPath(workspace) {
  return `/workspaces/${encodeURIComponent(workspace)}`;
}

export function buildTaskPath(workspace, task) {
  return `${buildExplorerPath(workspace)}/tasks/${encodeURIComponent(task)}`;
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
