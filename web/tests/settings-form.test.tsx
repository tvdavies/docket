import { afterEach, describe, expect, test } from 'vitest';
import { act, cleanup, fireEvent, render, renderHook } from '@testing-library/react';
import { PluginSettingsForm } from '../src/views/settings/PluginSettingsForm';
import { useSettingsForm } from '../src/views/settings/useSettingsForm';
import { listPluginCatalogue, patchInstanceConfig, patchWorkspaceConfig, patchStatusConfig, type PluginCatalogueEntry } from '../src/api/plugin-settings';

const originalFetch = globalThis.fetch;
afterEach(() => { cleanup(); globalThis.fetch = originalFetch; });
const entry = (): PluginCatalogueEntry => ({ name: 'fixture', version: '99.0.0', source: { type: 'local' }, schemas: { instance: { text: { type: 'string' }, json: { type: 'list' }, token: { type: 'string', secret: true } } }, instance_values: { text: 'old' }, workspace_values: {} });
const target = { scope: 'instance' as const, workspace: '', plugin: 'fixture', status: '' };
const draft = (text: string) => ({ set: true, text, checked: false, option: -1 });
const ok = () => Response.json({ plugin: 'fixture', values: { text: 'new' } });

describe('settings API wrappers', () => {
  test('uses existing same-origin JSON client and encoded path segments with typed bodies', async () => {
    const requests: { path: string; options: RequestInit }[] = [];
    globalThis.fetch = (async (path, options) => { requests.push({ path: String(path), options: options! }); return ok(); }) as typeof fetch;
    await patchInstanceConfig('a/b', { value: false });
    await patchWorkspaceConfig('a b', 'a/b', { value: 0 });
    await patchStatusConfig('a b', 'a/b', 'in/review', { value: [1, false] });
    expect(requests.map((item) => item.path)).toEqual(['/api/plugins/a%2Fb/config', '/api/workspaces/a%20b/plugins/a%2Fb/config', '/api/workspaces/a%20b/plugins/a%2Fb/statuses/in%2Freview']);
    expect(requests.map((item) => JSON.parse(item.options.body as string).values.value)).toEqual([false, 0, [1, false]]);
    for (const { options } of requests) { expect(options.credentials).toBe('same-origin'); expect(new Headers(options.headers).get('Content-Type')).toBe('application/json'); expect(new Headers(options.headers).has('X-Docket-Actor')).toBe(true); }
  });
  test('catalogue faults are errors, secret values are dropped and unknown versions work', async () => {
    for (const response of [Response.json({ error: 'broken manifest' }, { status: 500 }), new Response('html'), Response.json([{}]), Response.json([{ ...entry(), workspace_values: [] }])]) {
      globalThis.fetch = (async () => response) as typeof fetch;
      await expect(listPluginCatalogue()).rejects.toThrow();
    }
    globalThis.fetch = (async () => Response.json([{ ...entry(), instance_values: { text: 'old', token: 'NEVER' } }])) as typeof fetch;
    expect((await listPluginCatalogue())[0].instance_values).toEqual({ text: 'old' });
  });
  test('malformed success does not imply a confirmed write', async () => {
    globalThis.fetch = (async () => new Response('html')) as typeof fetch;
    await expect(patchInstanceConfig('fixture', { text: 'new' })).rejects.toBeInstanceOf(TypeError);
  });
});

