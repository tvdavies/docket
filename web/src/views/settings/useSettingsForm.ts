import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { invalidSchemaReason, patchInstanceConfig, patchStatusConfig, patchWorkspaceConfig, type PluginCatalogueEntry } from '../../api/plugin-settings';
import { buildFields, canonical, draftEquals, draftFromValue, evaluateDraft, evaluateForm, fieldForServerError, formFingerprint, scopeSchema, scopeValues, undeclaredKeys, type FieldDraft, type FieldModel, type FormTarget } from './model';

export type SaveStatus = 'idle' | 'invalid' | 'saving' | 'saved' | 'saved-reload-failed' | 'failed' | 'uncertain' | 'stale';

export type SettingsFormState = {
  fields: FieldModel[];
  drafts: Record<string, FieldDraft>;
  baselines: Record<string, FieldDraft>;
  /** Errors currently shown: live feedback for edited fields, server errors, and required gaps after a save attempt. */
  errors: Record<string, string>;
  editedKeys: string[];
  dirty: boolean;
  status: SaveStatus;
  message: string;
  /** Stored keys the declared schema does not know; saving is blocked until an operator repairs the file. */
  undeclared: string[];
  /** The catalogue changed underneath unsaved edits. Saving is blocked until the user reviews it. */
  staleNotice: boolean;
  canSave: boolean;
  setDraft(key: string, draft: FieldDraft): void;
  discardField(key: string): void;
  discardAll(): void;
  reviewRefreshed(): void;
  save(): Promise<void>;
  retryReload(): Promise<void>;
};

export type SettingsFormOptions = {
  entry: PluginCatalogueEntry;
  target: FormTarget;
  /** Refetches the catalogue and returns the fresh entries; rejects on failure. */
  reload(): Promise<PluginCatalogueEntry[]>;
  /** Returns a message when the target can no longer be written (workspace unavailable, lane removed); empty when fine. */
  checkTarget(entries: PluginCatalogueEntry[]): Promise<string> | string;
  /** External reason writes are blocked, such as a stale catalogue. */
  blocked?: string;
  onFocusField?(key: string): void;
};

const isNetworkFailure = (cause: unknown) => cause instanceof TypeError || cause instanceof SyntaxError || (cause instanceof DOMException && cause.name === 'AbortError');
const describe = (cause: unknown) => cause instanceof Error ? cause.message : String(cause);

type Applied = { fingerprint: string; baselines: Record<string, FieldDraft>; fields: FieldModel[] };
const baselinesFor = (models: FieldModel[]) => Object.fromEntries(models.map((model) => [model.key, draftFromValue(model.field, model.baseline)]));

