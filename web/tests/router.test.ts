import { describe, expect, test } from 'vitest';
import { classicPath, explorerPath, parseRoute, resolveRoute, taskRoutePath } from '../src/app/router';

describe('React router helpers', () => {
  test('round trips canonical and preview paths', () => {
    expect(explorerPath('client a')).toBe('/workspaces/client%20a');
    expect(taskRoutePath('client a', 'TASK-0001')).toBe('/workspaces/client%20a/tasks/TASK-0001');
    expect(parseRoute({ pathname: '/next/workspaces/client%20a/tasks/TASK-0001', search: '' } as Location)).toMatchObject({ workspace: 'client a', task: 'TASK-0001', valid: true });
    expect(classicPath('client a', 'TASK-0001')).toBe('/classic/workspaces/client%20a/tasks/TASK-0001');
  });
  test('accepts legacy queries and drops task IDs for unknown workspaces', () => {
    const route = parseRoute({ pathname: '/', search: '?workspace=missing&task=TASK-9' } as Location);
    expect(resolveRoute(route, ['demo'], 'demo')).toEqual({ workspace: 'demo', task: '', valid: false });
  });
});
