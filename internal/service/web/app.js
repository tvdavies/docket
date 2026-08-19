import { icon } from './icons.js';
import {
  buildExplorerPath, buildTaskAPIPath, buildTaskPath, parseLocation,
  resolveRoute, routeContext, sameRouteContext, shouldNavigateInApp,
} from './router.js';
import { activeFilterCount, allStatuses, filterAndSortTasks, filterOptions, groupTasks, normalisePreferences, sameBoardContent } from './view-model.js';
import { markdownFromElement, normalizedTitle, plainPasteText } from './markdown-edit.js';
import { nextWorkspaceIndex, workspaceIsAvailable, workspaceOptionText } from './workspace-picker.js';

const $ = (selector, root = document) => root.querySelector(selector);
const elements = {
  appShell: $('.app-shell'), workspaceButton: $('#workspace-picker-button'), workspaceName: $('#workspace-picker-name'), workspaceMenu: $('#workspace-picker-menu'), workspacePath: $('#workspace-path'), actor: $('#actor-input'), refresh: $('#refresh-button'),
  newTask: $('#new-task-button'), home: $('#home-button'), tasksCrumb: $('#tasks-crumb'), taskCrumbWrap: $('#task-crumb-wrap'), taskCrumb: $('#task-crumb'),
  explorerToolbar: $('#explorer-toolbar'), explorer: $('#explorer-root'), task: $('#task-root'), summary: $('#board-summary'), notice: $('#notice'),
  boardView: $('#board-view-button'), listView: $('#list-view-button'),
  search: $('#search-input'), filterButton: $('#filter-button'), filterCount: $('#filter-count'), filterPanel: $('#filter-panel'),
  order: $('#order-select'), viewSettingsButton: $('#view-settings-button'), viewSettingsPanel: $('#view-settings-panel'), activeFilters: $('#active-filters'),
  newDialog: $('#new-task-dialog'), newForm: $('#new-task-form'), newTitle: $('#new-title'), newStatus: $('#new-status'),
  newAssignee: $('#new-assignee'), newLabels: $('#new-labels'), newDescription: $('#new-description'),
  linkDialog: $('#link-dialog'), linkForm: $('#link-form'), linkKind: $('#link-kind'), linkURL: $('#link-url'), linkTitle: $('#link-title'),
  uploadDialog: $('#upload-dialog'), uploadForm: $('#upload-form'), uploadFile: $('#upload-file'), uploadCaption: $('#upload-caption'), toasts: $('#toast-region'),
};

const state = {
  workspaces: [], workspace: '', board: null, preferences: null, selectedTask: null, routeTask: '',
  dragging: false, detailRequest: 0, boardRequest: 0, routeGeneration: 0, refreshes: 0,
  activeEditor: null, pendingSave: false, pendingSavePromise: null, panelTrigger: null,
  workspacePickerOpen: false, workspacePickerStale: false, scroll: new Map(),
};

function el(tag, className = '', text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}
function button(className, text, iconName) {
  const node = el('button', className, text);
  node.type = 'button';
  if (iconName) node.prepend(icon(iconName));
  return node;
}
function readStorage(key, fallback = '') { try { return localStorage.getItem(key) || fallback; } catch { return fallback; } }
function writeStorage(key, value) { try { localStorage.setItem(key, value); } catch { /* optional */ } }
function currentActor() { return elements.actor.value.trim() || 'web'; }
function preferenceKey() { return `docket.explorer.v1.${state.workspace}`; }
function loadPreferences() {
  let parsed = {};
  try { parsed = JSON.parse(readStorage(preferenceKey(), '{}')); } catch { parsed = {}; }
  state.preferences = normalisePreferences(parsed, state.board);
}
function savePreferences() { writeStorage(preferenceKey(), JSON.stringify(state.preferences)); }

async function api(path, options = {}) {
  const headers = new Headers(options.headers || {});
  if (options.body !== undefined) {
    if (!(options.body instanceof FormData)) headers.set('Content-Type', 'application/json');
    headers.set('X-Docket-Actor', currentActor());
  }
  const response = await fetch(path, { cache: 'no-store', ...options, headers });
  const contentType = response.headers.get('content-type') || '';
  const payload = contentType.includes('application/json') ? await response.json() : null;
  if (!response.ok) throw new Error(payload?.error || `${response.status} ${response.statusText}`);
  return payload;
}
function currentRoute() { return routeContext(state.workspace, state.routeTask, state.routeGeneration); }
function routeIsCurrent(context) { return sameRouteContext(context, currentRoute()); }
function advanceRoute() { state.routeGeneration += 1; state.detailRequest += 1; return currentRoute(); }
function taskAPI(suffix = '') { return buildTaskAPIPath(currentRoute(), suffix); }
function showNotice(message, isError = false) { elements.notice.hidden = !message; elements.notice.textContent = message || ''; elements.notice.className = `notice${isError ? ' error' : ''}`; }
function toast(message, isError = false) { const item = el('div', `toast${isError ? ' error' : ''}`, message); elements.toasts.append(item); setTimeout(() => item.remove(), 3800); }
function humanize(value) { return String(value || '').replace(/[._-]+/g, ' ').replace(/\b\w/g, (character) => character.toUpperCase()); }
function labelsFromInput(value) { return [...new Set(String(value).split(',').map((label) => label.trim()).filter(Boolean))]; }
function safeURL(value) { try { const parsed = new URL(value); return ['http:', 'https:', 'file:'].includes(parsed.protocol) ? parsed.href : ''; } catch { return ''; } }
function formatBytes(bytes) { if (bytes < 1024) return `${bytes} B`; if (bytes < 1048576) return `${(bytes / 1024).toFixed(1)} KB`; return `${(bytes / 1048576).toFixed(1)} MB`; }
function formatDate(value) { const date = new Date(value); return Number.isNaN(date.valueOf()) ? value || '' : new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(date); }
function relativeDate(value) {
  const time = new Date(value).valueOf(); if (!Number.isFinite(time)) return '';
  const seconds = Math.round((time - Date.now()) / 1000); const absolute = Math.abs(seconds);
  const [amount, unit] = absolute < 60 ? [seconds, 'second'] : absolute < 3600 ? [Math.round(seconds / 60), 'minute'] : absolute < 86400 ? [Math.round(seconds / 3600), 'hour'] : [Math.round(seconds / 86400), 'day'];
  return new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' }).format(amount, unit);
}
function refreshRelativeTaskTimes() {
  elements.explorer.querySelectorAll('time.card-time[datetime]').forEach((node) => { node.textContent = relativeDate(node.dateTime); });
}
function labelTone(label) { let hash = 0; for (const character of label) hash = ((hash * 31) + character.codePointAt(0)) >>> 0; return `label-tone-${hash % 6}`; }
function statusTone(status, terminal = false) {
  const value = status.toLowerCase();
  if (terminal) return 'tone-green'; if (/block|fail|cancel|reject/.test(value)) return 'tone-red';
  if (/progress|doing|active/.test(value)) return 'tone-amber'; if (/review|research/.test(value)) return 'tone-violet';
  if (/ready|todo|backlog/.test(value)) return 'tone-blue'; return 'tone-gray';
}
function setMarkdown(node, html) {
  if (html) node.innerHTML = html;
  else node.append(el('p', 'empty-detail', 'No content yet.'));
  node.querySelectorAll('a').forEach((link) => { link.target = '_blank'; link.rel = 'noopener noreferrer'; });
}
function closePanels({ restoreFocus = false } = {}) {
  const trigger = state.panelTrigger;
  elements.filterPanel.hidden = true; elements.viewSettingsPanel.hidden = true;
  elements.filterButton.setAttribute('aria-expanded', 'false'); elements.viewSettingsButton.setAttribute('aria-expanded', 'false');
  state.panelTrigger = null;
  if (restoreFocus && trigger) trigger.focus();
}
function openPanel(panel, trigger) {
  closeWorkspacePicker(); closePanels(); panel.hidden = false; trigger.setAttribute('aria-expanded', 'true'); state.panelTrigger = trigger;
  requestAnimationFrame(() => panel.querySelector('input,select,button')?.focus());
}
function capturePanelFocus() {
  const active = document.activeElement;
  for (const [name, panel] of [['filter', elements.filterPanel], ['view-settings', elements.viewSettingsPanel]]) {
    if (panel.hidden || !panel.contains(active)) continue;
    const option = active.closest?.('[data-panel-group]');
    if (option) return { name, group: option.dataset.panelGroup, value: option.dataset.panelValue, action: '' };
    const action = active.closest?.('[data-panel-action]');
    if (action) return { name, group: '', value: '', action: action.dataset.panelAction };
  }
  return null;
}
function restorePanelFocus(snapshot) {
  if (!snapshot) return;
  const panel = snapshot.name === 'filter' ? elements.filterPanel : elements.viewSettingsPanel;
  if (panel.hidden) return;
  if (snapshot.group) { focusPanelOption(panel, snapshot.group, snapshot.value); return; }
  requestAnimationFrame(() => [...panel.querySelectorAll('[data-panel-action]')]
    .find((node) => node.dataset.panelAction === snapshot.action)?.focus());
}

