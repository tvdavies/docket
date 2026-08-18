const $ = (selector) => document.querySelector(selector);

const elements = {
  workspace: $('#workspace-select'),
  workspacePath: $('#workspace-path'),
  actor: $('#actor-input'),
  refresh: $('#refresh-button'),
  newTask: $('#new-task-button'),
  board: $('#board'),
  summary: $('#board-summary'),
  notice: $('#notice'),
  connectionDot: $('#connection-dot'),
  connectionLabel: $('#connection-label'),
  scrim: $('#drawer-scrim'),
  drawer: $('#task-drawer'),
  closeDrawer: $('#close-drawer'),
  detailID: $('#detail-id'),
  detailHeading: $('#detail-heading'),
  detailLoading: $('#detail-loading'),
  detailContent: $('#detail-content'),
  taskForm: $('#task-form'),
  title: $('#detail-title'),
  status: $('#detail-status'),
  assignee: $('#detail-assignee'),
  labels: $('#detail-labels'),
  description: $('#detail-description'),
  saveState: $('#save-state'),
  saveTask: $('#save-task-button'),
  waitSection: $('#wait-section'),
  waitKind: $('#wait-kind'),
  waitReason: $('#wait-reason'),
  waitReference: $('#wait-reference'),
  resolveWaitForm: $('#resolve-wait-form'),
  waitResult: $('#wait-result'),
  waitComment: $('#wait-comment'),
  references: $('#references'),
  referenceCount: $('#reference-count'),
  relationships: $('#relationships'),
  attachments: $('#attachments'),
  attachmentCount: $('#attachment-count'),
  activity: $('#activity'),
  activityCount: $('#activity-count'),
  commentForm: $('#comment-form'),
  commentText: $('#comment-text'),
  newDialog: $('#new-task-dialog'),
  newForm: $('#new-task-form'),
  newTitle: $('#new-title'),
  newStatus: $('#new-status'),
  newAssignee: $('#new-assignee'),
  newLabels: $('#new-labels'),
  newDescription: $('#new-description'),
  closeNew: $('#close-new-task'),
  cancelNew: $('#cancel-new-task'),
  toasts: $('#toast-region'),
};

const state = {
  workspaces: [],
  workspace: '',
  board: null,
  selectedTask: null,
  dragging: false,
  dirty: false,
  editGeneration: 0,
  detailRequest: 0,
  workspaceRefreshes: 0,
};

function createElement(tag, className, text) {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (text !== undefined) element.textContent = text;
  return element;
}

function readStorage(key, fallback = '') {
  try { return localStorage.getItem(key) || fallback; } catch { return fallback; }
}

function writeStorage(key, value) {
  try { localStorage.setItem(key, value); } catch { /* storage is optional */ }
}

function currentActor() {
  return elements.actor.value.trim() || 'web';
}

async function api(path, options = {}) {
  const headers = new Headers(options.headers || {});
  if (options.body !== undefined) {
    headers.set('Content-Type', 'application/json');
    headers.set('X-Docket-Actor', currentActor());
  }
  const response = await fetch(path, { cache: 'no-store', ...options, headers });
  const contentType = response.headers.get('content-type') || '';
  const payload = contentType.includes('application/json') ? await response.json() : null;
  if (!response.ok) {
    throw new Error(payload?.error || `${response.status} ${response.statusText}`);
  }
  return payload;
}

function setConnection(kind, label) {
  elements.connectionDot.className = `connection-dot ${kind}`;
  elements.connectionLabel.textContent = label;
}

function showNotice(message, isError = false) {
  elements.notice.hidden = !message;
  elements.notice.textContent = message || '';
  elements.notice.className = `notice${isError ? ' error' : ''}`;
}

function toast(message, isError = false) {
  const item = createElement('div', `toast${isError ? ' error' : ''}`, message);
  elements.toasts.append(item);
  window.setTimeout(() => item.remove(), 3600);
}