describe('failure-safe settings controller', () => {
  test('unsupported schema refresh retains old drafts without crashing or saving', async () => {
    const current = entry();
    const props = { entry: current, target, reload: async () => [current], checkTarget: () => '' };
    const view = render(<PluginSettingsForm {...props} />);
    fireEvent.change(view.container.querySelector('[data-key="text"] input')!, { target: { value: 'kept' } });
    view.rerender(<PluginSettingsForm {...props} entry={{ ...current, schemas: { instance: { text: null } } as unknown as PluginCatalogueEntry['schemas'] }} />);
    expect(view.getByText(/Unsupported schema/)).toBeTruthy();
    expect((view.container.querySelector('[data-key="text"] input') as HTMLInputElement).value).toBe('kept');
    expect((view.getByRole('button', { name: 'Save' }) as HTMLButtonElement).disabled).toBe(true);
  });
  test('server field rejection focuses the labelled control after re-enabling it', async () => {
    const current = entry();
    globalThis.fetch = (async () => Response.json({ error: 'config.instance.text: must be string' }, { status: 400 })) as typeof fetch;
    const view = render(<PluginSettingsForm entry={current} target={target} reload={async () => [current]} checkTarget={() => ''} />);
    const input = view.container.querySelector('[data-key="text"] input')!;
    fireEvent.change(input, { target: { value: 'new' } });
    await act(async () => { fireEvent.click(view.getByRole('button', { name: 'Save' })); });
    await act(async () => { await new Promise<void>((done) => requestAnimationFrame(() => done())); });
    expect(document.activeElement === input).toBe(true);
    expect(input.getAttribute('aria-invalid')).toBe('true');
  });
  test('keeps raw invalid JSON and sends nothing', async () => {
    let calls = 0;
    globalThis.fetch = (async () => { calls++; return ok(); }) as typeof fetch;
    const current = entry();
    const { result } = renderHook(() => useSettingsForm({ entry: current, target, reload: async () => [current], checkTarget: () => '' }));
    act(() => result.current.setDraft('json', draft('[broken')));
    await act(() => result.current.save());
    expect(result.current.drafts.json.text).toBe('[broken'); expect(result.current.errors.json).toContain('Invalid JSON'); expect(calls).toBe(0);
  });
  test('rejects duplicate saves synchronously and retains drafts on server rejection', async () => {
    let release!: (entries: PluginCatalogueEntry[]) => void; let calls = 0;
    const current = entry();
    globalThis.fetch = (async () => { calls++; return Response.json({ error: 'config.instance.text: must be string' }, { status: 400 }); }) as typeof fetch;
    const { result } = renderHook(() => useSettingsForm({ entry: current, target, reload: () => new Promise((resolve) => { release = resolve; }), checkTarget: () => '' }));
    act(() => result.current.setDraft('text', draft('new')));
    let first!: Promise<void>;
    act(() => { first = result.current.save(); void result.current.save(); });
    await act(async () => { release([current]); await first; });
    expect(calls).toBe(1); expect(result.current.status).toBe('failed'); expect(result.current.drafts.text.text).toBe('new'); expect(result.current.errors.text).toContain('must be string');
  });
  test('changed version blocks writes and keeps drafts for deliberate review', async () => {
    let calls = 0; const current = entry(); const fresh = { ...current, version: '100.0.0' };
    globalThis.fetch = (async () => { calls++; return ok(); }) as typeof fetch;
    const { result, rerender } = renderHook(({ value }) => useSettingsForm({ entry: value, target, reload: async () => [fresh], checkTarget: () => '' }), { initialProps: { value: current } });
    act(() => result.current.setDraft('text', draft('new')));
    await act(() => result.current.save());
    rerender({ value: fresh });
    expect(calls).toBe(0); expect(result.current.staleNotice).toBe(true); expect(result.current.drafts.text.text).toBe('new');
    act(() => result.current.reviewRefreshed()); expect(result.current.canSave).toBe(true);
  });
  test('a refreshed instance fallback updates board provenance without writing a local value', () => {
    const current = { ...entry(), schemas: { instance: { flag: { type: 'boolean' as const } }, workspace: { flag: { type: 'boolean' as const } } }, instance_values: { flag: false }, workspace_values: { alpha: { config: {}, statuses: {} } } };
    const boardTarget = { ...target, scope: 'workspace' as const, workspace: 'alpha' };
    const { result, rerender } = renderHook(({ value }) => useSettingsForm({ entry: value, target: boardTarget, reload: async () => [value], checkTarget: () => '' }), { initialProps: { value: current } });
    expect(result.current.fields[0].effective).toContain('false');
    rerender({ value: { ...current, instance_values: { flag: true } } });
    expect(result.current.fields[0].effective).toContain('true');
    expect(result.current.drafts.flag.set).toBe(false); expect(result.current.dirty).toBe(false);
  });
  test('a semantically unchanged formatted JSON save becomes clean', async () => {
    const current = { ...entry(), instance_values: { text: 'old', json: [1] } };
    globalThis.fetch = (async () => Response.json({ plugin: 'fixture', values: { json: [1] } })) as typeof fetch;
    const { result } = renderHook(() => useSettingsForm({ entry: current, target, reload: async () => [current], checkTarget: () => '' }));
    act(() => result.current.setDraft('json', draft('[1]')));
    expect(result.current.dirty).toBe(true);
    await act(() => result.current.save());
    expect(result.current.status).toBe('saved'); expect(result.current.dirty).toBe(false);
  });
  test('success with failed read-back cannot issue another PATCH through discard', async () => {
    let calls = 0; let reads = 0; const current = entry();
    globalThis.fetch = (async () => { calls++; return ok(); }) as typeof fetch;
    const { result } = renderHook(() => useSettingsForm({ entry: current, target, reload: async () => { if (++reads > 1) throw new Error('offline'); return [current]; }, checkTarget: () => '' }));
    act(() => result.current.setDraft('text', draft('new')));
    await act(() => result.current.save());
    expect(result.current.status).toBe('saved-reload-failed'); expect(result.current.drafts.text.text).toBe('new');
    act(() => result.current.discardAll()); await act(() => result.current.save());
    expect(calls).toBe(1); expect(result.current.canSave).toBe(false);
  });
  test('uncertain write requires read-back; read retry never writes', async () => {
    let calls = 0; const current = entry();
    globalThis.fetch = (async () => { calls++; throw new TypeError('connection lost'); }) as typeof fetch;
    const { result } = renderHook(() => useSettingsForm({ entry: current, target, reload: async () => [current], checkTarget: () => '' }));
    act(() => result.current.setDraft('text', draft('new'))); await act(() => result.current.save());
    expect(result.current.status).toBe('uncertain'); await act(() => result.current.save()); expect(calls).toBe(1);
    await act(() => result.current.retryReload()); expect(calls).toBe(1); expect(result.current.drafts.text.text).toBe('new'); expect(result.current.canSave).toBe(true);
  });
  test('availability rejection is caught and an unmounted preflight cannot write', async () => {
    let release!: (entries: PluginCatalogueEntry[]) => void; let calls = 0; const current = entry();
    globalThis.fetch = (async () => { calls++; return ok(); }) as typeof fetch;
    const first = renderHook(() => useSettingsForm({ entry: current, target, reload: async () => [current], checkTarget: async () => { throw new Error('workspace gone'); } }));
    act(() => first.result.current.setDraft('text', draft('new'))); await act(() => first.result.current.save());
    expect(first.result.current.status).toBe('failed'); expect(first.result.current.message).toContain('workspace gone'); first.unmount();
    const second = renderHook(() => useSettingsForm({ entry: current, target, reload: () => new Promise((resolve) => { release = resolve; }), checkTarget: () => '' }));
    act(() => second.result.current.setDraft('text', draft('new')));
    let pending!: Promise<void>; act(() => { pending = second.result.current.save(); }); second.unmount();
    await act(async () => { release([current]); await pending; }); expect(calls).toBe(0);
  });
});
