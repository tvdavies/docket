import { describe, expect, test } from 'bun:test';
import { nextWorkspaceIndex, workspaceOptionText } from './workspace-picker.js';

describe('workspace picker helpers', () => {
  test('wraps arrow navigation and supports boundaries', () => {
    expect(nextWorkspaceIndex(3, 2, 'ArrowDown')).toBe(0);
    expect(nextWorkspaceIndex(3, 0, 'ArrowUp')).toBe(2);
    expect(nextWorkspaceIndex(3, 1, 'Home')).toBe(0);
    expect(nextWorkspaceIndex(3, 1, 'End')).toBe(2);
    expect(nextWorkspaceIndex(0, 0, 'ArrowDown')).toBe(-1);
  });

  test('presents workspace state and path context', () => {
    expect(workspaceOptionText({ name: 'dispatch', state: 'watching', path: '/tmp/dispatch' })).toEqual({
      name: 'dispatch', detail: 'Watching · /tmp/dispatch',
    });
    expect(workspaceOptionText({ name: 'broken', state: 'error', path: '/tmp/broken' }).detail).toBe('error · /tmp/broken');
  });
});