async function loadWorkspaces({ preserve = true } = {}) {
  try {
    const rows = await api('/api/workspaces');
    state.workspaces = rows;
    const previous = preserve ? (state.workspace || readStorage('docket.workspace')) : '';
    state.workspace = rows.some((row) => row.name === previous) ? previous : (rows[0]?.name || '');
    renderWorkspaceSelect();
    setConnection('live', rows.length === 1 ? '1 workspace' : `${rows.length} workspaces`);
    elements.newTask.disabled = !state.workspace;
    if (!state.workspace) {
      state.board = null;
      elements.workspacePath.textContent = 'Run docket init in the directory you want to use.';
      elements.summary.textContent = '';
      renderEmptyBoard('No workspaces registered. Run docket init in a workspace directory.');
      return;
    }
    writeStorage('docket.workspace', state.workspace);
    updateWorkspaceHeader();
  } catch (error) {
    setConnection('error', 'Service disconnected');
    showNotice(`Could not load workspaces: ${error.message}`, true);
  }
}

function renderWorkspaceSelect() {
  elements.workspace.replaceChildren();
  if (!state.workspaces.length) {
    const option = createElement('option', '', 'No workspaces');
    option.value = '';
    elements.workspace.append(option);
    return;
  }
  for (const workspace of state.workspaces) {
    const option = createElement('option', '', `${workspace.name}${workspace.state === 'watching' ? '' : ` · ${workspace.state}`}`);
    option.value = workspace.name;
    option.selected = workspace.name === state.workspace;
    elements.workspace.append(option);
  }
}

function updateWorkspaceHeader() {
  const workspace = state.workspaces.find((row) => row.name === state.workspace);
  elements.workspacePath.textContent = workspace?.path || '';
  if (workspace?.state && workspace.state !== 'watching') {
    showNotice(workspace.last_error || `Workspace is ${workspace.state}.`, true);
  }
}

async function loadBoard({ quiet = false } = {}) {
  if (!state.workspace || state.dragging) return;
  const requestedWorkspace = state.workspace;
  try {
    if (!quiet) setConnection('', 'Refreshing');
    const board = await api(`/api/workspaces/${encodeURIComponent(requestedWorkspace)}/board`);
    if (state.workspace !== requestedWorkspace) return;
    state.board = board;
    renderBoard();
    showNotice('');
    setConnection('live', 'Live');
  } catch (error) {
    if (state.workspace !== requestedWorkspace) return;
    setConnection('error', 'Board unavailable');
    showNotice(`Could not load board: ${error.message}`, true);
    if (!state.board) renderEmptyBoard('This workspace board could not be loaded.');
  }
}

function renderEmptyBoard(message) {
  elements.board.replaceChildren(createElement('div', 'empty-board', message));
}

function renderBoard() {
  if (!state.board) return;
  const configured = [...state.board.statuses];
  const unknown = state.board.tasks.map((task) => task.status).filter((status) => status && !configured.includes(status));
  const statuses = [...configured, ...new Set(unknown)];
  const terminal = new Set(state.board.terminal);
  elements.board.replaceChildren();

  if (!statuses.length) {
    renderEmptyBoard('No statuses are configured for this workspace.');
    return;
  }

  for (const status of statuses) {
    const tasks = state.board.tasks.filter((task) => task.status === status);
    const column = createElement('section', `column${terminal.has(status) ? ' terminal' : ''}`);
    column.dataset.status = status;

    const header = createElement('header', 'column-header');
    const titleWrap = createElement('div', 'column-title');
    titleWrap.append(createElement('span', 'status-mark'));
    titleWrap.append(createElement('h3', '', humanize(status)));
    header.append(titleWrap, createElement('span', 'count', String(tasks.length)));

    const list = createElement('div', 'card-list');
    if (!tasks.length) list.append(createElement('div', 'empty-column', 'Drop tasks here'));
    for (const task of tasks) list.append(renderTaskCard(task));
    column.append(header, list);

    column.addEventListener('dragover', (event) => {
      event.preventDefault();
      if (event.dataTransfer) event.dataTransfer.dropEffect = 'move';
      column.classList.add('drag-over');
    });
    column.addEventListener('dragleave', (event) => {
      if (!column.contains(event.relatedTarget)) column.classList.remove('drag-over');
    });
    column.addEventListener('drop', async (event) => {
      event.preventDefault();
      column.classList.remove('drag-over');
      const id = event.dataTransfer?.getData('text/plain');
      if (id) await moveTask(id, status);
    });
    elements.board.append(column);
  }

  const total = state.board.tasks.length;
  const open = state.board.tasks.filter((task) => !terminal.has(task.status)).length;
  elements.summary.textContent = `${total} task${total === 1 ? '' : 's'} · ${open} open`;
}

