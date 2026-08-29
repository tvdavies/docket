import { render, screen } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { PluginCardHost } from '../src/registry/PluginCardHost';
import { demoPluginUI } from '../src/registry/demo-plugin';
import { registerPluginUI, resolveReference } from '../src/registry/registry';
import type { BoardTask } from '../src/types';

const task = (labels: string[] = [], references: any[] = []): BoardTask => ({ id: 'TASK-1', title: 'Demo', status: 'todo', labels, references, active_sessions: [], created_at: '2026-01-01', updated_at: '2026-01-01', resource_count: references.length });

describe('approved plugin UI contracts', () => {
  test('demo resolver renders plan metadata through the central registry', async () => {
    registerPluginUI(demoPluginUI);
    const value = await resolveReference({ id: 'ref-1', kind: 'plan', url: 'https://plans.myslop.app/p/abc', added_at: '', added_by: '' });
    expect(value.label).toBe('Implementation plan');
    expect(value.meta).toMatchObject({ provider: 'myslop plans', id: 'abc' });
  });

  test('card host mounts, updates, destroys, and isolates broken modules', () => {
    const update = vi.fn(); const destroy = vi.fn();
    registerPluginUI({ cards: [
      { type: 'test/lifecycle', appliesTo: (value) => value.labels.includes('lifecycle'), mount(el) { el.textContent = 'plugin mounted'; return { update, destroy }; } },
      { type: 'test/broken', appliesTo: (value) => value.labels.includes('broken'), mount() { throw new Error('broken'); } },
    ] });
    const { rerender, unmount } = render(<PluginCardHost workspace="demo" task={task(['lifecycle', 'broken'])} />);
    expect(screen.getByText('plugin mounted')).toBeInTheDocument();
    expect(screen.getByText('Plugin card unavailable')).toBeInTheDocument();
    rerender(<PluginCardHost workspace="demo" task={{ ...task(['lifecycle', 'broken']), status: 'done', updated_at: '2026-01-02' }} />);
    expect(update).toHaveBeenCalled();
    unmount(); expect(destroy).toHaveBeenCalled();
  });
});
