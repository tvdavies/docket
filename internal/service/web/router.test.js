import { describe, expect, test } from 'bun:test';
import {
  buildExplorerPath, buildTaskAPIPath, buildTaskPath, parseLocation,
  resolveRoute, routeContext, sameRouteContext, shouldNavigateInApp,
} from './router.js';

describe('router', () => {
  test('round trips clean workspace and task routes', () => {
    expect(buildExplorerPath('client a')).toBe('/workspaces/client%20a');
    const path = buildTaskPath('client a', 'TASK-0001');
    expect(parseLocation({ pathname: path, search: '' })).toEqual({ workspace: 'client a', task: 'TASK-0001', legacy: false, valid: true });
  });

  test('accepts legacy query deep links for known workspace canonicalization', () => {
    const parsed = parseLocation({ pathname: '/', search: '?workspace=demo&task=TASK-0002' });
    expect(parsed).toEqual({ workspace: 'demo', task: 'TASK-0002', legacy: true, valid: true });
    expect(resolveRoute(parsed, ['demo'], 'demo')).toEqual({ valid: true, workspace: 'demo', task: 'TASK-0002', legacy: true, reason: '' });
  });

  test('rejects unrelated, malformed, and unknown-workspace routes without retaining task ids', () => {
    expect(parseLocation({ pathname: '/other/path', search: '' }).valid).toBe(false);
    expect(parseLocation({ pathname: '/workspaces/demo/not-tasks/x', search: '' }).valid).toBe(false);
    const unknown = resolveRoute(parseLocation({ pathname: '/workspaces/missing/tasks/TASK-9999', search: '' }), ['demo'], 'demo');
    expect(unknown).toEqual({ valid: false, workspace: 'demo', task: '', legacy: false, reason: 'unknown-workspace' });
    const unknownLegacy = resolveRoute(parseLocation({ pathname: '/', search: '?workspace=missing&task=TASK-9999' }), ['demo'], 'demo');
    expect(unknownLegacy.task).toBe('');
    expect(unknownLegacy.workspace).toBe('demo');
  });

  test('captures immutable route contexts and builds bound task API paths', () => {
    const context = routeContext('client a', 'TASK-0001', 7);
    expect(buildTaskAPIPath(context, '/comments')).toBe('/api/workspaces/client%20a/tasks/TASK-0001/comments');
    expect(sameRouteContext(context, routeContext('client a', 'TASK-0001', 7))).toBe(true);
    expect(sameRouteContext(context, routeContext('client a', 'TASK-0002', 8))).toBe(false);
  });

  test('uses SPA navigation only for unmodified primary link clicks', () => {
    const primary = { button: 0, defaultPrevented: false, metaKey: false, ctrlKey: false, shiftKey: false, altKey: false };
    expect(shouldNavigateInApp(primary)).toBe(true);
    for (const override of [
      { button: 1 }, { defaultPrevented: true }, { metaKey: true },
      { ctrlKey: true }, { shiftKey: true }, { altKey: true },
    ]) {
      expect(shouldNavigateInApp({ ...primary, ...override })).toBe(false);
    }
  });
});