function renderTaskCard(task) {
  const card = createElement('article', 'task-card');
  card.draggable = true;
  card.tabIndex = 0;
  card.dataset.task = task.id;

  const top = createElement('div', 'card-top');
  top.append(createElement('span', 'task-id', task.id));
  if (task.wait) top.append(createElement('span', 'wait-badge', `Waiting · ${humanize(task.wait.kind)}`));
  card.append(top, createElement('div', 'card-title', task.title));

  const meta = createElement('div', 'card-meta');
  for (const label of task.labels || []) meta.append(createElement('span', 'tag', label));
  for (const reference of task.references || []) meta.append(createElement('span', 'reference-chip', humanize(reference.kind)));
  if (task.assignee) meta.append(createElement('span', 'assignee', task.assignee));
  card.append(meta);

  card.addEventListener('click', () => openTask(task.id));
  card.addEventListener('keydown', (event) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      openTask(task.id);
    }
  });
  card.addEventListener('dragstart', (event) => {
    state.dragging = true;
    card.classList.add('dragging');
    event.dataTransfer?.setData('text/plain', task.id);
    if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move';
  });
  card.addEventListener('dragend', () => {
    state.dragging = false;
    card.classList.remove('dragging');
    document.querySelectorAll('.drag-over').forEach((node) => node.classList.remove('drag-over'));
  });
  return card;
}

async function moveTask(id, status) {
  const requestedWorkspace = state.workspace;
  const task = state.board?.tasks.find((item) => item.id === id);
  if (!task || task.status === status) return;
  const previous = task.status;
  task.status = status;
  renderBoard();
  try {
    await api(`/api/workspaces/${encodeURIComponent(requestedWorkspace)}/tasks/${encodeURIComponent(id)}`, {
      method: 'PATCH', body: JSON.stringify({ status }),
    });
    if (state.workspace !== requestedWorkspace) return;
    toast(`${id} moved to ${status}`);
    await loadBoard({ quiet: true });
    if (state.selectedTask?.id === id && !state.dirty) await loadTaskDetail(id);
  } catch (error) {
    if (state.workspace !== requestedWorkspace) return;
    task.status = previous;
    renderBoard();
    toast(`Move failed: ${error.message}`, true);
  }
}

async function openTask(id) {
  if (state.dirty && state.selectedTask?.id !== id && !window.confirm('Discard unsaved task changes?')) return;
  state.dirty = false;
  state.editGeneration = 0;
  elements.saveState.textContent = '';
  elements.drawer.classList.add('open');
  elements.drawer.setAttribute('aria-hidden', 'false');
  elements.scrim.hidden = false;
  elements.detailContent.hidden = true;
  elements.detailLoading.hidden = false;
  elements.detailLoading.textContent = 'Loading task…';
  elements.detailID.textContent = id;
  elements.detailHeading.textContent = 'Task details';
  await loadTaskDetail(id);
}