async function loadWorkspaces() {
  state.workspaces = await api('/api/workspaces');
  if (state.workspacePickerOpen) state.workspacePickerStale = true;
  else renderWorkspacePicker();
}
function renderWorkspacePicker() {
  elements.workspaceMenu.replaceChildren();
  for (const [index, workspace] of state.workspaces.entries()) {
    const copy = workspaceOptionText(workspace); const option = button('workspace-option', '');
    option.id = `workspace-option-${index}`; option.setAttribute('role', 'option'); option.dataset.workspace = workspace.name;
    option.setAttribute('aria-selected', String(workspace.name === state.workspace));
    option.append(el('span', 'workspace-option-name', copy.name), el('span', 'workspace-option-detail', copy.detail));
    option.addEventListener('click', () => chooseWorkspace(workspace.name));
    option.addEventListener('keydown', workspaceOptionKeydown); elements.workspaceMenu.append(option);
  }
  elements.workspaceButton.disabled = !state.workspaces.length; state.workspacePickerStale = false;
}
function workspaceOptions() { return [...elements.workspaceMenu.querySelectorAll('[role="option"]')]; }
function focusWorkspaceOption(index) { const options = workspaceOptions(); options[index]?.focus(); }
function openWorkspacePicker(key = '') {
  if (!state.workspaces.length) return; closePanels(); state.workspacePickerOpen = true; elements.workspaceMenu.hidden = false; elements.workspaceButton.setAttribute('aria-expanded', 'true');
  const selected = Math.max(0, state.workspaces.findIndex((row) => row.name === state.workspace));
  const index = key === 'Home' || key === 'End' || key === 'ArrowUp' || key === 'ArrowDown' ? nextWorkspaceIndex(state.workspaces.length, selected, key) : selected;
  requestAnimationFrame(() => focusWorkspaceOption(index));
}
function closeWorkspacePicker({ restoreFocus = false } = {}) {
  state.workspacePickerOpen = false; elements.workspaceMenu.hidden = true; elements.workspaceButton.setAttribute('aria-expanded', 'false');
  if (state.workspacePickerStale) renderWorkspacePicker();
  if (restoreFocus) elements.workspaceButton.focus();
}
function workspaceOptionKeydown(event) {
  const options = workspaceOptions(); const current = options.indexOf(event.currentTarget);
  if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) { event.preventDefault(); focusWorkspaceOption(nextWorkspaceIndex(options.length, current, event.key)); }
  else if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); chooseWorkspace(event.currentTarget.dataset.workspace); }
  else if (event.key === 'Escape') { event.preventDefault(); closeWorkspacePicker({ restoreFocus: true }); }
  else if (event.key === 'Tab') closeWorkspacePicker();
}
async function chooseWorkspace(workspace) {
  closeWorkspacePicker({ restoreFocus: true });
  if (!workspaceIsAvailable(state.workspaces, workspace)) return;
  if (workspace !== state.workspace) await switchWorkspace(workspace);
}
function updateWorkspaceChrome() {
  const workspace = state.workspaces.find((row) => row.name === state.workspace); const copy = workspaceOptionText(workspace);
  elements.workspaceName.textContent = copy.name; elements.workspaceButton.title = copy.detail; elements.workspacePath.textContent = workspace?.path || '';
  elements.workspacePath.title = workspace?.path || ''; writeStorage('docket.workspace', state.workspace);
  workspaceOptions().forEach((option) => option.setAttribute('aria-selected', String(option.dataset.workspace === state.workspace)));
  if (workspace?.state && workspace.state !== 'watching') showNotice(workspace.last_error || `Workspace is ${workspace.state}.`, true);
}
async function loadBoard({ quiet = false } = {}) {
  if (!state.workspace || state.dragging) return;
  const workspace = state.workspace; const request = ++state.boardRequest;
  try {
    const board = await api(`/api/workspaces/${encodeURIComponent(workspace)}/board`);
    if (request !== state.boardRequest || workspace !== state.workspace) return;
    const contentChanged = !sameBoardContent(state.board, board);
    state.board = board;
    if (!state.preferences) loadPreferences(); else state.preferences = normalisePreferences(state.preferences, board);
    savePreferences();
    if (!state.routeTask) {
      if (!quiet || contentChanged) renderExplorer();
      else refreshRelativeTaskTimes();
    }
    showNotice('');
  } catch (error) {
    if (request !== state.boardRequest || workspace !== state.workspace) return;
    showNotice(`Could not load board: ${error.message}`, true);
    if (!state.board && !state.routeTask) renderEmptyExplorer('This workspace board could not be loaded.');
  }
}

