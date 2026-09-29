import { describe, expect, test } from 'vitest';
import { buildFields, draftFromValue, evaluateDraft, evaluateForm, hasDynamicOptions, MAX_FIELD_OPTIONS, parseFieldOptions, seedDraft } from '../src/views/settings/model';
import { invalidSchemaReason, type PluginConfigField } from '../src/api/plugin-settings';
import { instanceSettingsPath, parseRoute, resolveRoute, statusSettingsPath, workspaceSettingsPath } from '../src/app/router';

const unset = { set: false, text: '', checked: false, option: -1 };
describe('generated settings data contract', () => {
  test('keeps absent, false, zero, empty string and whitespace distinct', () => {
    for (const [type, value] of [['boolean', false], ['number', 0], ['string', ''], ['string', '  x  '], ['list', []], ['map', {}]] as const) {
      const field = { type } as PluginConfigField;
      expect(evaluateDraft(field, draftFromValue(field, { present: true, value }))).toEqual({ ok: true, absent: false, value });
      expect(evaluateDraft(field, unset)).toEqual({ ok: true, absent: true, value: undefined });
    }
    for (const text of ['', '-', '1e', 'Infinity', 'NaN', '1e400', '0x10']) expect(evaluateDraft({ type: 'number' }, { ...unset, set: true, text }).ok).toBe(false);
  });
  test('validates JSON kind and preserves nested JSON types', () => {
    for (const [type, text] of [['list', '{}'], ['map', '[]'], ['list', 'null'], ['map', '{'], ['map', '{} trailing'], ['list', '[1e400]']] as const) expect(evaluateDraft({ type }, { ...unset, set: true, text }).ok).toBe(false);
    expect(evaluateDraft({ type: 'list' }, { ...unset, set: true, text: '[1,false,null,{"x":[2]}]' })).toEqual({ ok: true, absent: false, value: [1, false, null, { x: [2] }] });
  });
  test('enums preserve boolean, number and compound values', () => {
    for (const field of [{ type: 'boolean', enum: [false, true] }, { type: 'number', enum: [1, 3] }, { type: 'list', enum: [['a'], ['a', 'b']] }, { type: 'map', enum: [{ x: 1 }, { x: false }] }] as PluginConfigField[]) {
      expect(evaluateDraft(field, { ...unset, set: true, option: 1 })).toEqual({ ok: true, absent: false, value: field.enum![1] });
      expect(evaluateDraft(field, seedDraft(field)).ok).toBe(false);
    }
  });
  test('patches touched keys only and never secrets, required means presence', () => {
    const fields = buildFields('workspace', { text: { type: 'string', required: true }, count: { type: 'number', default: 4 }, token: { type: 'string', secret: true } }, { values: {} });
    const baselines = Object.fromEntries(fields.map((item) => [item.key, draftFromValue(item.field, item.baseline)]));
    expect(evaluateForm(fields, baselines, baselines).errors.text).toContain('Required');
    const drafts = { ...baselines, text: { ...unset, set: true, text: '' }, token: { ...unset, set: true, text: 'NEVER' } };
    const evaluated = evaluateForm(fields, drafts, baselines);
    expect(evaluated.values).toEqual({ text: '' });
    expect(evaluated.errors).toEqual({});
  });
  test('scope precedence is independent and instance provenance is honest', () => {
    const schema = { limit: { type: 'number', default: 4 }, fallback: { type: 'boolean' }, secret: { type: 'string' } } as const;
    const fields = buildFields('workspace', schema, { values: {}, instanceValues: { limit: 2, fallback: false, secret: 'NEVER' }, instanceSchema: { limit: { type: 'number' }, fallback: { type: 'boolean' }, secret: { type: 'string', secret: true } } });
    expect(fields.find((item) => item.key === 'limit')!.provenance).toContain('default 4');
    expect(fields.find((item) => item.key === 'fallback')!.effective).toContain('instance value false');
    expect(JSON.stringify(fields.map((item) => item.effective))).not.toContain('NEVER');
    expect(buildFields('instance', schema, { values: { limit: 4 } }).find((item) => item.key === 'limit')!.provenance).toContain('stored or default');
    expect(buildFields('status', schema, { values: {} }).every((item) => !item.effective)).toBe(true);
  });
  test('rejects malformed declarations without leaking secret values in diagnostics', () => {
    for (const field of [null, { type: 'future' }, { type: 'string', required: 'yes' }, { type: 'number', default: false }, { type: 'boolean', enum: ['false'] }, { type: 'string', secret: true, default: 'NEVER' }]) {
      const error = invalidSchemaReason({ field }); expect(error).not.toBe(''); expect(error).not.toContain('NEVER');
    }
  });
  test('settings deep links do not depend on a registered workspace', () => {
    for (const path of [instanceSettingsPath(), workspaceSettingsPath('a b'), statusSettingsPath('a b', 'in/review')]) {
      const route = parseRoute({ pathname: path, search: '' });
      expect(route.valid).toBe(true); expect(resolveRoute(route, []).settings).toEqual(route.settings);
    }
    for (const path of ['/settings/other', '/settings/plugins/extra', '/workspaces/a/settings/plugins/statuses', '/workspaces/%FF/settings/plugins']) expect(parseRoute({ pathname: path, search: '' }).valid).toBe(false);
  });
});

describe('options_from fields', () => {
  test('schema validation accepts service paths on string and number fields only', () => {
    expect(invalidSchemaReason({ pick: { type: 'string', options_from: '/options/picks' } }, 'instance')).toBe('');
    expect(invalidSchemaReason({ pick: { type: 'number', options_from: '/options?x' } }, 'instance')).not.toBe('');
    for (const field of [
      { type: 'string', options_from: 'relative' },
      { type: 'string', options_from: '//evil.example/x' },
      { type: 'string', options_from: 42 },
      { type: 'list', options_from: '/x' },
      { type: 'string', options_from: '/x', enum: ['a'] },
      { type: 'string', options_from: '/x', secret: true },
    ]) expect(invalidSchemaReason({ pick: field } as unknown as Record<string, PluginConfigField>, 'instance')).not.toBe('');
    expect(hasDynamicOptions({ type: 'string', options_from: '/x' })).toBe(true);
    expect(hasDynamicOptions({ type: 'string' })).toBe(false);
  });
  test('responses accept bare values and labelled objects, dropping duplicates', () => {
    expect(parseFieldOptions({ type: 'string' }, ['a', { value: 'b', label: 'Bee' }, { value: 'a', label: 'again' }, { value: 'c', label: ' ' }]))
      .toEqual([{ value: 'a', label: 'a' }, { value: 'b', label: 'Bee' }, { value: 'c', label: 'c' }]);
    expect(parseFieldOptions({ type: 'number' }, [1, { value: 2.5 }])).toEqual([{ value: 1, label: '1' }, { value: 2.5, label: '2.5' }]);
  });
  test('malformed responses are errors', () => {
    for (const [field, payload] of [
      [{ type: 'string' }, { options: [] }],
      [{ type: 'string' }, [1]],
      [{ type: 'number' }, ['1']],
      [{ type: 'number' }, [{ value: Number.NaN }]],
      [{ type: 'string' }, [null]],
      [{ type: 'string' }, Array.from({ length: MAX_FIELD_OPTIONS + 1 }, (_, index) => String(index))],
    ] as [PluginConfigField, unknown][]) expect(() => parseFieldOptions(field, payload)).toThrow();
  });
});