async function loadTaskDetail(id) {
  const requestID = ++state.detailRequest;
  const requestedWorkspace = state.workspace;
  try {
    const detail = await api(`/api/workspaces/${encodeURIComponent(requestedWorkspace)}/tasks/${encodeURIComponent(id)}`);
    if (requestID !== state.detailRequest || state.workspace !== requestedWorkspace) return;
    state.selectedTask = detail;
    state.dirty = false;
    state.editGeneration = 0;
    renderTaskDetail(detail);
  } catch (error) {
    if (requestID !== state.detailRequest || state.workspace !== requestedWorkspace) return;
    elements.detailLoading.hidden = false;
    elements.detailLoading.textContent = `Could not load task: ${error.message}`;
    elements.detailContent.hidden = true;
  }
}

function renderTaskDetail(task) {
  elements.saveTask.disabled = true;
  elements.detailID.textContent = task.id;
  elements.detailHeading.textContent = task.title;
  elements.title.value = task.title;
  fillStatusSelect(elements.status, task.status);
  elements.assignee.value = task.assignee || '';
  elements.labels.value = (task.labels || []).join(', ');
  elements.description.value = task.description || '';
  elements.saveState.textContent = task.project ? `Project ${task.project.id}` : '';
  renderWait(task.wait);
  renderReferences(task.references || []);
  renderRelationships(task.relationships || {});
  renderAttachments(task.attachments || []);
  renderActivity(task.activity || []);
  elements.detailLoading.hidden = true;
  elements.detailContent.hidden = false;
}

function fillStatusSelect(select, selected) {
  select.replaceChildren();
  const statuses = [...(state.board?.statuses || [])];
  if (selected && !statuses.includes(selected)) statuses.push(selected);
  for (const status of statuses) {
    const option = createElement('option', '', humanize(status));
    option.value = status;
    option.selected = status === selected;
    select.append(option);
  }
}

function renderWait(wait) {
  elements.waitSection.hidden = !wait;
  elements.waitResult.value = '';
  elements.waitComment.value = '';
  if (!wait) return;
  elements.waitKind.textContent = humanize(wait.kind);
  elements.waitReason.textContent = wait.reason || '';
  const safeURL = safeReferenceURL(wait.reference);
  elements.waitReference.hidden = !safeURL;
  elements.waitReference.textContent = safeURL || '';
  if (safeURL) elements.waitReference.href = safeURL;
  else elements.waitReference.removeAttribute('href');
}

function renderReferences(references) {
  elements.references.replaceChildren();
  elements.referenceCount.textContent = String(references.length);
  if (!references.length) {
    elements.references.append(createElement('div', 'empty-detail', 'No external references.'));
    return;
  }
  for (const reference of [...references].reverse()) {
    const safeURL = safeReferenceURL(reference.url);
    const row = createElement(safeURL ? 'a' : 'div', 'reference-row');
    if (safeURL) {
      row.href = safeURL;
      row.target = '_blank';
      row.rel = 'noreferrer';
    }
    row.append(createElement('span', 'reference-kind', humanize(reference.kind)));
    row.append(createElement('span', 'reference-title', reference.title || reference.url));
    elements.references.append(row);
  }
}

function renderRelationships(relationships) {
  elements.relationships.replaceChildren();
  const entries = Object.entries(relationships);
  if (!entries.length) {
    elements.relationships.append(createElement('div', 'empty-detail', 'No relationships.'));
    return;
  }
  for (const [kind, refs] of entries) {
    for (const ref of refs) {
      const row = createElement('div', 'relationship-row');
      row.append(createElement('span', 'relationship-kind', kind));
      row.append(document.createTextNode(`${ref.id}${ref.title ? ` · ${ref.title}` : ''}`));
      elements.relationships.append(row);
    }
  }
}