function captureExplorerScroll() {
  const snapshot = state.scroll.get(state.workspace) || { lanes: {} };
  const viewport = $('.board-viewport', elements.explorer); if (viewport) snapshot.outer = viewport.scrollLeft;
  const list = $('.list-viewport', elements.explorer); if (list) snapshot.list = list.scrollTop;
  elements.explorer.querySelectorAll('.card-list[data-status]').forEach((node) => { snapshot.lanes[node.dataset.status] = node.scrollTop; });
  state.scroll.set(state.workspace, snapshot);
}
function restoreExplorerScroll() {
  const snapshot = state.scroll.get(state.workspace); if (!snapshot) return;
  requestAnimationFrame(() => {
    const viewport = $('.board-viewport', elements.explorer); if (viewport) viewport.scrollLeft = snapshot.outer || 0;
    const list = $('.list-viewport', elements.explorer); if (list) list.scrollTop = snapshot.list || 0;
    elements.explorer.querySelectorAll('.card-list[data-status]').forEach((node) => { node.scrollTop = snapshot.lanes?.[node.dataset.status] || 0; });
  });
}
function renderEmptyExplorer(message, actions = []) {
  const wrap = el('div', 'empty-board'); const inner = el('div'); inner.append(el('div', '', message));
  if (actions.length) { const row = el('div', 'empty-actions'); actions.forEach((action) => row.append(action)); inner.append(row); }
  wrap.append(inner); elements.explorer.replaceChildren(wrap);
}
function updateSurface() {
  const detail = Boolean(state.routeTask);
  elements.appShell.classList.toggle('detail-mode', detail);
  elements.explorerToolbar.hidden = detail; elements.explorer.hidden = detail; elements.task.hidden = !detail;
  elements.taskCrumbWrap.hidden = !detail; elements.taskCrumb.textContent = detail ? state.routeTask : '';
  if (detail) { elements.tasksCrumb.removeAttribute('aria-current'); elements.taskCrumb.setAttribute('aria-current', 'page'); }
  else { elements.tasksCrumb.setAttribute('aria-current', 'page'); elements.taskCrumb.removeAttribute('aria-current'); }
  document.title = detail ? `${state.routeTask} · Docket` : `${state.workspace || 'Docket'} tasks · Docket`;
}
function renderExplorer() {
  if (!state.board || !state.preferences) return;
  const panelFocus = capturePanelFocus();
  updateSurface(); captureExplorerScroll();
  elements.boardView.setAttribute('aria-pressed', String(state.preferences.view === 'board'));
  elements.listView.setAttribute('aria-pressed', String(state.preferences.view === 'list'));
  elements.explorer.setAttribute('aria-label', state.preferences.view === 'board' ? 'Task board' : 'Task list');
  elements.search.value = state.preferences.filters.query; elements.order.value = state.preferences.order;
  const count = activeFilterCount(state.preferences); elements.filterCount.textContent = count ? String(count) : '';
  renderActiveFilters(); renderFilterPanel(); renderViewSettings();
  const tasks = filterAndSortTasks(state.board, state.preferences);
  const hidden = state.preferences.hiddenStatuses.length;
  elements.summary.textContent = `${tasks.length} / ${state.board.tasks.length} tasks${hidden ? ` · ${hidden} hidden status${hidden === 1 ? '' : 'es'}` : ''}`;
  const visibleStatuses = allStatuses(state.board).filter((status) => !state.preferences.hiddenStatuses.includes(status));
  if (!visibleStatuses.length) {
    const showAll = button('button', 'Show all'); showAll.addEventListener('click', () => { state.preferences.hiddenStatuses = []; savePreferences(); renderExplorer(); });
    renderEmptyExplorer('All statuses are hidden.', [showAll]); restorePanelFocus(panelFocus); return;
  }
  if (state.preferences.view === 'list') renderList(tasks); else renderBoard();
  restoreExplorerScroll(); restorePanelFocus(panelFocus);
}
function renderActiveFilters() {
  elements.activeFilters.replaceChildren(); const filters = state.preferences.filters;
  if (filters.query.trim()) { const chip = button('filter-chip', `Search: ${filters.query} ×`); chip.addEventListener('click', () => { filters.query = ''; savePreferences(); renderExplorer(); }); elements.activeFilters.append(chip); }
  for (const key of ['statuses', 'assignees', 'labels', 'projects', 'states']) {
    if (!filters[key].length) continue;
    const chip = button('filter-chip', `${humanize(key)} ${filters[key].length} ×`); chip.addEventListener('click', () => { filters[key] = []; savePreferences(); renderExplorer(); }); elements.activeFilters.append(chip);
  }
}
function focusPanelOption(panel, group, value) {
  requestAnimationFrame(() => [...panel.querySelectorAll('input[data-panel-group]')]
    .find((input) => input.dataset.panelGroup === group && input.dataset.panelValue === String(value))?.focus());
}
function panelSection(title, group, values, selected, display, onChange) {
  const section = el('section', 'panel-section'); section.append(el('h3', 'panel-title', title)); const list = el('div', 'option-list');
  for (const value of values) {
    const label = el('label', 'check-row'); const input = el('input'); input.type = 'checkbox'; input.checked = selected.includes(value);
    input.dataset.panelGroup = group; input.dataset.panelValue = String(value);
    input.addEventListener('change', () => onChange(value, input.checked)); label.append(input, document.createTextNode(display(value))); list.append(label);
  }
  section.append(list); return section;
}
function renderFilterPanel() {
  const options = filterOptions(state.board); const filters = state.preferences.filters; elements.filterPanel.replaceChildren();
  const mutate = (key) => (value, checked) => { filters[key] = checked ? [...new Set([...filters[key], value])] : filters[key].filter((item) => item !== value); savePreferences(); renderExplorer(); focusPanelOption(elements.filterPanel, key, value); };
  elements.filterPanel.append(
    panelSection('Status', 'statuses', options.statuses, filters.statuses, humanize, mutate('statuses')),
    panelSection('Assignee', 'assignees', options.assignees, filters.assignees, (value) => value || 'Unassigned', mutate('assignees')),
    panelSection('Labels', 'labels', options.labels, filters.labels, (value) => value, mutate('labels')),
    panelSection('Project', 'projects', options.projects, filters.projects, (value) => value || 'No project', mutate('projects')),
    panelSection('Task state', 'states', ['open', 'terminal', 'waiting'], filters.states, humanize, mutate('states')),
  );
  const actions = el('div', 'panel-actions'); const clear = button('button quiet', 'Clear all'); clear.dataset.panelAction = 'clear-filters'; clear.addEventListener('click', () => { state.preferences.filters = { query: '', statuses: [], assignees: [], labels: [], projects: [], states: [] }; savePreferences(); renderExplorer(); }); actions.append(clear); elements.filterPanel.append(actions);
}
function renderViewSettings() {
  elements.viewSettingsPanel.replaceChildren(); const viewSection = el('section', 'panel-section'); viewSection.append(el('h3', 'panel-title', 'Board options'));
  const showEmpty = el('label', 'check-row'); const emptyCheck = el('input'); emptyCheck.type = 'checkbox'; emptyCheck.checked = state.preferences.showEmpty; emptyCheck.dataset.panelGroup = 'board-options'; emptyCheck.dataset.panelValue = 'show-empty';
  emptyCheck.addEventListener('change', () => { state.preferences.showEmpty = emptyCheck.checked; savePreferences(); renderExplorer(); focusPanelOption(elements.viewSettingsPanel, 'board-options', 'show-empty'); }); showEmpty.append(emptyCheck, document.createTextNode('Show empty statuses')); viewSection.append(showEmpty);
  elements.viewSettingsPanel.append(viewSection, panelSection('Visible statuses', 'visible-statuses', allStatuses(state.board), allStatuses(state.board).filter((status) => !state.preferences.hiddenStatuses.includes(status)), humanize, (value, checked) => {
    state.preferences.hiddenStatuses = checked ? state.preferences.hiddenStatuses.filter((status) => status !== value) : [...new Set([...state.preferences.hiddenStatuses, value])]; savePreferences(); renderExplorer(); focusPanelOption(elements.viewSettingsPanel, 'visible-statuses', value);
  }));
  const actions = el('div', 'panel-actions'); const showAll = button('button quiet', 'Show all'); showAll.dataset.panelAction = 'show-all-statuses'; showAll.addEventListener('click', () => { state.preferences.hiddenStatuses = []; savePreferences(); renderExplorer(); }); actions.append(showAll); elements.viewSettingsPanel.append(actions);
}
function renderBoard() {
  const viewport = el('div', 'board-viewport'); const board = el('section', 'board'); board.setAttribute('aria-label', 'Kanban board');
  const terminal = new Set(state.board.terminal); const grouped = groupTasks(state.board, state.preferences);
  for (const [status, tasks] of grouped) {
    if (state.preferences.hiddenStatuses.includes(status) || (!state.preferences.showEmpty && !tasks.length)) continue;
    const lane = el('section', `lane ${terminal.has(status) ? 'terminal ' : ''}${statusTone(status, terminal.has(status))}`); lane.dataset.status = status;
    const header = el('header', 'lane-header'); const title = el('div', 'lane-title'); title.append(el('span', 'status-mark'), el('h2', '', humanize(status))); header.append(title, el('span', 'count', String(tasks.length)));
    const list = el('div', 'card-list'); list.dataset.status = status;
    if (!tasks.length) list.append(el('div', 'empty-column', activeFilterCount(state.preferences) ? 'No matching tasks' : 'Drop tasks here'));
    tasks.forEach((task) => list.append(renderCard(task)));
    lane.append(header, list); attachDropHandlers(lane, status); board.append(lane);
  }
  viewport.append(board); elements.explorer.replaceChildren(viewport);
}
function renderCard(task) {
  const card = el('article', 'task-card'); card.draggable = true; card.dataset.task = task.id;
  const link = el('a', 'task-card-link'); link.href = buildTaskPath(state.workspace, task.id); link.setAttribute('aria-label', `${task.id}: ${task.title}`);
  const top = el('div', 'card-top'); top.append(el('span', 'task-id', task.id)); if (task.wait) top.append(el('span', 'wait-badge', `Waiting · ${humanize(task.wait.kind)}`));
  link.append(top, el('div', 'card-title', task.title)); const meta = el('div', 'card-meta');
  (task.labels || []).forEach((label) => meta.append(el('span', `tag ${labelTone(label)}`, label)));
  if (task.resource_count) meta.append(el('span', 'reference-chip', `${task.resource_count} resource${task.resource_count === 1 ? '' : 's'}`));
  if (task.assignee) meta.append(el('span', 'card-assignee', task.assignee));
  const updated = el('time', 'card-time', relativeDate(task.updated_at)); updated.dateTime = task.updated_at; updated.title = formatDate(task.updated_at); meta.append(updated); link.append(meta); card.append(link);
  link.addEventListener('click', (event) => { if (!shouldNavigateInApp(event)) return; event.preventDefault(); navigateTask(task.id); });
  card.addEventListener('dragstart', (event) => { state.dragging = true; card.classList.add('dragging'); event.dataTransfer?.setData('text/plain', task.id); });
  card.addEventListener('dragend', () => { state.dragging = false; card.classList.remove('dragging'); document.querySelectorAll('.drag-over').forEach((node) => node.classList.remove('drag-over')); });
  return card;
}
function attachDropHandlers(lane, status) {
  lane.addEventListener('dragover', (event) => { event.preventDefault(); lane.classList.add('drag-over'); });
  lane.addEventListener('dragleave', (event) => { if (!lane.contains(event.relatedTarget)) lane.classList.remove('drag-over'); });
  lane.addEventListener('drop', async (event) => { event.preventDefault(); lane.classList.remove('drag-over'); const id = event.dataTransfer?.getData('text/plain'); if (id) await moveTask(id, status); });
}
async function moveTask(id, status) {
  const context = currentRoute(); const task = state.board.tasks.find((item) => item.id === id); if (!task || task.status === status) return;
  const previous = task.status; task.status = status; renderExplorer();
  try {
    await api(`/api/workspaces/${encodeURIComponent(context.workspace)}/tasks/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify({ status }) });
    if (!routeIsCurrent(context)) return; toast(`${id} moved to ${humanize(status)}`); await loadBoard({ quiet: true });
  } catch (error) {
    if (!routeIsCurrent(context)) return; const current = state.board.tasks.find((item) => item.id === id); if (current?.status === status) current.status = previous;
    renderExplorer(); toast(`Move failed: ${error.message}`, true);
  }
}
function renderList(tasks) {
  const viewport = el('div', 'list-viewport'); const table = el('table', 'task-table'); const head = el('thead'); const header = el('tr');
  ['Status', 'Task', 'Assignee', 'Labels', 'Updated'].forEach((label) => header.append(el('th', '', label))); head.append(header); const body = el('tbody');
  for (const task of tasks) {
    const row = el('tr', 'task-row'); const status = el('td'); const statusWrap = el('span', `list-status ${statusTone(task.status, state.board.terminal.includes(task.status))}`); statusWrap.append(el('span', 'status-mark'), document.createTextNode(humanize(task.status))); status.append(statusWrap);
    const title = el('td'); const link = el('a', 'task-row-link'); link.href = buildTaskPath(state.workspace, task.id); link.append(el('span', 'task-id list-id', task.id), el('span', 'list-title', task.title)); link.addEventListener('click', (event) => { if (!shouldNavigateInApp(event)) return; event.preventDefault(); navigateTask(task.id); }); title.append(link);
    row.append(status, title, el('td', '', task.assignee || '—'), el('td', '', (task.labels || []).join(', ') || '—'));
    const updated = el('td', '', relativeDate(task.updated_at)); updated.title = formatDate(task.updated_at); row.append(updated);
    row.addEventListener('click', (event) => { if (shouldNavigateInApp(event) && !event.target.closest('a,button,input,select,textarea')) link.click(); }); body.append(row);
  }
  table.append(head, body); viewport.append(table); elements.explorer.replaceChildren(viewport);
}

function hasDraft() {
  if (state.pendingSave || state.activeEditor?.dirty || state.activeEditor?.failed) return true;
  return Boolean($('#comment-text', elements.task)?.value || $('#wait-result', elements.task)?.value || $('#wait-comment', elements.task)?.value || elements.linkDialog.open || elements.uploadDialog.open || elements.newDialog.open);
}
function hasActiveTaskEdit() { return Boolean(state.activeEditor || state.pendingSave || hasDraft()); }
function closeTaskDialogs() { if (elements.linkDialog.open) elements.linkDialog.close(); if (elements.uploadDialog.open) elements.uploadDialog.close(); }
function closeDraftDialogs() { closeTaskDialogs(); if (elements.newDialog.open) elements.newDialog.close(); }
function confirmNavigation() { return !hasDraft() || window.confirm('Discard unsaved task input?'); }
async function flushContentEditor() {
  const active = state.activeEditor;
  if (active?.content && !active.failed) await saveContentEditor();
  if (state.pendingSavePromise) await state.pendingSavePromise;
  const failed = state.activeEditor?.content && state.activeEditor.failed;
  if (failed) state.activeEditor.editor.focus();
  return !failed;
}
async function prepareNavigation() { await flushContentEditor(); return confirmNavigation(); }
function setHistory(path, replace = false) { window.history[replace ? 'replaceState' : 'pushState'](null, '', path); }
async function navigateTask(id, { replace = false } = {}) {
  if (!(await prepareNavigation())) return; captureExplorerScroll(); closePanels(); closeDraftDialogs(); state.routeTask = id; state.selectedTask = null; state.activeEditor = null; state.pendingSave = false; advanceRoute();
  setHistory(buildTaskPath(state.workspace, id), replace); updateSurface(); await loadTask(id, { focusContent: true });
}
async function navigateExplorer({ replace = false } = {}) {
  if (!(await prepareNavigation())) return; closeDraftDialogs(); state.routeTask = ''; state.selectedTask = null; state.activeEditor = null; state.pendingSave = false; advanceRoute();
  setHistory(buildExplorerPath(state.workspace), replace); updateSurface(); renderExplorer(); elements.tasksCrumb.focus();
}
async function switchWorkspace(workspace, routeTask = '', { replace = false } = {}) {
  if (!(await prepareNavigation())) { updateWorkspaceChrome(); return; }
  closeDraftDialogs(); state.workspace = workspace; state.board = null; state.preferences = null; state.routeTask = routeTask; state.selectedTask = null; state.activeEditor = null; state.pendingSave = false; advanceRoute(); updateWorkspaceChrome();
  setHistory(routeTask ? buildTaskPath(workspace, routeTask) : buildExplorerPath(workspace), replace); await loadBoard();
  if (routeTask) await loadTask(routeTask, { focusContent: true }); else renderExplorer();
}
async function loadTask(id, { background = false, focusContent = false } = {}) {
  const request = ++state.detailRequest; const context = currentRoute();
  try {
    const detail = await api(buildTaskAPIPath(context));
    if (request !== state.detailRequest || !routeIsCurrent(context) || hasActiveTaskEdit()) return;
    state.selectedTask = detail; renderTask(detail, { focusContent });
  } catch (error) {
    if (request !== state.detailRequest || !routeIsCurrent(context) || hasActiveTaskEdit()) return; elements.task.replaceChildren(); const back = button('button', 'Back to tasks', 'back'); back.addEventListener('click', () => navigateExplorer());
    const empty = el('div', 'empty-board'); const inner = el('div'); inner.append(el('p', '', `Could not load ${id}: ${error.message}`), back); empty.append(inner); elements.task.append(empty);
  }
}
function taskFocusKey() {
  const active = document.activeElement; const named = active?.closest?.('[data-focus-key]')?.dataset.focusKey;
  return named || (active?.id ? `id:${active.id}` : '');
}
function restoreTaskFocus(key, fallback = '') {
  const targetKey = key || fallback; if (!targetKey) return;
  requestAnimationFrame(() => {
    if (targetKey.startsWith('id:')) { elements.task.querySelector(`#${CSS.escape(targetKey.slice(3))}`)?.focus(); return; }
    [...elements.task.querySelectorAll('[data-focus-key]')].find((node) => node.dataset.focusKey === targetKey)?.focus();
  });
}
function renderTask(task, { focusContent = false, restoreFocusKey = '' } = {}) {
  updateSurface(); const scrollTop = elements.task.scrollTop; elements.task.replaceChildren();
  const layout = el('div', 'task-layout'); const documentColumn = el('article', 'task-document'); documentColumn.setAttribute('aria-labelledby', 'task-heading'); const aside = el('aside', 'properties');
  const kicker = el('div', 'task-kicker'); const back = button('button quiet', 'Back to tasks', 'back'); back.dataset.focusKey = 'back'; back.addEventListener('click', () => navigateExplorer()); kicker.append(el('span', 'task-id', task.id), back);
  const title = el('h1', 'task-title-editor', task.title); title.id = 'task-heading'; configureContentEditor(title, 'title', { singleLine: true });
  const saveBanner = el('div', 'save-banner'); saveBanner.id = 'save-banner'; saveBanner.setAttribute('role', 'status'); saveBanner.setAttribute('aria-live', 'polite'); saveBanner.setAttribute('aria-atomic', 'true');
  const descriptionSection = el('section', 'document-description'); const descriptionHeading = el('div', 'section-heading'); descriptionHeading.append(el('h2', '', 'Description'));
  const descriptionBody = el('div', 'markdown content-editor'); if (task.description_html) setMarkdown(descriptionBody, task.description_html); else descriptionBody.dataset.placeholder = 'Add a description…';
  configureContentEditor(descriptionBody, 'description'); descriptionSection.append(descriptionHeading, descriptionBody);
  documentColumn.append(kicker, title, saveBanner, descriptionSection);
  if (task.wait) documentColumn.append(renderWait(task.wait));
  documentColumn.append(renderResources(task), renderRelationships(task.relationships || {}), renderActivity(task.activity || []));
  aside.append(el('h2', '', 'Properties')); const properties = el('div', 'property-list');
  properties.append(propertyRow('Status', 'status', humanize(task.status), 'select'), propertyRow('Assignee', 'assignee', task.assignee || 'Unassigned', 'input'), propertyRow('Labels', 'labels', (task.labels || []).join(', ') || 'None', 'input'));
  properties.append(staticProperty('Project', task.project?.name || task.project?.id || 'None'), staticProperty('Created', formatDate(task.created_at)), staticProperty('Updated', formatDate(task.updated_at)));
  aside.append(properties); layout.append(documentColumn, aside); elements.task.append(layout); elements.task.scrollTop = scrollTop;
  if (focusContent) { documentColumn.tabIndex = -1; requestAnimationFrame(() => documentColumn.focus()); }
  else restoreTaskFocus(restoreFocusKey);
}
function staticProperty(label, value) { const row = el('div', 'property-row'); row.append(el('span', 'property-label', label), el('span', 'property-value', value)); return row; }
function propertyRow(label, field, value, kind) {
  const row = el('div', 'property-row'); const holder = el('div'); const edit = button('property-value', value); edit.dataset.focusKey = `field-${field}`; edit.dataset.propertyField = field;
  edit.setAttribute('aria-label', `Edit ${label}: ${value}`); edit.addEventListener('click', () => openPropertyEditor(field, kind)); holder.append(edit); row.append(el('span', 'property-label', label), holder); return row;
}
async function openPropertyEditor(field, kind) {
  if (!(await flushContentEditor())) return;
  const trigger = elements.task.querySelector(`[data-property-field="${CSS.escape(field)}"]`);
  if (trigger) startEditor(trigger.parentElement, field, kind);
}
function insertPlainText(editor, value, singleLine) {
  const selection = window.getSelection(); if (!selection?.rangeCount) return;
  const range = selection.getRangeAt(0); if (!editor.contains(range.commonAncestorContainer)) return;
  const text = plainPasteText(value, singleLine); range.deleteContents(); const fragment = document.createDocumentFragment();
  text.split('\n').forEach((line, index) => { if (index) fragment.append(document.createElement('br')); fragment.append(document.createTextNode(line)); });
  const last = fragment.lastChild; range.insertNode(fragment); if (last) { range.setStartAfter(last); range.collapse(true); selection.removeAllRanges(); selection.addRange(range); }
  editor.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertText', data: text }));
}
function configureContentEditor(editor, field, { singleLine = false } = {}) {
  editor.contentEditable = 'true'; editor.spellcheck = true; editor.dataset.focusKey = `field-${field}`;
  editor.setAttribute('aria-label', `Edit ${humanize(field)}`); editor.setAttribute('aria-describedby', 'save-banner');
  editor.addEventListener('focus', () => {
    if (state.pendingSave || (state.activeEditor && state.activeEditor.editor !== editor)) { editor.blur(); return; }
    if (!state.activeEditor) state.activeEditor = { editor, field, original: String(state.selectedTask?.[field] || ''), originalHTML: editor.innerHTML, dirty: false, failed: false, content: true, singleLine };
  });
  editor.addEventListener('input', () => {
    const active = state.activeEditor; if (!active || active.editor !== editor) return;
    active.dirty = true; active.failed = false; editor.classList.remove('save-error'); editor.removeAttribute('aria-invalid'); setSaveMessage('');
  });
  editor.addEventListener('paste', (event) => { event.preventDefault(); insertPlainText(editor, event.clipboardData?.getData('text/plain') || '', singleLine); });
  editor.addEventListener('drop', (event) => { event.preventDefault(); insertPlainText(editor, event.dataTransfer?.getData('text/plain') || '', singleLine); });
  editor.addEventListener('beforeinput', (event) => { if (singleLine && (event.inputType === 'insertParagraph' || event.inputType === 'insertLineBreak')) { event.preventDefault(); editor.blur(); } });
  editor.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') { event.preventDefault(); restoreContentEditor(); }
    else if (singleLine && event.key === 'Enter') { event.preventDefault(); editor.blur(); }
  });
  editor.addEventListener('blur', () => { const blurred = state.activeEditor; setTimeout(() => { if (state.activeEditor === blurred && blurred?.editor === editor && !blurred.failed) saveContentEditor(); }, 0); });
}
function restoreContentEditor() {
  const active = state.activeEditor; if (!active?.content) return;
  const editor = active.editor;
  if (active.field === 'title') editor.textContent = active.original; else editor.innerHTML = active.originalHTML;
  editor.classList.remove('save-error'); editor.removeAttribute('aria-invalid'); state.activeEditor = null; setSaveMessage(''); editor.blur();
  requestAnimationFrame(() => editor.focus());
}
async function saveContentEditor() {
  const active = state.activeEditor; if (!active?.content || state.pendingSave) return;
  if (!active.dirty || (active.field === 'description' && active.editor.innerHTML === active.originalHTML)) { state.activeEditor = null; return; }
  const value = active.field === 'title' ? normalizedTitle(active.editor.textContent) : markdownFromElement(active.editor);
  if (active.field === 'title' && !value) { markSaveFailure(active, 'Title cannot be empty'); return; }
  if (value === active.original) { state.activeEditor = null; return; }
  await submitEditorValue(active, value);
}
function startEditor(container, field, kind) {
  if (state.pendingSave || state.activeEditor) return;
  const task = state.selectedTask; const original = field === 'labels' ? (task.labels || []).join(', ') : String(task[field] || ''); let editor;
  if (kind === 'select') { editor = el('select', 'inline-editor'); const statuses = [...(state.board?.statuses || [])]; if (original && !statuses.includes(original)) statuses.push(original); for (const status of statuses) { const option = el('option', '', humanize(status)); option.value = status; option.selected = status === original; editor.append(option); } }
  else { editor = el('input', 'inline-editor'); editor.value = original; }
  editor.setAttribute('aria-label', `Edit ${humanize(field)}`); editor.setAttribute('aria-describedby', 'save-banner'); editor.dataset.focusKey = `editor-${field}`;
  container.replaceChildren(editor); state.activeEditor = { container, editor, field, original, failed: false, dirty: false, content: false };
  editor.addEventListener('input', () => { if (!state.activeEditor) return; state.activeEditor.dirty = true; state.activeEditor.failed = false; editor.classList.remove('save-error'); editor.removeAttribute('aria-invalid'); setSaveMessage(''); });
  editor.addEventListener('keydown', (event) => { if (event.key === 'Escape') { event.preventDefault(); cancelEditor(field); } });
  editor.addEventListener('blur', () => { const blurred = state.activeEditor; setTimeout(() => { if (state.activeEditor === blurred && blurred?.editor === editor && !blurred.failed) saveEditor(); }, 0); });
  if (kind === 'select') editor.addEventListener('change', () => { if (state.activeEditor) state.activeEditor.dirty = true; editor.blur(); }); editor.focus(); if (editor.select) editor.select();
}
function cancelEditor(field = state.activeEditor?.field) { state.activeEditor = null; if (state.selectedTask) renderTask(state.selectedTask, { restoreFocusKey: `field-${field}` }); }
async function saveEditor() {
  const active = state.activeEditor; if (!active || active.content || state.pendingSave) return; let value = active.editor.value;
  if (active.field === 'assignee') value = value.trim();
  const payloadValue = active.field === 'labels' ? labelsFromInput(value) : value;
  const originalValue = active.field === 'labels' ? labelsFromInput(active.original) : active.original;
  if (JSON.stringify(payloadValue) === JSON.stringify(originalValue)) { cancelEditor(active.field); return; }
  await submitEditorValue(active, payloadValue);
}
async function submitEditorValue(active, payloadValue) {
  const context = currentRoute(); const field = active.field; const submitted = JSON.stringify({ [field]: payloadValue });
  const operation = (async () => {
    state.pendingSave = true;
    if (active.content) active.editor.contentEditable = 'false'; else active.editor.disabled = true;
    try {
      const updated = await api(buildTaskAPIPath(context), { method: 'PATCH', body: submitted });
      if (!routeIsCurrent(context)) return;
      const focusKey = taskFocusKey(); state.selectedTask = updated; state.activeEditor = null; state.pendingSave = false; renderTask(updated, { restoreFocusKey: focusKey || `field-${field}` }); await loadBoard({ quiet: true });
    } catch (error) {
      if (!routeIsCurrent(context)) return;
      state.pendingSave = false; if (active.content) active.editor.contentEditable = 'true'; else active.editor.disabled = false; markSaveFailure(active, error.message);
    }
  })();
  state.pendingSavePromise = operation;
  try { await operation; } finally { if (state.pendingSavePromise === operation) state.pendingSavePromise = null; }
}
function markSaveFailure(active, message) {
  active.failed = true; active.editor.classList.add('save-error'); active.editor.setAttribute('aria-invalid', 'true');
  setSaveMessage(`Not saved · ${message}`, true, () => { active.failed = false; active.editor.removeAttribute('aria-invalid'); if (active.content) saveContentEditor(); else saveEditor(); });
}
function setSaveMessage(message, error = false, retry) {
  const banner = $('#save-banner', elements.task); if (!banner) return; banner.className = `save-banner${error ? ' error' : ''}`; banner.replaceChildren(document.createTextNode(message));
  if (retry) { const action = button('button quiet', 'Retry'); action.addEventListener('click', retry); banner.append(document.createTextNode(' '), action); }
}
function renderWait(wait) {
  const section = el('section', 'wait-callout'); section.append(el('h2', '', `Waiting · ${humanize(wait.kind)}`), el('p', '', wait.reason));
  const url = safeURL(wait.reference); if (url) { const link = el('a', '', wait.reference); link.href = url; link.target = '_blank'; link.rel = 'noopener noreferrer'; section.append(link); }
  const form = el('form', 'wait-form'); const resultLabel = el('label', 'field'); resultLabel.append(el('span', '', 'Resolution')); const result = el('input'); result.id = 'wait-result'; result.placeholder = 'For example: approved'; resultLabel.append(result);
  const commentLabel = el('label', 'field'); commentLabel.append(el('span', '', 'Feedback (optional)')); const comment = el('textarea'); comment.id = 'wait-comment'; comment.rows = 2; comment.placeholder = 'Explain what changed'; commentLabel.append(comment);
  const submit = button('button primary', 'Resolve wait'); submit.type = 'submit'; form.append(resultLabel, commentLabel, submit);
  form.addEventListener('submit', resolveWait); section.append(form); return section;
}
async function resolveWait(event) {
  event.preventDefault(); const context = currentRoute(); const submit = $('button[type="submit"]', event.currentTarget);
  const note = $('#wait-comment', event.currentTarget).value.trim(); const result = $('#wait-result', event.currentTarget).value.trim(); const waitID = state.selectedTask?.wait?.id;
  submit.disabled = true;
  try {
    if (note) await api(buildTaskAPIPath(context, '/comments'), { method: 'POST', body: JSON.stringify({ text: note }) });
    const updated = await api(buildTaskAPIPath(context, '/wait/resolve'), { method: 'POST', body: JSON.stringify({ wait_id: waitID, result }) });
    if (!routeIsCurrent(context)) return;
    state.selectedTask = updated; renderTask(updated); toast(`${updated.id} resumed`); await loadBoard({ quiet: true });
  } catch (error) { if (routeIsCurrent(context)) toast(`Could not resolve wait: ${error.message}`, true); }
  finally { if (routeIsCurrent(context) && submit.isConnected) submit.disabled = false; }
}
function renderResources(task) {
  const section = el('section', 'document-section'); const heading = el('div', 'section-heading'); heading.append(el('h2', '', `Resources · ${(task.references || []).length + (task.attachments || []).length}`));
  const actions = el('div', 'section-actions'); const addLink = button('icon-button', '', 'link'); addLink.dataset.focusKey = 'add-link'; addLink.setAttribute('aria-label', 'Add link'); addLink.addEventListener('click', () => { elements.linkForm.reset(); elements.linkKind.value = 'plan'; elements.linkDialog.showModal(); });
  const upload = button('icon-button', '', 'file'); upload.dataset.focusKey = 'upload-file'; upload.setAttribute('aria-label', 'Upload file'); upload.addEventListener('click', () => { elements.uploadForm.reset(); elements.uploadDialog.showModal(); }); actions.append(addLink, upload); heading.append(actions); section.append(heading);
  const list = el('div', 'resource-list');
  for (const reference of [...(task.references || [])].reverse()) list.append(referenceRow(reference));
  for (const attachment of task.attachments || []) list.append(attachmentRow(attachment));
  if (!list.childElementCount) list.append(el('div', 'empty-detail', 'No resources yet. Add a plan, context link, run reference, or file.')); section.append(list); return section;
}
function referenceRow(reference) {
  const row = el('div', 'resource-row'); const mark = el('span', 'resource-icon'); mark.append(icon('link')); const content = el('div'); content.append(el('div', 'resource-title', reference.title || reference.url), el('div', 'resource-meta', `${humanize(reference.kind)} · ${reference.added_by || 'unknown'} · ${formatDate(reference.added_at)}`));
  const actions = el('div', 'resource-actions'); const url = safeURL(reference.url); if (url) { const open = el('a', 'icon-button'); open.href = url; open.target = '_blank'; open.rel = 'noopener noreferrer'; open.setAttribute('aria-label', 'Open resource'); open.append(icon('link')); actions.append(open); }
  const remove = button('icon-button', '', 'close'); remove.setAttribute('aria-label', 'Remove resource'); remove.addEventListener('click', async () => {
    if (!window.confirm('Remove this link resource?')) return; const context = currentRoute(); const referenceID = reference.id;
    try { const updated = await api(buildTaskAPIPath(context, `/references/${encodeURIComponent(referenceID)}`), { method: 'DELETE', body: '{}' }); if (!routeIsCurrent(context)) return; state.selectedTask = updated; renderTask(updated); await loadBoard({ quiet: true }); }
    catch (error) { if (routeIsCurrent(context)) toast(`Could not remove link: ${error.message}`, true); }
  }); actions.append(remove); row.append(mark, content, actions); return row;
}
function attachmentRow(attachment) {
  const row = el('div', 'resource-row'); const mark = el('span', 'resource-icon'); mark.append(icon('file')); const content = el('div'); content.append(el('div', 'resource-title', attachment.caption || attachment.file), el('div', 'resource-meta', `${attachment.file} · ${formatBytes(attachment.bytes || 0)} · ${attachment.added_by || 'unknown'}`));
  const actions = el('div', 'resource-actions'); const download = el('a', 'icon-button'); download.href = taskAPI(`/attachments/${encodeURIComponent(attachment.file)}`); download.setAttribute('aria-label', `Download ${attachment.file}`); download.append(icon('download')); actions.append(download); row.append(mark, content, actions); return row;
}
function renderRelationships(relationships) {
  const entries = Object.entries(relationships); if (!entries.length) return document.createDocumentFragment(); const section = el('section', 'document-section');
  const heading = el('div', 'section-heading'); heading.append(el('h2', '', 'Relationships')); section.append(heading); const list = el('div', 'relationship-list');
  for (const [kind, refs] of entries) for (const ref of refs) list.append(el('span', 'relationship-chip', `${humanize(kind)} · ${ref.id}${ref.title ? ` · ${ref.title}` : ''}`)); section.append(list); return section;
}
function activityTitle(entry) {
  const data = entry.data || {};
  switch (entry.type) {
    case 'task.created': return 'created the task'; case 'task.updated': return 'updated the task'; case 'task.assigned': return 'changed the assignee';
    case 'task.labeled': return 'changed labels'; case 'task.moved': return `moved ${humanize(data.from)} → ${humanize(data.to)}`;
    case 'task.waiting': return `set a wait · ${humanize(data.wait?.kind)}`; case 'task.resumed': return `resolved the wait · ${humanize(data.result || 'resolved')}`;
    case 'task.reference_added': return `added a ${humanize(data.reference?.kind)} resource`; case 'task.reference_removed': return `removed a ${humanize(data.reference?.kind)} resource`;
    case 'task.file_attached': return `attached ${data.file || 'a file'}`; case 'task.linked': return 'linked a task'; case 'task.unlinked': return 'unlinked a task';
    case 'attach': return 'attached task context'; case 'detach': return 'detached task context'; default: return humanize(String(entry.type || 'activity').replace(/^task\./, '')).toLocaleLowerCase();
  }
}
function renderActivity(activity) {
  const section = el('section', 'document-section'); const heading = el('div', 'section-heading'); heading.append(el('h2', '', `Activity · ${activity.length}`)); section.append(heading); const list = el('div', 'activity-list');
  if (!activity.length) list.append(el('div', 'empty-detail', 'No activity yet.'));
  for (const entry of activity) {
    const item = el('article', `activity-item ${entry.kind || 'event'}`); const dot = el('span', 'activity-dot'); dot.append(icon(entry.kind === 'comment' ? 'comment' : entry.type?.includes('wait') ? 'wait' : 'more'));
    if (entry.kind === 'comment') {
      const card = el('div', 'comment-card'); const header = el('div', 'comment-header'); const time = el('time', '', formatDate(entry.at)); time.dateTime = entry.at; header.append(el('strong', '', entry.actor || 'unknown'), time); const body = el('div', 'markdown'); setMarkdown(body, entry.body_html); card.append(header, body); item.append(dot, card);
    } else {
      const line = el('div', 'activity-line'); line.append(document.createTextNode(`${entry.actor || 'system'} ${activityTitle(entry)} · `)); const time = el('time', '', relativeDate(entry.at)); time.dateTime = entry.at; time.title = formatDate(entry.at); line.append(time); item.append(dot, line);
    }
    list.append(item);
  }
  section.append(list); const form = el('form', 'comment-form'); const label = el('label', 'field'); label.append(el('span', '', `Comment as ${currentActor()}`)); const textarea = el('textarea'); textarea.id = 'comment-text'; textarea.required = true; textarea.placeholder = 'Record a decision, result, or handoff note'; label.append(textarea); const submit = button('button', 'Add comment', 'comment'); submit.type = 'submit'; form.append(label, submit); form.addEventListener('submit', addComment); section.append(form); return section;
}
async function addComment(event) {
  event.preventDefault(); const context = currentRoute(); const textarea = $('#comment-text', event.currentTarget); const text = textarea.value.trim(); if (!text) return; const submit = $('button[type="submit"]', event.currentTarget); submit.disabled = true;
  try { const updated = await api(buildTaskAPIPath(context, '/comments'), { method: 'POST', body: JSON.stringify({ text }) }); if (!routeIsCurrent(context)) return; state.selectedTask = updated; renderTask(updated); toast('Comment added'); elements.task.scrollTop = elements.task.scrollHeight; }
  catch (error) { if (routeIsCurrent(context)) toast(`Comment failed: ${error.message}`, true); } finally { if (routeIsCurrent(context) && submit.isConnected) submit.disabled = false; }
}

