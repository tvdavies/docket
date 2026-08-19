export function workspaceOptionText(workspace) {
  const state = workspace?.state && workspace.state !== 'watching' ? workspace.state : 'Watching';
  return {
    name: workspace?.name || 'Workspace',
    detail: [state, workspace?.path || ''].filter(Boolean).join(' · '),
  };
}

export function nextWorkspaceIndex(length, current, key) {
  if (!length) return -1;
  const index = current >= 0 && current < length ? current : 0;
  if (key === 'Home') return 0;
  if (key === 'End') return length - 1;
  if (key === 'ArrowDown') return (index + 1) % length;
  if (key === 'ArrowUp') return (index - 1 + length) % length;
  return index;
}