function renderAttachments(attachments) {
  elements.attachments.replaceChildren();
  elements.attachmentCount.textContent = String(attachments.length);
  if (!attachments.length) {
    elements.attachments.append(createElement('div', 'empty-detail', 'No attachments. Use docket attach-file to add one.'));
    return;
  }
  for (const attachment of attachments) {
    const file = attachment.file || attachment.File || 'attachment';
    const caption = attachment.caption || attachment.Caption || '';
    const bytes = attachment.bytes ?? attachment.Bytes;
    const suffix = [caption, Number.isFinite(bytes) ? formatBytes(bytes) : ''].filter(Boolean).join(' · ');
    elements.attachments.append(createElement('div', 'attachment-row', `${file}${suffix ? ` — ${suffix}` : ''}`));
  }
}

function renderActivity(activity) {
  elements.activity.replaceChildren();
  elements.activityCount.textContent = String(activity.length);
  if (!activity.length) {
    elements.activity.append(createElement('div', 'empty-detail', 'No activity yet.'));
    return;
  }
  for (const entry of [...activity].reverse()) {
    const item = createElement('article', `activity-item ${entry.kind || 'event'}`);
    const meta = createElement('div', 'activity-meta');
    const identity = [entry.actor || 'system', entry.session ? `session ${entry.session}` : ''].filter(Boolean).join(' · ');
    meta.append(createElement('span', '', identity));
    meta.append(createElement('time', '', formatDate(entry.at)));
    item.append(meta, createElement('div', 'activity-type', activityTitle(entry)));
    if (entry.body) item.append(createElement('div', 'activity-body', entry.body));
    elements.activity.append(item);
  }
}

function activityTitle(entry) {
  const data = entry.data || {};
  switch (entry.type) {
    case 'comment': return 'Commented';
    case 'task.created': return 'Created task';
    case 'task.moved': return `Moved ${humanize(data.from || '')} → ${humanize(data.to || '')}`;
    case 'task.waiting': return `Waiting · ${humanize(data.wait?.kind || '')}${data.wait?.reason ? ` — ${data.wait.reason}` : ''}`;
    case 'task.resumed': return `Resumed · ${humanize(data.result || 'resolved')}`;
    case 'task.reference_added': return `Added ${humanize(data.reference?.kind || '')} reference`;
    case 'task.reference_removed': return `Removed ${humanize(data.reference?.kind || '')} reference`;
    case 'attach': return 'Attached agent session';
    case 'detach': return 'Detached agent session';
    default: return humanize(String(entry.type || 'activity').replace(/^task\./, ''));
  }
}

function closeDrawer() {
  if (state.dirty && !window.confirm('Discard unsaved task changes?')) return;
  state.detailRequest += 1;
  state.selectedTask = null;
  state.dirty = false;
  elements.drawer.classList.remove('open');
  elements.drawer.setAttribute('aria-hidden', 'true');
  elements.scrim.hidden = true;
}

function labelsFromInput(value) {
  return [...new Set(value.split(',').map((label) => label.trim()).filter(Boolean))];
}

function safeReferenceURL(value) {
  if (!value) return '';
  try {
    const parsed = new URL(value);
    if (!['http:', 'https:', 'file:'].includes(parsed.protocol.toLowerCase())) return '';
    return parsed.href;
  } catch {
    return '';
  }
}

function humanize(value) {
  return String(value || '')
    .replace(/[._-]+/g, ' ')
    .replace(/\b\w/g, (character) => character.toUpperCase());
}

function formatDate(value) {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.valueOf())) return value;
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(date);
}

function formatBytes(bytes) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function openNewTask() {
  if (!state.board) return;
  elements.newForm.reset();
  fillStatusSelect(elements.newStatus, state.board.statuses[0] || '');
  elements.newDialog.showModal();
  window.setTimeout(() => elements.newTitle.focus(), 0);
}

