import { useState } from 'react';
import { act, fireEvent, render, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { FilterPopover } from '../src/views/filters/FilterPopover';
import { defaultPreferences, type Preferences } from '../src/store/preferences';
import type { BoardTask } from '../src/types';

const tasks = [
  { id: 'TASK-1', status: 'todo', assignee: 'Tom', labels: ['bug'], project: 'Docket' },
  { id: 'TASK-2', status: 'done', labels: ['design'] },
] as BoardTask[];
const config = { statuses: ['todo', 'done'], terminal: ['done'], labels: ['configured-label'] };
function Harness({ initial = defaultPreferences(), items = tasks }: { initial?: Preferences; items?: BoardTask[] }) {
  const [preferences, setPreferences] = useState(initial);
  return <><FilterPopover tasks={items} preferences={preferences} config={config} update={setPreferences} /><output data-testid="preferences">{JSON.stringify(preferences)}</output><button>Outside</button></>;
}

describe('FilterPopover', () => {
  test('shows every category in one popover and stays open through multiple selections', async () => {
    const view = render(<Harness />);
    fireEvent.click(view.getByRole('button', { name: 'Filter' }));
    const panel = view.getByRole('dialog', { name: 'Filters' });
    for (const name of ['Status', 'Assignee', 'Labels', 'Project', 'State']) expect(within(panel).getByRole('group', { name })).toBeInTheDocument();
    for (const name of ['Todo', 'Done', 'Tom', 'bug', 'Docket', 'Waiting']) {
      fireEvent.click(within(panel).getByRole('checkbox', { name }));
      expect(view.getByRole('dialog', { name: 'Filters' })).toBeInTheDocument();
      expect(within(panel).getByRole('checkbox', { name })).toBeChecked();
    }
    expect(within(panel).getByText('6 active filters')).toBeInTheDocument();
    fireEvent.click(within(panel).getByRole('checkbox', { name: 'Todo' }));
    expect(within(panel).getByRole('checkbox', { name: 'Todo' })).not.toBeChecked();
    expect(within(panel).getByRole('checkbox', { name: 'Done' })).toBeChecked();
    await act(async () => {});
  });

  test('reset clears text and every filter without changing view settings or closing', async () => {
    const initial = defaultPreferences();
    initial.view = 'list'; initial.order = 'title-asc'; initial.hiddenStatuses = ['done'];
    initial.filters = { query: 'search', statuses: ['todo'], assignees: ['Tom'], labels: ['bug'], projects: ['Docket'], states: ['open'] };
    const view = render(<Harness initial={initial} />);
    fireEvent.click(view.getByRole('button', { name: /Filter/ }));
    fireEvent.click(view.getByRole('button', { name: 'Reset filters' }));
    expect(view.getByRole('dialog', { name: 'Filters' })).toBeInTheDocument();
    expect(view.getByLabelText('Search tasks')).toHaveValue('');
    expect(view.getByRole('button', { name: 'Reset filters' })).toBeDisabled();
    const value = JSON.parse(view.getByTestId('preferences').textContent!);
    expect(value.filters).toEqual(defaultPreferences().filters);
    expect(value.view).toBe('list'); expect(value.order).toBe('title-asc'); expect(value.hiddenStatuses).toEqual(['done']);
    await act(async () => {});
  });

  test('includes configured labels, unassigned/no-project options, and removable stale selections', async () => {
    const initial = defaultPreferences(); initial.filters.assignees = ['Former assignee'];
    const view = render(<Harness initial={initial} />);
    fireEvent.click(view.getByRole('button', { name: /Filter/ }));
    expect(view.getByRole('checkbox', { name: 'configured-label' })).toBeInTheDocument();
    fireEvent.click(view.getByRole('checkbox', { name: 'Unassigned' }));
    fireEvent.click(view.getByRole('checkbox', { name: 'No project' }));
    fireEvent.click(view.getByRole('checkbox', { name: 'Former assignee' }));
    const value = JSON.parse(view.getByTestId('preferences').textContent!);
    expect(value.filters.assignees).toEqual(['']); expect(value.filters.projects).toEqual(['']);
    expect(view.queryByRole('checkbox', { name: 'Former assignee' })).toBeNull();
    await act(async () => {});
  });
});
