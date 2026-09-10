import { invalidSchemaReason, type PluginCatalogueEntry, type PluginConfigField, type PluginConfigSchemas } from '../../api/plugin-settings';
import type { SettingsScope } from '../../app/router';

export type { SettingsScope };

/** Identity of one generated form. Drafts never leak across identities. */
export type FormTarget = { scope: SettingsScope; plugin: string; workspace: string; status: string };
export const targetKey = (target: FormTarget) => [target.scope, target.plugin, target.workspace, target.status].map(encodeURIComponent).join('/');

/** Raw editor state for one field. Text is kept verbatim so invalid input survives a failed save. */
export type FieldDraft = { set: boolean; text: string; checked: boolean; option: number };
export type Baseline = { present: boolean; value: unknown };
export type FieldResult = { ok: true; absent: boolean; value: unknown } | { ok: false; error: string };

const hasOwn = (value: object, key: string) => Object.prototype.hasOwnProperty.call(value, key);

/** Stable JSON used to compare typed values (enum matching, baseline changes). */
export function canonical(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonical).join(',')}]`;
  if (value && typeof value === 'object') return `{${Object.keys(value as object).sort().map((key) => `${JSON.stringify(key)}:${canonical((value as Record<string, unknown>)[key])}`).join(',')}}`;
  return JSON.stringify(value) ?? 'undefined';
}

export const humanizeKey = (key: string) => key.replace(/[._-]+/g, ' ').replace(/\b\w/g, (letter) => letter.toUpperCase());
export const optionLabel = (value: unknown) => typeof value === 'string' ? value : JSON.stringify(value);
export const formatValue = (value: unknown) => typeof value === 'string' ? (value === '' ? '"" (empty text)' : value) : JSON.stringify(value);

export const isJsonKind = (field: PluginConfigField) => field.type === 'list' || field.type === 'map';
export const hasEnum = (field: PluginConfigField) => Array.isArray(field.enum) && field.enum.length > 0;
export const hasDefault = (field: PluginConfigField) => field.default !== undefined && field.default !== null;

export function enumIndex(field: PluginConfigField, value: unknown): number {
  if (!hasEnum(field)) return -1;
  const wanted = canonical(value);
  return field.enum!.findIndex((option) => canonical(option) === wanted);
}

const emptyDraft: FieldDraft = { set: false, text: '', checked: false, option: -1 };

export function draftFromValue(field: PluginConfigField, baseline: Baseline): FieldDraft {
  if (!baseline.present) return { ...emptyDraft };
  const value = baseline.value;
  if (hasEnum(field)) return { ...emptyDraft, set: true, option: enumIndex(field, value) };
  switch (field.type) {
    case 'boolean': return { ...emptyDraft, set: true, checked: value === true };
    case 'number': return { ...emptyDraft, set: true, text: typeof value === 'number' ? String(value) : String(value ?? '') };
    case 'list': case 'map': return { ...emptyDraft, set: true, text: JSON.stringify(value, null, 2) ?? '' };
    default: return { ...emptyDraft, set: true, text: typeof value === 'string' ? value : String(value ?? '') };
  }
}

/** Seeds a value for a field the user chose to set. Declared defaults are the obvious starting point. */
export function seedDraft(field: PluginConfigField): FieldDraft {
  if (hasDefault(field)) return draftFromValue(field, { present: true, value: field.default });
  if (hasEnum(field)) return { ...emptyDraft, set: true };
  switch (field.type) {
    case 'boolean': return { ...emptyDraft, set: true, checked: false };
    case 'list': return { ...emptyDraft, set: true, text: '[]' };
    case 'map': return { ...emptyDraft, set: true, text: '{}' };
    default: return { ...emptyDraft, set: true };
  }
}

export const draftEquals = (left: FieldDraft, right: FieldDraft) => left.set === right.set && left.text === right.text && left.checked === right.checked && left.option === right.option;

export function finiteJson(value: unknown): boolean {
  if (typeof value === 'number') return Number.isFinite(value);
  if (Array.isArray(value)) return value.every(finiteJson);
  if (value && typeof value === 'object') return Object.values(value).every(finiteJson);
  return true;
}

const numberPattern = /^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$/;

export function evaluateDraft(field: PluginConfigField, draft: FieldDraft): FieldResult {
  if (!draft.set) return { ok: true, absent: true, value: undefined };
  if (hasEnum(field)) {
    if (draft.option < 0 || draft.option >= field.enum!.length) return { ok: false, error: 'Choose one of the listed options.' };
    return { ok: true, absent: false, value: field.enum![draft.option] };
  }
  switch (field.type) {
    case 'boolean': return { ok: true, absent: false, value: draft.checked };
    case 'number': {
      const text = draft.text.trim();
      if (!text) return { ok: false, error: 'Enter a number, or discard this change.' };
      if (!numberPattern.test(text) || !Number.isFinite(Number(text))) return { ok: false, error: 'Must be a finite number such as 3 or 0.5.' };
      return { ok: true, absent: false, value: Number(text) };
    }
    case 'list': case 'map': {
      let parsed: unknown;
      try { parsed = JSON.parse(draft.text); } catch (cause) { return { ok: false, error: `Invalid JSON: ${cause instanceof Error ? cause.message : String(cause)}` }; }
      if (!finiteJson(parsed)) return { ok: false, error: 'JSON numbers must be finite.' };
      if (field.type === 'list' && !Array.isArray(parsed)) return { ok: false, error: 'Must be a JSON array, for example ["a", "b"].' };
      if (field.type === 'map' && (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed))) return { ok: false, error: 'Must be a JSON object, for example {"key": "value"}.' };
      return { ok: true, absent: false, value: parsed };
    }
    default: return { ok: true, absent: false, value: draft.text };
  }
}

export type FieldModel = {
  key: string;
  field: PluginConfigField;
  label: string;
  baseline: Baseline;
  /** Human explanation of where the shown value comes from at this scope. */
  provenance: string;
  /** Extra explanation of the effective value when this scope stores nothing. */
  effective: string;
};

export type ScopeValues = {
  /** Raw stored keys for board/lane scope, or default-resolved values for instance scope. */
  values: Record<string, unknown>;
  /** Instance values, used for board fallback explanations. */
  instanceValues?: Record<string, unknown>;
  instanceSchema?: Record<string, PluginConfigField>;
};

export function buildFields(scope: SettingsScope, schema: Record<string, PluginConfigField>, values: ScopeValues): FieldModel[] {
  return Object.keys(schema).sort().map((key) => {
    const field = schema[key];
    const present = hasOwn(values.values, key);
    const baseline: Baseline = { present, value: present ? values.values[key] : undefined };
    let provenance = '';
    let effective = '';
    if (field.secret) {
      provenance = 'Secret: supplied through the plugin service environment. Docket never stores, shows or saves it.';
    } else if (scope === 'instance') {
      provenance = present ? 'Current instance value (stored or default; the API does not distinguish them).' : 'Not set.';
    } else {
      const where = scope === 'workspace' ? 'this board' : 'this lane';
      if (present) provenance = `Stored on ${where}.`;
      else if (hasDefault(field)) provenance = `Not stored on ${where}; the schema default ${formatValue(field.default)} applies.`;
      else provenance = `Not set on ${where}.`;
      if (scope === 'workspace' && !present && !hasDefault(field) && values.instanceSchema && hasOwn(values.instanceSchema, key) && values.instanceValues && hasOwn(values.instanceValues, key) && !values.instanceSchema[key].secret) {
        effective = `Effective value falls back to the instance value ${formatValue(values.instanceValues[key])} until this board stores one.`;
      } else if (scope === 'workspace' && present && values.instanceSchema && hasOwn(values.instanceSchema, key)) {
        effective = 'Overrides the instance value of the same key for this board.';
      }
    }
    return { key, field, label: humanizeKey(key), baseline, provenance, effective };
  });
}

/** Stored keys the current plugin version does not declare. They block every save until repaired. */
export function undeclaredKeys(schema: Record<string, PluginConfigField>, values: Record<string, unknown>): string[] {
  return Object.keys(values).filter((key) => !hasOwn(schema, key)).sort();
}

export function scopeSchema(schemas: PluginConfigSchemas | undefined, scope: SettingsScope): Record<string, PluginConfigField> | undefined {
  if (!schemas) return undefined;
  return scope === 'instance' ? schemas.instance : scope === 'workspace' ? schemas.workspace : schemas.status;
}

export function scopeValues(entry: PluginCatalogueEntry, target: FormTarget): ScopeValues {
  if (target.scope === 'instance') return { values: entry.instance_values };
  const workspace = entry.workspace_values[target.workspace];
  if (target.scope === 'workspace') return { values: workspace?.config || {}, instanceValues: entry.instance_values, instanceSchema: invalidSchemaReason(entry.schemas.instance, 'instance') ? undefined : entry.schemas.instance };
  return { values: workspace?.statuses[target.status] || {} };
}

/** Fingerprint of everything a form depends on: schema, version and stored values. */
export function formFingerprint(entry: PluginCatalogueEntry, target: FormTarget) {
  return canonical({ version: entry.version, schema: scopeSchema(entry.schemas, target.scope) || {}, values: scopeValues(entry, target) });
}

export type FormEvaluation = { values: Record<string, unknown>; errors: Record<string, string>; editedKeys: string[] };

/** Collects the PATCH payload from edited keys and every local error that blocks it. */
export function evaluateForm(fields: FieldModel[], drafts: Record<string, FieldDraft>, baselines: Record<string, FieldDraft>): FormEvaluation {
  const values: Record<string, unknown> = Object.create(null);
  const errors: Record<string, string> = Object.create(null);
  const editedKeys: string[] = [];
  for (const model of fields) {
    if (model.field.secret) continue;
    const draft = drafts[model.key];
    const result = evaluateDraft(model.field, draft);
    const edited = !draftEquals(draft, baselines[model.key]);
    if (!result.ok) { errors[model.key] = result.error; if (edited) editedKeys.push(model.key); continue; }
    if (result.absent) {
      if (model.field.required && !hasDefault(model.field)) errors[model.key] = 'Required by the plugin. Set a value before saving.';
      continue;
    }
    if (edited) { editedKeys.push(model.key); values[model.key] = result.value; }
  }
  return { values, errors, editedKeys };
}

/** Best-effort association of a server message such as "config.workspace.priority: must be one of [...]" with a field. */
export function fieldForServerError(message: string, keys: string[]): string {
  for (const key of keys) {
    if (new RegExp(`config\\.[a-z0-9._-]*\\.${key.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\b`).test(message)) return key;
  }
  return '';
}