async function submitNewTask(event) {
  event.preventDefault();
  const requestedWorkspace = state.workspace;
  const submit = elements.newForm.querySelector('button[type="submit"]');
  submit.disabled = true;
  try {
    const created = await api(`/api/workspaces/${encodeURIComponent(requestedWorkspace)}/tasks`, {
      method: 'POST',
      body: JSON.stringify({
        title: elements.newTitle.value,
        description: elements.newDescription.value,
        status: elements.newStatus.value,
        assignee: elements.newAssignee.value.trim(),
        labels: labelsFromInput(elements.newLabels.value),
      }),
    });
    if (state.workspace !== requestedWorkspace) return;
    elements.newDialog.close();
    toast(`${created.id} created`);
    await loadBoard({ quiet: true });
    await openTask(created.id);
  } catch (error) {
    toast(`Create failed: ${error.message}`, true);
  } finally {
    submit.disabled = false;
  }
}

async function saveTask(event) {
  event.preventDefault();
  if (!state.selectedTask) return;
  const requestedWorkspace = state.workspace;
  const requestedTask = state.selectedTask.id;
  const submit = elements.taskForm.querySelector('button[type="submit"]');
  const submittedGeneration = state.editGeneration;
  submit.disabled = true;
  elements.saveState.textContent = 'Saving…';
  try {
    const updated = await api(`/api/workspaces/${encodeURIComponent(requestedWorkspace)}/tasks/${encodeURIComponent(requestedTask)}`, {
      method: 'PATCH',
      body: JSON.stringify({
        title: elements.title.value,
        description: elements.description.value,
        status: elements.status.value,
        assignee: elements.assignee.value.trim(),
        labels: labelsFromInput(elements.labels.value),
      }),
    });
    if (state.workspace !== requestedWorkspace || state.selectedTask?.id !== requestedTask) return;
    state.selectedTask = updated;
    if (state.editGeneration === submittedGeneration) {
      state.dirty = false;
      renderTaskDetail(updated);
      toast(`${updated.id} saved`);
    } else {
      state.dirty = true;
      elements.saveTask.disabled = false;
      elements.saveState.textContent = 'Saved earlier values · newer edits remain';
      toast(`${updated.id} saved; newer edits are still unsaved`);
    }
    await loadBoard({ quiet: true });
  } catch (error) {
    elements.saveState.textContent = 'Save failed';
    toast(`Save failed: ${error.message}`, true);
  } finally {
    submit.disabled = !state.dirty;
  }
}

async function resolveWait(event) {
  event.preventDefault();
  if (!state.selectedTask?.wait) return;
  if (state.dirty && !window.confirm('Discard unsaved task edits before resolving this wait?')) return;
  const requestedWorkspace = state.workspace;
  const requestedTask = state.selectedTask.id;
  const waitID = state.selectedTask.wait.id;
  const submittedGeneration = state.editGeneration;
  const submit = elements.resolveWaitForm.querySelector('button[type="submit"]');
  submit.disabled = true;
  try {
    const note = elements.waitComment.value.trim();
    if (note) {
      await api(`/api/workspaces/${encodeURIComponent(requestedWorkspace)}/tasks/${encodeURIComponent(requestedTask)}/comments`, {
        method: 'POST', body: JSON.stringify({ text: note }),
      });
    }
    const updated = await api(`/api/workspaces/${encodeURIComponent(requestedWorkspace)}/tasks/${encodeURIComponent(requestedTask)}/wait/resolve`, {
      method: 'POST',
      body: JSON.stringify({ wait_id: waitID, result: elements.waitResult.value.trim() }),
    });
    if (state.workspace !== requestedWorkspace || state.selectedTask?.id !== requestedTask) return;
    state.selectedTask = updated;
    if (state.editGeneration === submittedGeneration) {
      state.dirty = false;
      renderTaskDetail(updated);
      toast(`${requestedTask} resumed`);
    } else {
      state.dirty = true;
      renderWait(updated.wait);
      renderReferences(updated.references || []);
      renderActivity(updated.activity || []);
      elements.saveTask.disabled = false;
      elements.saveState.textContent = 'Wait resolved · newer edits remain unsaved';
      toast(`${requestedTask} resumed; newer edits are still unsaved`);
    }
    await loadBoard({ quiet: true });
  } catch (error) {
    toast(`Could not resolve wait: ${error.message}`, true);
    if (state.workspace === requestedWorkspace && state.selectedTask?.id === requestedTask &&
        state.editGeneration === submittedGeneration && !state.dirty) {
      await loadTaskDetail(requestedTask);
    }
  } finally {
    submit.disabled = false;
  }
}

