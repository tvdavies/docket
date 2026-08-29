import { useEffect, useState } from 'react';
import * as Dialog from '@radix-ui/react-dialog';
import type { CreateTaskInput, StreamConfig } from '../../types';

export function CreateTaskDialog({ open, config, onOpenChange, onCreate }: { open: boolean; config: StreamConfig; onOpenChange(open: boolean): void; onCreate(input: CreateTaskInput): Promise<void> }) {
  const [title, setTitle] = useState(''); const [description, setDescription] = useState(''); const [status, setStatus] = useState(''); const [assignee, setAssignee] = useState(''); const [labels, setLabels] = useState(''); const [error, setError] = useState(''); const [busy, setBusy] = useState(false);
  useEffect(() => { if (open) { setTitle(''); setDescription(''); setStatus(config.statuses[0] || ''); setAssignee(''); setLabels(''); setError(''); } }, [open, config.statuses]);
  return <Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal><Dialog.Overlay className="dialog-overlay" /><Dialog.Content className="modal-card" aria-describedby={undefined}><Dialog.Title>New task</Dialog.Title>
    <label>Title<input autoFocus value={title} onChange={(event) => setTitle(event.target.value)} /></label><label>Status<select value={status} onChange={(event) => setStatus(event.target.value)}>{config.statuses.map((item) => <option key={item}>{item}</option>)}</select></label><label>Assignee<input value={assignee} onChange={(event) => setAssignee(event.target.value)} /></label><label>Labels<input value={labels} onChange={(event) => setLabels(event.target.value)} placeholder="comma separated" /></label><label>Description<textarea rows={6} value={description} onChange={(event) => setDescription(event.target.value)} /></label>
    {error && <p className="error-text">{error}</p>}<div className="button-row"><Dialog.Close asChild><button>Cancel</button></Dialog.Close><button className="primary-button" disabled={busy || !title.trim()} onClick={async () => { setBusy(true); try { await onCreate({ title: title.trim(), description, status, assignee: assignee.trim(), labels: [...new Set(labels.split(',').map((item) => item.trim()).filter(Boolean))] }); onOpenChange(false); } catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); } finally { setBusy(false); } }}>{busy ? 'Creating…' : 'Create task'}</button></div>
  </Dialog.Content></Dialog.Portal></Dialog.Root>;
}