async function submitNewTask(event) {
  event.preventDefault(); const context = currentRoute(); const submit = $('button[type="submit"]', elements.newForm); const payload = JSON.stringify({ title: elements.newTitle.value, description: elements.newDescription.value, status: elements.newStatus.value, assignee: elements.newAssignee.value.trim(), labels: labelsFromInput(elements.newLabels.value) }); submit.disabled = true;
  try {
    const created = await api(`/api/workspaces/${encodeURIComponent(context.workspace)}/tasks`, { method: 'POST', body: payload });
    if (!routeIsCurrent(context)) return; elements.newDialog.close(); await loadBoard({ quiet: true }); if (!routeIsCurrent(context)) return; await navigateTask(created.id); toast(`${created.id} created`);
  } catch (error) { if (routeIsCurrent(context)) toast(`Create failed: ${error.message}`, true); } finally { submit.disabled = false; }
}
function openNewTask() { elements.newForm.reset(); fillStatusSelect(elements.newStatus, state.board?.statuses?.[0] || ''); elements.newDialog.showModal(); setTimeout(() => elements.newTitle.focus(), 0); }
function fillStatusSelect(select, selected) { select.replaceChildren(); const statuses = [...(state.board?.statuses || [])]; if (selected && !statuses.includes(selected)) statuses.push(selected); for (const status of statuses) { const option = el('option', '', humanize(status)); option.value = status; option.selected = status === selected; select.append(option); } }
async function submitLink(event) {
  event.preventDefault(); const context = currentRoute(); const submit = $('button[type="submit"]', elements.linkForm); const payload = JSON.stringify({ kind: elements.linkKind.value.trim(), url: elements.linkURL.value.trim(), title: elements.linkTitle.value.trim() }); submit.disabled = true;
  try { const updated = await api(buildTaskAPIPath(context, '/references'), { method: 'POST', body: payload }); if (!routeIsCurrent(context)) return; state.selectedTask = updated; elements.linkDialog.close(); renderTask(updated); await loadBoard({ quiet: true }); if (routeIsCurrent(context)) toast('Link added'); }
  catch (error) { if (routeIsCurrent(context)) toast(`Could not add link: ${error.message}`, true); } finally { submit.disabled = false; }
}
async function submitUpload(event) {
  event.preventDefault(); const context = currentRoute(); const file = elements.uploadFile.files[0]; if (!file) return; const submit = $('button[type="submit"]', elements.uploadForm); const caption = elements.uploadCaption.value.trim(); submit.disabled = true; const form = new FormData(); form.append('file', file); form.append('caption', caption);
  try { const updated = await api(buildTaskAPIPath(context, '/attachments'), { method: 'POST', body: form }); if (!routeIsCurrent(context)) return; state.selectedTask = updated; elements.uploadDialog.close(); renderTask(updated); await loadBoard({ quiet: true }); if (routeIsCurrent(context)) toast(`${file.name} uploaded`); }
  catch (error) { if (routeIsCurrent(context)) toast(`Upload failed: ${error.message}`, true); } finally { submit.disabled = false; }
}