async function addComment(event) {
  event.preventDefault();
  if (!state.selectedTask) return;
  const requestedWorkspace = state.workspace;
  const requestedTask = state.selectedTask.id;
  const text = elements.commentText.value.trim();
  if (!text) return;
  const submit = elements.commentForm.querySelector('button[type="submit"]');
  submit.disabled = true;
  try {
    const updated = await api(`/api/workspaces/${encodeURIComponent(requestedWorkspace)}/tasks/${encodeURIComponent(requestedTask)}/comments`, {
      method: 'POST', body: JSON.stringify({ text }),
    });
    if (state.workspace !== requestedWorkspace || state.selectedTask?.id !== requestedTask) return;
    elements.commentText.value = '';
    state.selectedTask = updated;
    renderActivity(updated.activity || []);
    toast('Comment added');
  } catch (error) {
    toast(`Comment failed: ${error.message}`, true);
  } finally {
    submit.disabled = false;
  }
}

elements.workspace.addEventListener('change', async () => {
  if (state.dirty && !window.confirm('Discard unsaved task changes?')) {
    elements.workspace.value = state.workspace;
    return;
  }
  closeDrawerWithoutPrompt();
  if (elements.newDialog.open) elements.newDialog.close();
  state.workspace = elements.workspace.value;
  state.board = null;
  writeStorage('docket.workspace', state.workspace);
  updateWorkspaceHeader();
  renderEmptyBoard('Loading board…');
  await loadBoard();
});

elements.actor.value = readStorage('docket.actor', 'web');
elements.actor.addEventListener('change', () => writeStorage('docket.actor', currentActor()));
elements.refresh.addEventListener('click', async () => {
  await loadWorkspaces();
  await loadBoard();
});
elements.newTask.addEventListener('click', openNewTask);
elements.closeNew.addEventListener('click', () => elements.newDialog.close());
elements.cancelNew.addEventListener('click', () => elements.newDialog.close());
elements.newForm.addEventListener('submit', submitNewTask);
elements.taskForm.addEventListener('input', () => {
  state.dirty = true;
  state.editGeneration += 1;
  elements.saveTask.disabled = false;
  elements.saveState.textContent = 'Unsaved changes';
});
elements.taskForm.addEventListener('submit', saveTask);
elements.resolveWaitForm.addEventListener('submit', resolveWait);
elements.commentForm.addEventListener('submit', addComment);
elements.closeDrawer.addEventListener('click', closeDrawer);
elements.scrim.addEventListener('click', closeDrawer);
document.addEventListener('keydown', (event) => {
  if (event.key === 'Escape' && elements.drawer.classList.contains('open') && !elements.newDialog.open) closeDrawer();
});

function closeDrawerWithoutPrompt() {
  state.detailRequest += 1;
  state.selectedTask = null;
  state.dirty = false;
  elements.drawer.classList.remove('open');
  elements.drawer.setAttribute('aria-hidden', 'true');
  elements.scrim.hidden = true;
}

async function start() {
  renderEmptyBoard('Loading workspaces…');
  await loadWorkspaces({ preserve: true });
  if (state.workspace) await loadBoard();
  window.setInterval(async () => {
    if (document.hidden || state.dragging || state.dirty || elements.newDialog.open) return;
    state.workspaceRefreshes += 1;
    if (state.workspaceRefreshes % 5 === 0) await loadWorkspaces();
    await loadBoard({ quiet: true });
  }, 3000);
}

start();
