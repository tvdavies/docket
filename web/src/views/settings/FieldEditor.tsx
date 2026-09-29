import { useLayoutEffect, useRef } from 'react';
import { Button } from '../../components/ui/button';
import { Checkbox } from '../../components/ui/checkbox';
import { Input } from '../../components/ui/input';
import { Textarea } from '../../components/ui/textarea';
import { evaluateDraft, formatValue, hasDefault, hasDynamicOptions, hasEnum, isJsonKind, optionLabel, seedDraft, type FieldDraft, type FieldModel } from './model';
import { useFieldOptions } from './useFieldOptions';

export function FieldEditor({ id, plugin, model, draft, edited, error, disabled, onChange, onDiscard }: {
  id: string; plugin: string; model: FieldModel; draft: FieldDraft; edited: boolean; error: string; disabled: boolean;
  onChange(draft: FieldDraft): void; onDiscard(): void;
}) {
  const { field, key } = model;
  const describedBy = [field.description ? `${id}-description` : '', `${id}-provenance`, error ? `${id}-error` : ''].filter(Boolean).join(' ');
  const controlId = `${id}-control`;
  const focusNewEditor = useRef(false);
  useLayoutEffect(() => {
    if (focusNewEditor.current && draft.set) {
      focusNewEditor.current = false;
      document.getElementById(controlId)?.focus();
    }
  }, [controlId, draft.set]);
  const jsonKind = isJsonKind(field);
  const dynamic = hasDynamicOptions(field);
  const choices = useFieldOptions(plugin, field);
  // The stored value stays selectable even when the plugin no longer offers it.
  const offered = choices.options && draft.set && draft.text !== '' && !choices.options.some((option) => String(option.value) === draft.text)
    ? [...choices.options, { value: draft.text, label: `${draft.text} (not offered)` }]
    : choices.options;
  const canFormat = jsonKind && !hasEnum(field) && draft.set && evaluateDraft(field, draft).ok;
  return (
    <div className={`settings-field ${edited ? 'edited' : ''} ${error ? 'invalid' : ''}`} data-key={key}>
      <div className="settings-field-head">
        <label htmlFor={field.secret ? undefined : controlId} id={`${id}-label`}>
          <span className="settings-field-label">{model.label}</span> <code className="settings-field-key">{key}</code>
          {field.required && <span className="settings-required">Required</span>}
          {field.secret && <span className="settings-secret-badge">Secret</span>}
        </label>
        <span className="settings-field-type">{hasEnum(field) || dynamic ? `${field.type} · choice` : field.type}{edited && <em> · edited</em>}</span>
      </div>
      {field.description && <p className="settings-field-description" id={`${id}-description`}>{field.description}</p>}
      <p className="settings-provenance" id={`${id}-provenance`}>
        {model.provenance}{model.effective ? ` ${model.effective}` : ''}
        {hasDefault(field) && !field.secret && <span className="settings-default"> Schema default: <code>{formatValue(field.default)}</code>.</span>}
      </p>
      {field.secret ? (
        <p className="settings-secret-note">Configured only through the plugin service environment. There is nothing to enter here.</p>
      ) : !draft.set ? (
        <div className="settings-field-actions">
          <Button type="button" variant="outline" size="sm" id={controlId} aria-label={`Set value for ${model.label}`} aria-describedby={describedBy} disabled={disabled} onClick={() => { focusNewEditor.current = true; onChange(seedDraft(field)); }}>Set value</Button>
        </div>
      ) : (
        <div className="settings-control">
          {hasEnum(field) ? (
            <select id={controlId} value={draft.option < 0 ? '' : String(draft.option)} aria-describedby={describedBy} aria-invalid={error ? true : undefined} disabled={disabled} onChange={(event) => onChange({ ...draft, option: event.target.value === '' ? -1 : Number(event.target.value) })}>
              <option value="">Choose…</option>
              {field.enum!.map((option, index) => <option key={index} value={String(index)}>{optionLabel(option)}</option>)}
            </select>
          ) : dynamic && offered ? (
            <select id={controlId} value={draft.text} aria-describedby={describedBy} aria-invalid={error ? true : undefined} disabled={disabled} onChange={(event) => onChange({ ...draft, text: event.target.value })}>
              <option value="">Choose…</option>
              {offered.map((option) => <option key={String(option.value)} value={String(option.value)}>{option.label}</option>)}
            </select>
          ) : field.type === 'boolean' ? (
            <span className="settings-boolean">
              <Checkbox id={controlId} checked={draft.checked} aria-describedby={describedBy} aria-invalid={error ? true : undefined} disabled={disabled} onCheckedChange={(checked) => onChange({ ...draft, checked: checked === true })} />
              <label htmlFor={controlId}>{draft.checked ? 'true' : 'false'}</label>
            </span>
          ) : field.type === 'number' ? (
            <Input id={controlId} type="text" inputMode="decimal" autoComplete="off" value={draft.text} aria-describedby={describedBy} aria-invalid={error ? true : undefined} disabled={disabled} onChange={(event) => onChange({ ...draft, text: event.target.value })} />
          ) : jsonKind ? (
            <Textarea id={controlId} className="settings-json" rows={Math.min(12, Math.max(3, draft.text.split('\n').length))} spellCheck={false} autoComplete="off" value={draft.text} placeholder={field.type === 'list' ? '["first", "second"]' : '{"key": "value"}'} aria-describedby={describedBy} aria-invalid={error ? true : undefined} disabled={disabled} onChange={(event) => onChange({ ...draft, text: event.target.value })} />
          ) : (
            <Input id={controlId} type="text" autoComplete="off" value={draft.text} aria-describedby={describedBy} aria-invalid={error ? true : undefined} disabled={disabled} onChange={(event) => onChange({ ...draft, text: event.target.value })} />
          )}
          {dynamic && <p className="settings-options-status" role={choices.error ? 'alert' : undefined}>
            {choices.loading ? 'Loading options from the plugin…' : choices.error ? `Could not load options (${choices.error}); enter a value directly.` : `Options served by ${plugin} at `}
            {!choices.loading && !choices.error && <code>{field.options_from}</code>}
            {' '}<Button type="button" variant="ghost" size="sm" disabled={choices.loading} onClick={choices.refresh}>Refresh options</Button>
          </p>}
          {jsonKind && <p className="settings-json-hint">JSON {field.type === 'list' ? 'array' : 'object'}. Saving replaces the whole stored value; an empty <code>{field.type === 'list' ? '[]' : '{}'}</code> is a real value.</p>}
        </div>
      )}
      {error && <p className="settings-field-error" id={`${id}-error`}>{error}</p>}
      {!field.secret && (canFormat || edited) && <div className="settings-field-actions">
        {canFormat && <Button type="button" variant="ghost" size="sm" disabled={disabled} onClick={() => { const result = evaluateDraft(field, draft); if (result.ok) onChange({ ...draft, text: JSON.stringify(result.value, null, 2) }); }}>Format JSON</Button>}
        {edited && <Button type="button" variant="ghost" size="sm" disabled={disabled} onClick={onDiscard}>Discard change</Button>}
      </div>}
    </div>
  );
}