export function useSettingsForm({ entry, target, reload, checkTarget, blocked = '', onFocusField }: SettingsFormOptions): SettingsFormState {
  const schema = useMemo(() => scopeSchema(entry.schemas, target.scope) || {}, [entry, target.scope]);
  const values = useMemo(() => scopeValues(entry, target), [entry, target]);
  const fields = useMemo(() => buildFields(target.scope, invalidSchemaReason(schema, target.scope) ? {} : schema, values), [target.scope, schema, values]);
  const fingerprint = useMemo(() => formFingerprint(entry, target), [entry, target]);

  const [applied, setApplied] = useState<Applied>(() => ({ fingerprint, baselines: baselinesFor(fields), fields }));
  const [drafts, setDrafts] = useState<Record<string, FieldDraft>>(() => baselinesFor(fields));
  const [status, setStatus] = useState<SaveStatus>('idle');
  const [message, setMessage] = useState('');
  const [attempted, setAttempted] = useState(false);
  const [serverErrors, setServerErrors] = useState<Record<string, string>>({});
  const [staleNotice, setStaleNotice] = useState(false);
  const generation = useRef(0);
  const saving = useRef(false);
  useEffect(() => () => { generation.current += 1; }, []);

  const dirty = useMemo(() => applied.fields.some((model) => !draftEquals(drafts[model.key], applied.baselines[model.key])), [applied, drafts]);

  // Adopt a refreshed baseline when it cannot lose input: nothing is unsaved, or the refreshed values already equal
  // the drafts (the usual post-save case). Otherwise raise a notice and leave the drafts alone.
  useEffect(() => {
    if ((fingerprint === applied.fingerprint && !(status === 'saved' && dirty)) || status === 'saving') return;
    const sameDeclarations = canonical(applied.fields.map((model) => [model.key, model.field])) === canonical(fields.map((model) => [model.key, model.field]));
    const draftsMatchRefreshed = sameDeclarations && fields.every((model) => {
      const draft = drafts[model.key];
      const result = draft ? evaluateDraft(model.field, draft) : null;
      if (!result || !result.ok) return false;
      return result.absent ? !model.baseline.present : model.baseline.present && canonical(result.value) === canonical(model.baseline.value);
    });
    if (!dirty || (status === 'saved' && draftsMatchRefreshed)) {
      const baselines = baselinesFor(fields);
      setApplied({ fingerprint, baselines, fields });
      setDrafts(baselines);
      setServerErrors({});
      setStaleNotice(false);
      if (status === 'stale' || status === 'saved-reload-failed' || status === 'uncertain') { setStatus('idle'); setMessage(''); }
    } else {
      setStaleNotice(true);
    }
  }, [fingerprint, applied, dirty, status, fields, drafts]);

  const evaluation = useMemo(() => evaluateForm(applied.fields, drafts, applied.baselines), [applied, drafts]);
  const requiredGaps = useMemo(() => Object.fromEntries(Object.entries(evaluation.errors).filter(([key]) => !evaluation.editedKeys.includes(key))), [evaluation]);
  const blockingErrors = useMemo(() => Object.fromEntries(Object.entries(evaluation.errors).filter(([key]) => evaluation.editedKeys.includes(key) || (applied.fields.find((model) => model.key === key)?.field.required && !applied.baselines[key]?.set))), [evaluation, applied]);
  const errors = useMemo(() => {
    // Schema keys such as "constructor" are valid; inherited object properties are not errors.
    const visible: Record<string, string> = Object.assign(Object.create(null), serverErrors);
    for (const [key, text] of Object.entries(evaluation.errors)) if (!(key in visible) && (evaluation.editedKeys.includes(key) || (attempted && key in requiredGaps))) visible[key] = text;
    return visible;
  }, [serverErrors, evaluation, attempted, requiredGaps]);
  const undeclared = useMemo(() => undeclaredKeys(schema, values.values), [schema, values.values]);
  const busy = status === 'saving';
  const canSave = !busy && !staleNotice && !blocked && !undeclared.length && status !== 'saved-reload-failed' && status !== 'uncertain' && evaluation.editedKeys.length > 0 && !Object.keys(blockingErrors).length;

  const setDraft = useCallback((key: string, draft: FieldDraft) => {
    setDrafts((current) => ({ ...current, [key]: draft }));
    setServerErrors((current) => { if (!(key in current)) return current; const next = { ...current }; delete next[key]; return next; });
    setStatus((current) => (current === 'saved' || current === 'failed' || current === 'invalid') ? 'idle' : current);
  }, []);
  const discardField = useCallback((key: string) => { setDraft(key, applied.baselines[key]); }, [applied.baselines, setDraft]);
  const discardAll = useCallback(() => { if (saving.current || status === 'uncertain' || status === 'saved-reload-failed') return; setDrafts({ ...applied.baselines }); setServerErrors({}); setAttempted(false); setStatus('idle'); setMessage(''); }, [applied.baselines, status]);

  /** Deliberately re-bases on the refreshed catalogue. Edits to fields whose declaration is unchanged are kept; nothing else carries over. */
  const reviewRefreshed = useCallback(() => {
    const baselines = baselinesFor(fields);
    const nextDrafts: Record<string, FieldDraft> = { ...baselines };
    let kept = 0;
    for (const model of fields) {
      const previous = applied.fields.find((item) => item.key === model.key);
      const sameDeclaration = previous && canonical(previous.field) === canonical(model.field);
      const wasEdited = previous && drafts[model.key] && !draftEquals(drafts[model.key], applied.baselines[model.key]);
      if (sameDeclaration && wasEdited) { nextDrafts[model.key] = drafts[model.key]; kept += 1; }
    }
    setApplied({ fingerprint, baselines, fields });
    setDrafts(nextDrafts);
    setServerErrors({});
    setStaleNotice(false);
    setStatus('idle');
    setMessage(kept ? `Loaded the current configuration and kept ${kept} unsaved ${kept === 1 ? 'edit' : 'edits'} to unchanged fields. Review them before saving.` : 'Loaded the current configuration.');
  }, [applied, drafts, fields, fingerprint]);

  const focusFirstError = useCallback((problems: Record<string, string>) => {
    const first = applied.fields.find((model) => Object.prototype.hasOwnProperty.call(problems, model.key));
    if (first) onFocusField?.(first.key);
  }, [applied.fields, onFocusField]);

  const save = useCallback(async () => {
    if (saving.current || busy || staleNotice || blocked || undeclared.length || status === 'saved-reload-failed' || status === 'uncertain') return;
    const local = evaluateForm(applied.fields, drafts, applied.baselines);
    const blocking = Object.fromEntries(Object.entries(local.errors).filter(([key]) => local.editedKeys.includes(key) || (applied.fields.find((model) => model.key === key)?.field.required && !applied.baselines[key]?.set)));
    setAttempted(true);
    if (Object.keys(blocking).length) { setStatus('invalid'); setMessage('Fix the highlighted fields before saving. Nothing was sent.'); focusFirstError(blocking); return; }
    if (!local.editedKeys.length) { setStatus('idle'); setMessage('Nothing to save.'); return; }
    saving.current = true;
    try {
    const token = generation.current;
    const capturedTarget = target;
    const payload = local.values;
    setStatus('saving'); setMessage('Checking current configuration…'); setServerErrors({});
    let fresh: PluginCatalogueEntry[];
    try { fresh = await reload(); } catch (cause) {
      if (token !== generation.current) return;
      setStatus('failed'); setMessage(`Could not verify the current configuration before saving: ${describe(cause)}. Nothing was sent; your edits are kept.`);
      return;
    }
    if (token !== generation.current) return;
    const current = fresh.find((item) => item.name === capturedTarget.plugin);
    if (!current) { setStatus('failed'); setMessage(`Plugin ${capturedTarget.plugin} is no longer installed. Nothing was sent; your edits are kept.`); return; }
    let targetProblem = '';
    try { targetProblem = await checkTarget(fresh); }
    catch (cause) { targetProblem = `Could not verify the owning scope: ${describe(cause)}.`; }
    if (token !== generation.current) return;
    if (targetProblem) { setStatus('failed'); setMessage(`${targetProblem} Nothing was sent; your edits are kept.`); return; }
    if (formFingerprint(current, capturedTarget) !== applied.fingerprint) { setStaleNotice(true); setStatus('stale'); setMessage('Configuration changed since you loaded it. Nothing was sent; review the refreshed values before saving.'); return; }
    setMessage('Saving…');
    try {
      if (capturedTarget.scope === 'instance') await patchInstanceConfig(capturedTarget.plugin, payload);
      else if (capturedTarget.scope === 'workspace') await patchWorkspaceConfig(capturedTarget.workspace, capturedTarget.plugin, payload);
      else await patchStatusConfig(capturedTarget.workspace, capturedTarget.plugin, capturedTarget.status, payload);
    } catch (cause) {
      if (token !== generation.current) return;
      const text = describe(cause);
      if (isNetworkFailure(cause)) { setStatus('uncertain'); setMessage(`The save request failed before Docket answered (${text}). It may or may not have been applied. Reload the current values before trying again; your edits are kept.`); return; }
      setStatus('failed');
      const key = fieldForServerError(text, local.editedKeys) || fieldForServerError(text, applied.fields.map((model) => model.key));
      if (key) { setServerErrors({ [key]: text }); focusFirstError({ [key]: text }); }
      setMessage(`Docket rejected the save: ${text}. Your edits are kept. Validation failures do not change stored configuration.`);
      return;
    }
    if (token !== generation.current) return;
    const count = local.editedKeys.length;
    const summary = `Saved ${count} ${count === 1 ? 'setting' : 'settings'}`;
    try {
      await reload();
      if (token !== generation.current) return;
      setStatus('saved'); setMessage(`${summary}. Saving configuration does not check whether the plugin service is healthy.`);
      setAttempted(false);
    } catch (cause) {
      if (token !== generation.current) return;
      setStatus('saved-reload-failed'); setMessage(`${summary}, but the current values could not be reloaded: ${describe(cause)}. Showing what was submitted.`);
    }
    } finally { saving.current = false; }
  }, [busy, staleNotice, blocked, undeclared.length, status, applied, drafts, target, reload, checkTarget, focusFirstError]);

  const retryReload = useCallback(async () => {
    const token = generation.current;
    setMessage('Reloading current values…');
    try { await reload(); if (token === generation.current) { setStatus('idle'); setMessage('Reloaded the current values.'); } }
    catch (cause) { if (token === generation.current) setMessage(`Could not reload the current values: ${describe(cause)}`); }
  }, [reload]);

  return { fields: applied.fields, drafts, baselines: applied.baselines, errors, editedKeys: evaluation.editedKeys, dirty, status, message, undeclared, staleNotice, canSave, setDraft, discardField, discardAll, reviewRefreshed, save, retryReload };
}
