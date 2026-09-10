import { useCallback, useEffect, useLayoutEffect, useId, useRef } from 'react';
import { Button } from '../../components/ui/button';
import { invalidSchemaReason, type PluginCatalogueEntry } from '../../api/plugin-settings';
import { FieldEditor } from './FieldEditor';
import { humanizeKey, scopeSchema, type FormTarget } from './model';
import { useSettingsForm, type SettingsFormOptions } from './useSettingsForm';

export function storageDescription(target: FormTarget) {
  if (target.scope === 'instance') return { title: 'Instance settings', where: 'machine registry (plugins[].config)', note: 'Changes affect every board on this machine that enables the plugin.' };
  if (target.scope === 'workspace') return { title: `Board settings for ${target.workspace}`, where: `board config plugins.${target.plugin}.config`, note: 'Board values and board defaults override same-named instance values for this board only.' };
  return { title: `Lane settings for ${humanizeKey(target.status)}`, where: `board config plugins.${target.plugin}.statuses.${target.status}`, note: 'Lane values do not override instance or board config, and do not edit any Dispatch pipeline file. Lane defaults apply independently to every lane.' };
}

export function PluginSettingsForm(props: Omit<SettingsFormOptions, 'onFocusField'> & { entry: PluginCatalogueEntry; onDirtyChange?(dirty: boolean): void; onBusyChange?(busy: boolean): void }) {
  const { entry, target } = props;
  const id = useId().replace(/:/g, '');
  const fieldId = useCallback((key: string) => `${id}-${key}`, [id]);
  const summaryRef = useRef<HTMLDivElement>(null);
  const focusField = useCallback((key: string) => {
    const control = document.getElementById(`${fieldId(key)}-control`) as HTMLElement | null;
    if (control) control.focus(); else summaryRef.current?.focus();
  }, [fieldId]);
  const focusFrame = useRef(0);
  const focusAfterRender = useCallback((key: string) => {
    cancelAnimationFrame(focusFrame.current);
    focusFrame.current = requestAnimationFrame(() => focusField(key));
  }, [focusField]);
  useEffect(() => () => cancelAnimationFrame(focusFrame.current), []);
  const form = useSettingsForm({ ...props, onFocusField: focusAfterRender });
  const { onDirtyChange } = props;
  useLayoutEffect(() => { onDirtyChange?.(form.dirty); }, [form.dirty, onDirtyChange]);
  useEffect(() => () => onDirtyChange?.(false), [onDirtyChange]);

  const schemaProblem = invalidSchemaReason(scopeSchema(entry.schemas, target.scope), target.scope);
  const storage = storageDescription(target);
  const errorKeys = form.fields.filter((model) => form.errors[model.key]).map((model) => model.key);
  const busy = form.status === 'saving';
  const readBackRequired = form.status === 'uncertain' || form.status === 'saved-reload-failed';
  const editing = !busy && !readBackRequired && !props.blocked && !schemaProblem && !form.staleNotice && !form.undeclared.length;
  const { onBusyChange } = props;
  useLayoutEffect(() => { onBusyChange?.(busy); return () => onBusyChange?.(false); }, [busy, onBusyChange]);

  return (
    <section className="settings-plugin" aria-labelledby={`${id}-title`}>
      <header className="settings-plugin-head">
        <div>
          <h2 id={`${id}-title`}>{entry.name} <small>v{entry.version}</small></h2>
          {entry.description && <p className="settings-plugin-description">{entry.description}</p>}
        </div>
        <dl className="settings-storage"><dt>Scope</dt><dd>{storage.title}</dd><dt>Stored in</dt><dd><code>{storage.where}</code></dd></dl>
      </header>
      {props.blocked && <div className="settings-banner error-banner" role="alert">{props.blocked} This form is read-only; drafts are kept.</div>}
      {schemaProblem && <div className="notice error-banner settings-banner" role="alert">Unsupported schema: {schemaProblem}. Saving is blocked; repair the plugin declaration. Previous drafts are kept.</div>}
      <p className="settings-scope-note">{storage.note}</p>
      {!form.fields.length && <p className="settings-empty">This plugin declares no {target.scope === 'workspace' ? 'board' : target.scope === 'status' ? 'lane' : 'instance'} settings.</p>}
      {form.undeclared.length > 0 && <div className="notice error-banner settings-banner" role="alert">
        Stored keys not declared by {entry.name} v{entry.version}: {form.undeclared.map((key) => <code key={key}>{key}</code>)}. Docket keeps them in the saved configuration and rejects every write until an operator removes or renames them in <code>{storage.where}</code>. Nothing on this form can be saved meanwhile.
      </div>}
      {form.staleNotice && <div className="notice settings-banner settings-stale" role="alert">
        <span>Configuration changed since you loaded this form. Your unsaved edits are kept but cannot be saved against outdated values.</span>
        <span><Button type="button" size="sm" variant="outline" disabled={!!schemaProblem || busy} onClick={form.reviewRefreshed}>Review refreshed values</Button></span>
      </div>}
      {form.fields.length > 0 && <form className="settings-form" noValidate onSubmit={(event) => { event.preventDefault(); void form.save(); }} aria-busy={busy || undefined}>
        <fieldset className="settings-fieldset" disabled={!editing}>
          <legend className="sr-only">{storage.title} for {entry.name}</legend>
          {errorKeys.length > 0 && <div className="settings-error-summary" ref={summaryRef} tabIndex={-1} role="alert">
            <p>{errorKeys.length === 1 ? 'One field needs attention:' : `${errorKeys.length} fields need attention:`}</p>
            <ul>{errorKeys.map((key) => <li key={key}><a href={`#${fieldId(key)}-control`} onClick={(event) => { event.preventDefault(); focusField(key); }}>{humanizeKey(key)}</a>: {form.errors[key]}</li>)}</ul>
          </div>}
          {form.fields.map((model) => <FieldEditor key={model.key} id={fieldId(model.key)} model={model} draft={form.drafts[model.key]} edited={form.editedKeys.includes(model.key)} error={form.errors[model.key] || ''} disabled={!editing} onChange={(draft) => form.setDraft(model.key, draft)} onDiscard={() => form.discardField(model.key)} />)}
        </fieldset>
        <div className="settings-actions">
          <Button type="submit" disabled={!form.canSave || !!schemaProblem}>{busy ? 'Saving…' : 'Save'}</Button>
          <Button type="button" variant="outline" disabled={!form.dirty || busy || readBackRequired} onClick={form.discardAll}>Discard changes</Button>
          {(form.status === 'saved-reload-failed' || form.status === 'uncertain') && <Button type="button" variant="outline" onClick={() => void form.retryReload()}>Reload current values</Button>}
          <span className="settings-dirty" aria-live="polite">{form.dirty ? `${form.editedKeys.length} unsaved ${form.editedKeys.length === 1 ? 'change' : 'changes'}` : ''}</span>
        </div>
        <p className={`settings-status settings-status-${form.status}`} role="status" aria-live="polite" data-status={form.status}>{form.message}</p>
      </form>}
    </section>
  );
}
