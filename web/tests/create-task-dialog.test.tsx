import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { CreateTaskDialog } from '../src/views/task/CreateTaskDialog';

describe('CreateTaskDialog', () => {
  test('preserves an open draft across live config updates', () => {
    const props = { open: true, onOpenChange: vi.fn(), onCreate: vi.fn(async () => undefined) };
    const view = render(<CreateTaskDialog {...props} config={{ statuses: ['todo', 'done'], terminal: ['done'], labels: [] }} />);
    fireEvent.change(screen.getByLabelText('Title'), { target: { value: 'Draft title' } });
    fireEvent.change(screen.getByLabelText('Description'), { target: { value: 'Draft description' } });
    view.rerender(<CreateTaskDialog {...props} config={{ statuses: ['backlog', 'todo', 'done'], terminal: ['done'], labels: ['urgent'] }} />);
    expect(screen.getByLabelText('Title')).toHaveValue('Draft title');
    expect(screen.getByLabelText('Description')).toHaveValue('Draft description');
    expect(screen.getByLabelText('Status')).toHaveValue('todo');
  });
});
