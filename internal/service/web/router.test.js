import { describe, expect, test } from 'bun:test';
import { buildExplorerPath, buildTaskPath, parseLocation } from './router.js';

describe('router', () => {
  test('round trips clean workspace and task routes', () => {
    expect(buildExplorerPath('client a')).toBe('/workspaces/client%20a');
    const path = buildTaskPath('client a', 'TASK-0001');
    expect(parseLocation({ pathname: path, search: '' })).toEqual({ workspace: 'client a', task: 'TASK-0001', legacy: false, valid: true });
  });
  test('accepts legacy query deep links for canonicalization', () => {
    expect(parseLocation({ pathname: '/', search: '?workspace=demo&task=TASK-0002' })).toEqual({ workspace: 'demo', task: 'TASK-0002', legacy: true, valid: true });
  });
  test('rejects unrelated and malformed routes', () => {
    expect(parseLocation({ pathname: '/other/path', search: '' }).valid).toBe(false);
    expect(parseLocation({ pathname: '/workspaces/demo/not-tasks/x', search: '' }).valid).toBe(false);
  });
});