function wireControls() {
  elements.refresh.replaceChildren(icon('refresh')); elements.viewSettingsButton.replaceChildren(icon('settings'));
  document.querySelectorAll('.dialog-close.icon-button').forEach((node) => node.replaceChildren(icon('close')));
  elements.home.addEventListener('click', () => navigateExplorer()); elements.tasksCrumb.addEventListener('click', () => navigateExplorer());
  elements.workspaceButton.addEventListener('click', (event) => { event.stopPropagation(); if (state.workspacePickerOpen) closeWorkspacePicker({ restoreFocus: true }); else openWorkspacePicker(); });
  elements.workspaceButton.addEventListener('keydown', (event) => { if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) { event.preventDefault(); openWorkspacePicker(event.key); } else if (event.key === 'Escape' && state.workspacePickerOpen) { event.preventDefault(); closeWorkspacePicker({ restoreFocus: true }); } });
  elements.actor.value = readStorage('docket.actor', 'web'); elements.actor.addEventListener('change', () => writeStorage('docket.actor', currentActor()));
  elements.refresh.addEventListener('click', async () => { try { await loadWorkspaces(); updateWorkspaceChrome(); await loadBoard(); if (state.routeTask && !hasActiveTaskEdit()) await loadTask(state.routeTask, { background: true }); } catch (error) { showNotice(error.message, true); } });
  elements.newTask.addEventListener('click', openNewTask); elements.boardView.addEventListener('click', () => { state.preferences.view = 'board'; savePreferences(); renderExplorer(); }); elements.listView.addEventListener('click', () => { state.preferences.view = 'list'; savePreferences(); renderExplorer(); });
  elements.search.addEventListener('input', () => { state.preferences.filters.query = elements.search.value; savePreferences(); renderExplorer(); elements.search.focus(); });
  elements.order.addEventListener('change', () => { state.preferences.order = elements.order.value; savePreferences(); renderExplorer(); });
  elements.filterButton.addEventListener('click', (event) => { event.stopPropagation(); if (elements.filterPanel.hidden) openPanel(elements.filterPanel, elements.filterButton); else closePanels({ restoreFocus: true }); });
  elements.viewSettingsButton.addEventListener('click', (event) => { event.stopPropagation(); if (elements.viewSettingsPanel.hidden) openPanel(elements.viewSettingsPanel, elements.viewSettingsButton); else closePanels({ restoreFocus: true }); });
  document.addEventListener('click', (event) => {
    if (!event.target.closest('.control-panel') && !event.target.closest('#filter-button') && !event.target.closest('#view-settings-button')) closePanels();
    if (!event.target.closest('.workspace-picker')) closeWorkspacePicker();
  });
  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && state.panelTrigger) { event.preventDefault(); closePanels({ restoreFocus: true }); }
    else if (event.key === 'Escape' && state.workspacePickerOpen) { event.preventDefault(); closeWorkspacePicker({ restoreFocus: true }); }
  });
  elements.newForm.addEventListener('submit', submitNewTask); elements.linkForm.addEventListener('submit', submitLink); elements.uploadForm.addEventListener('submit', submitUpload);
  document.querySelectorAll('.dialog-close').forEach((node) => node.addEventListener('click', () => node.closest('dialog').close()));
  window.addEventListener('popstate', handlePopState);
}
async function handlePopState() {
  if (!(await prepareNavigation())) { setHistory(state.routeTask ? buildTaskPath(state.workspace, state.routeTask) : buildExplorerPath(state.workspace), true); return; }
  const rawRoute = parseLocation(window.location); const route = resolveRoute(rawRoute, state.workspaces.map((row) => row.name), state.workspace);
  if (!route.valid) { showNotice(route.reason === 'unknown-workspace' ? 'That workspace is not registered.' : 'This Docket route is not valid.', true); setHistory(state.routeTask ? buildTaskPath(state.workspace, state.routeTask) : buildExplorerPath(state.workspace), true); return; }
  closeDraftDialogs(); const workspaceChanged = route.workspace !== state.workspace; state.workspace = route.workspace; state.routeTask = route.task; state.selectedTask = null; state.activeEditor = null; state.pendingSave = false; advanceRoute();
  if (workspaceChanged) { state.board = null; state.preferences = null; updateWorkspaceChrome(); await loadBoard(); }
  if (rawRoute.legacy) setHistory(route.task ? buildTaskPath(route.workspace, route.task) : buildExplorerPath(route.workspace), true);
  updateSurface(); if (route.task) await loadTask(route.task, { focusContent: true }); else renderExplorer();
}
async function start() {
  wireControls(); elements.explorer.replaceChildren(el('div', 'empty-board', 'Loading workspaces…'));
  try {
    await loadWorkspaces(); const rawRoute = parseLocation(window.location); const stored = readStorage('docket.workspace'); const route = resolveRoute(rawRoute, state.workspaces.map((row) => row.name), stored);
    state.workspace = route.workspace;
    if (!state.workspace) { renderEmptyExplorer('No workspaces registered. Run docket init in a workspace directory.'); elements.newTask.disabled = true; return; }
    state.routeTask = route.task; advanceRoute(); updateWorkspaceChrome(); await loadBoard();
    setHistory(state.routeTask ? buildTaskPath(state.workspace, state.routeTask) : buildExplorerPath(state.workspace), true);
    if (!route.valid) showNotice(route.reason === 'unknown-workspace' ? 'The requested workspace is not registered; showing your default workspace.' : 'The requested route was invalid; showing your default workspace.', true);
    if (state.routeTask) await loadTask(state.routeTask, { focusContent: true }); else renderExplorer();
    setInterval(async () => { if (document.hidden || state.dragging || elements.newDialog.open) return; state.refreshes += 1; if (state.refreshes % 5 === 0) { await loadWorkspaces(); updateWorkspaceChrome(); } await loadBoard({ quiet: true }); if (state.routeTask && !hasActiveTaskEdit()) await loadTask(state.routeTask, { background: true }); }, 3000);
  } catch (error) { showNotice(`Could not start Docket: ${error.message}`, true); }
}

start();
