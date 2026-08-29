import type { CreateTaskInput, TaskDetail, TaskPatch, WorkspaceStatus } from '../types';

const ACTOR_KEY = 'docket.actor';

export function getActor(): string {
  try { return localStorage.getItem(ACTOR_KEY)?.trim() || 'web'; } catch { return 'web'; }
}

export function setActor(actor: string): void {
  try { localStorage.setItem(ACTOR_KEY, actor.trim() || 'web'); } catch { /* optional */ }
}

export async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers);
  if (options.body !== undefined) {
    if (!(options.body instanceof FormData)) headers.set('Content-Type', 'application/json');
    headers.set('X-Docket-Actor', getActor());
  }
  const response = await fetch(path, { cache: 'no-store', credentials: 'same-origin', ...options, headers });
  const type = response.headers.get('content-type') || '';
  const payload = type.includes('application/json') ? await response.json() : null;
  if (!response.ok) throw new Error(payload?.error || `${response.status} ${response.statusText}`);
  return payload as T;
}

export const workspacePath = (workspace: string) => `/api/workspaces/${encodeURIComponent(workspace)}`;
export const taskPath = (workspace: string, task: string, suffix = '') => `${workspacePath(workspace)}/tasks/${encodeURIComponent(task)}${suffix}`;

export const listWorkspaces = () => api<WorkspaceStatus[]>('/api/workspaces');
export const getTask = (workspace: string, task: string, signal?: AbortSignal) => api<TaskDetail>(taskPath(workspace, task), { signal });
export const createTask = (workspace: string, input: CreateTaskInput) => api<TaskDetail>(`${workspacePath(workspace)}/tasks`, { method: 'POST', body: JSON.stringify(input) });
export const patchTask = (workspace: string, task: string, patch: TaskPatch) => api<TaskDetail>(taskPath(workspace, task), { method: 'PATCH', body: JSON.stringify(patch) });
export const addComment = (workspace: string, task: string, text: string) => api<TaskDetail>(taskPath(workspace, task, '/comments'), { method: 'POST', body: JSON.stringify({ text }) });
export const resolveWait = (workspace: string, task: string, waitId: string, result: string) => api<TaskDetail>(taskPath(workspace, task, '/wait/resolve'), { method: 'POST', body: JSON.stringify({ wait_id: waitId, result }) });
export const addReference = (workspace: string, task: string, value: { kind: string; url: string; title?: string }) => api<TaskDetail>(taskPath(workspace, task, '/references'), { method: 'POST', body: JSON.stringify(value) });
export const removeReference = (workspace: string, task: string, reference: string) => api<TaskDetail>(taskPath(workspace, task, `/references/${encodeURIComponent(reference)}`), { method: 'DELETE', body: '{}' });
export const uploadAttachment = (workspace: string, task: string, file: File, caption: string) => {
  const form = new FormData(); form.set('file', file); if (caption) form.set('caption', caption);
  return api<TaskDetail>(taskPath(workspace, task, '/attachments'), { method: 'POST', body: form });
};
