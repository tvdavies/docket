import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import * as Dialog from '@radix-ui/react-dialog';
import type { LivePayload, StreamConfig, TaskDetail as TaskDetailValue, TaskPatch } from '../../types';
import { addComment, addReference, getActor, getTask, removeReference, resolveWait, taskPath, uploadAttachment } from '../../api/client';
import { ResolvedReference } from '../../registry/ResolvedReference';
import { PluginCardHost } from '../../registry/PluginCardHost';

const humanize = (value: string) => value.replace(/[._-]+/g, ' ').replace(/\b\w/g, (letter) => letter.toUpperCase());
const formatDate = (value: string) => new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value));

export function TaskDetail({ workspace, taskId, open, config, live, summaryUpdatedAt, onClose, onPatch, onCursor, onDraftChange }: {
  workspace: string; taskId: string; open: boolean; config: StreamConfig; live: LivePayload[]; summaryUpdatedAt?: string;
  onClose(): void; onPatch(task: string, patch: TaskPatch): Promise<TaskDetailValue>; onCursor(cursor?: string): void; onDraftChange?(dirty: boolean): void;
}) {
  const [detail, setDetail] = useState<TaskDetailValue | null>(null);
  const [error, setError] = useState('');
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [saveError, setSaveError] = useState('');
  const [propertyDraft, setPropertyDraft] = useState(false);
  const [waitDraft, setWaitDraft] = useState(false);
  const [resourceDraft, setResourceDraft] = useState(false);
  const [comment, setComment] = useState('');
  const [busy, setBusy] = useState(false);
  const request = useRef(0);
  const hasDraft = editing || propertyDraft || waitDraft || resourceDraft || Boolean(comment.trim());
  const acceptDetail = (value: TaskDetailValue) => { request.current += 1; setDetail(value); onCursor(value.cursor); };
  useEffect(() => { onDraftChange?.(hasDraft); return () => onDraftChange?.(false); }, [hasDraft, onDraftChange]);
  useEffect(() => { const beforeUnload = (event: BeforeUnloadEvent) => { if (hasDraft) event.preventDefault(); }; window.addEventListener('beforeunload', beforeUnload); return () => window.removeEventListener('beforeunload', beforeUnload); }, [hasDraft]);

  const load = async (background = false) => {
    const token = ++request.current;
    try {
      const value = await getTask(workspace, taskId);
      if (token !== request.current || (background && hasDraft)) return;
      setDetail(value); setTitle(value.title); setDescription(value.description); setError(''); onCursor(value.cursor);
    } catch (cause) { if (token === request.current) setError(cause instanceof Error ? cause.message : String(cause)); }
  };

  useEffect(() => {
    if (!open || !taskId) return;
    setDetail(null); setEditing(false); setPropertyDraft(false); setWaitDraft(false); setResourceDraft(false); setComment(''); setSaveError(''); void load();
    return () => { request.current += 1; };
  }, [open, taskId, workspace]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (open && detail && summaryUpdatedAt && summaryUpdatedAt !== detail.updated_at && !hasDraft) void load(true);
  }, [summaryUpdatedAt]); // eslint-disable-line react-hooks/exhaustive-deps

  const patch = async (value: TaskPatch) => {
    request.current += 1;
    setBusy(true); setSaveError('');
    try {
      const updated = await onPatch(taskId, value);
      acceptDetail(updated); setTitle(updated.title); setDescription(updated.description); setEditing(false); setPropertyDraft(false);
      return updated;
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : String(cause); setSaveError(message); throw cause;
    } finally { setBusy(false); }
  };

  const close = () => {
    if (hasDraft && !window.confirm('Discard unsaved task input?')) return;
    onClose();
  };

  const liveForTask = live.filter((item) => item.task === taskId);

  return (
    <Dialog.Root open={open} onOpenChange={(next) => { if (!next) close(); }}>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content className="task-detail-panel" aria-describedby={undefined}>
          <header className="detail-header"><div><span className="task-id">{taskId}</span><Dialog.Title>{detail?.title || 'Task'}</Dialog.Title></div><Dialog.Close asChild><button className="icon-button" onClick={(event) => { event.preventDefault(); close(); }} aria-label="Close task">×</button></Dialog.Close></header>
          {error && <div className="error-banner">Could not load task: {error} <button onClick={() => void load()}>Retry</button></div>}
          {!detail && !error && <div className="detail-loading">Loading task…</div>}
          {detail && <div className="detail-layout">
            <main className="detail-document">
              {liveForTask.length > 0 && <div className="live-detail">Live · {liveForTask.map((item) => item.kind).join(', ')}</div>}
              <PluginCardHost workspace={workspace} task={detailToBoard(detail)} refresh={() => void load(true)} />
              {editing ? <section className="edit-stack">
                <label>Title<input value={title} onChange={(event) => setTitle(event.target.value)} disabled={busy} /></label>
                <label>Description<textarea rows={12} value={description} onChange={(event) => setDescription(event.target.value)} disabled={busy} /></label>
                <div className="button-row"><button className="primary-button" disabled={busy || !title.trim()} onClick={() => void patch({ title: title.trim(), description })}>{busy ? 'Saving…' : 'Save'}</button><button onClick={() => { setEditing(false); setTitle(detail.title); setDescription(detail.description); setSaveError(''); }}>Cancel</button></div>
              </section> : <>
                <div className="document-heading"><h1>{detail.title}</h1><button onClick={() => setEditing(true)}>Edit</button></div>
                <section><h2>Description</h2>{detail.description_html ? <div className="markdown" dangerouslySetInnerHTML={{ __html: detail.description_html }} /> : <p className="muted">No description yet.</p>}</section>
              </>}
              {saveError && <div className="error-banner" role="alert">Not saved · {saveError}</div>}
              {detail.wait && <WaitPanel detail={detail} workspace={workspace} setDetail={acceptDetail} onCursor={onCursor} onDraftChange={setWaitDraft} />}
              <Resources detail={detail} workspace={workspace} setDetail={acceptDetail} onCursor={onCursor} onDraftChange={setResourceDraft} />
              <Relationships detail={detail} />
              <Activity detail={detail} />
              <section><h2>Comment</h2><textarea className="comment-box" rows={4} placeholder="Add durable context…" value={comment} onChange={(event) => setComment(event.target.value)} />
                <div className="button-row"><button className="primary-button" disabled={busy || !comment.trim()} onClick={async () => {
                  const text = comment.trim(); if (!text) return; setBusy(true); setComment('');
                  const previous = detail; const now = new Date().toISOString();
                  const actor = getActor(); setDetail({ ...detail, comments: [...detail.comments, { author: actor, created_at: now, body: text }], activity: [...detail.activity, { at: now, kind: 'comment', type: 'comment', actor, body: text }] });
                  try { request.current += 1; const updated = await addComment(workspace, taskId, text); acceptDetail(updated); }
                  catch (cause) { setDetail(previous); setComment(text); setSaveError(cause instanceof Error ? cause.message : String(cause)); }
                  finally { setBusy(false); }
                }}>Comment</button></div>
              </section>
            </main>
            <aside className="detail-properties"><h2>Properties</h2>
              <Property label="Status"><select value={detail.status} onChange={(event) => void patch({ status: event.target.value })}>{[...config.statuses, ...(!config.statuses.includes(detail.status) ? [detail.status] : [])].map((status) => <option key={status} value={status}>{humanize(status)}</option>)}</select></Property>
              <Property label="Assignee"><input key={`assignee-${detail.updated_at}`} defaultValue={detail.assignee || ''} placeholder="Unassigned" onFocus={() => setPropertyDraft(true)} onBlur={(event) => { if (event.target.value.trim() !== (detail.assignee || '')) void patch({ assignee: event.target.value.trim() }); else setPropertyDraft(false); }} /></Property>
              <Property label="Labels"><input key={`labels-${detail.updated_at}`} defaultValue={detail.labels.join(', ')} onFocus={() => setPropertyDraft(true)} onBlur={(event) => { const labels = [...new Set(event.target.value.split(',').map((item) => item.trim()).filter(Boolean))]; if (JSON.stringify(labels) !== JSON.stringify(detail.labels)) void patch({ labels }); else setPropertyDraft(false); }} /></Property>
              <Property label="Project"><span>{detail.project?.name || detail.project?.id || 'None'}</span></Property>
              <Property label="Created"><time>{formatDate(detail.created_at)}</time></Property><Property label="Updated"><time>{formatDate(detail.updated_at)}</time></Property>
              <a className="classic-link" href={`/classic/workspaces/${encodeURIComponent(workspace)}/tasks/${encodeURIComponent(taskId)}`}>Open classic task view</a>
            </aside>
          </div>}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

function Property({ label, children }: { label: string; children: ReactNode }) { return <label className="property"><span>{label}</span>{children}</label>; }

function WaitPanel({ detail, workspace, setDetail, onCursor, onDraftChange }: { detail: TaskDetailValue; workspace: string; setDetail(value: TaskDetailValue): void; onCursor(cursor?: string): void; onDraftChange(dirty: boolean): void }) {
  const [result, setResult] = useState(''); const [feedback, setFeedback] = useState(''); const [message, setMessage] = useState(''); const [busy, setBusy] = useState(false);
  useEffect(() => { onDraftChange(Boolean(result.trim() || feedback.trim())); return () => onDraftChange(false); }, [result, feedback, onDraftChange]);
  if (!detail.wait) return null;
  const waitReference = safeURL(detail.wait.reference || '');
  return <section className="wait-panel"><h2>Waiting · {humanize(detail.wait.kind)}</h2><p>{detail.wait.reason}</p>{waitReference && <a href={waitReference} target="_blank" rel="noreferrer">{detail.wait.reference}</a>}
    <input placeholder="Resolution, e.g. approved" value={result} onChange={(event) => setResult(event.target.value)} /><textarea rows={2} placeholder="Feedback (optional)" value={feedback} onChange={(event) => setFeedback(event.target.value)} />
    {message && <p className="error-text">{message}</p>}<button className="primary-button" disabled={busy} onClick={async () => {
      setBusy(true); setMessage(''); let commentSaved = false;
      try { if (feedback.trim()) { const commented = await addComment(workspace, detail.id, feedback.trim()); setDetail(commented); commentSaved = true; onCursor(commented.cursor); }
        const updated = await resolveWait(workspace, detail.id, detail.wait!.id, result.trim()); setDetail(updated); onCursor(updated.cursor);
      } catch (cause) { setMessage(`${commentSaved ? 'Feedback saved, but wait remains unresolved. ' : ''}${cause instanceof Error ? cause.message : String(cause)}`); }
      finally { setBusy(false); }
    }}>Resolve wait</button>
  </section>;
}

function Resources({ detail, workspace, setDetail, onCursor, onDraftChange }: { detail: TaskDetailValue; workspace: string; setDetail(value: TaskDetailValue): void; onCursor(cursor?: string): void; onDraftChange(dirty: boolean): void }) {
  const [kind, setKind] = useState('plan'); const [url, setURL] = useState(''); const [title, setTitle] = useState(''); const [caption, setCaption] = useState(''); const [fileSelected, setFileSelected] = useState(false); const file = useRef<HTMLInputElement>(null); const [error, setError] = useState('');
  useEffect(() => { onDraftChange(Boolean(url.trim() || title.trim() || caption.trim() || fileSelected)); return () => onDraftChange(false); }, [url, title, caption, fileSelected, onDraftChange]);
  return <section><h2>Resources · {detail.references.length + detail.attachments.length}</h2><div className="resource-list">
    {[...detail.references].reverse().map((reference) => <div className="resource-row" key={reference.id}><ResolvedReference reference={reference} /><button aria-label="Remove reference" onClick={async () => { if (!confirm('Remove this link resource?')) return; try { const updated = await removeReference(workspace, detail.id, reference.id); setDetail(updated); onCursor(updated.cursor); } catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); } }}>×</button></div>)}
    {detail.attachments.map((attachment) => <a className="attachment-row" key={attachment.file} href={taskPath(workspace, detail.id, `/attachments/${encodeURIComponent(attachment.file)}`)}><span>▧</span><span><b>{attachment.caption || attachment.file}</b><small>{attachment.file} · {Math.round(attachment.bytes / 1024)} KB</small></span></a>)}
  </div>{error && <p className="error-text">{error}</p>}
  <details><summary>Add link</summary><div className="inline-form"><input value={kind} onChange={(event) => setKind(event.target.value)} placeholder="kind" /><input value={url} onChange={(event) => setURL(event.target.value)} placeholder="https://…" /><input value={title} onChange={(event) => setTitle(event.target.value)} placeholder="title (optional)" /><button onClick={async () => { try { const updated = await addReference(workspace, detail.id, { kind, url, title }); setDetail(updated); setURL(''); setTitle(''); onCursor(updated.cursor); } catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); } }}>Add</button></div></details>
  <details><summary>Upload file</summary><div className="inline-form"><input ref={file} type="file" onChange={() => setFileSelected(Boolean(file.current?.files?.length))} /><input value={caption} onChange={(event) => setCaption(event.target.value)} placeholder="caption" /><button onClick={async () => { const selected = file.current?.files?.[0]; if (!selected) return; try { const updated = await uploadAttachment(workspace, detail.id, selected, caption); setDetail(updated); setCaption(''); setFileSelected(false); if (file.current) file.current.value = ''; onCursor(updated.cursor); } catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); } }}>Upload</button></div></details>
  </section>;
}

function Relationships({ detail }: { detail: TaskDetailValue }) {
  const entries = Object.entries(detail.relationships || {}); if (!entries.length) return null;
  return <section><h2>Relationships</h2><div className="relationship-list">{entries.flatMap(([kind, tasks]) => tasks.map((task) => <span key={`${kind}-${task.id}`}>{humanize(kind)} · {task.id}{task.title ? ` · ${task.title}` : ''}</span>))}</div></section>;
}

function Activity({ detail }: { detail: TaskDetailValue }) {
  const entries = useMemo(() => [...detail.activity].reverse(), [detail.activity]);
  return <section><h2>Activity</h2><div className="activity-list">{entries.map((entry, index) => {
    const reference = entry.data?.reference as any;
    return <article key={`${entry.at}-${index}`} className={`activity-entry ${entry.kind === 'comment' ? 'comment-entry' : ''}`}><div><b>{entry.actor || 'Docket'}</b> {activityTitle(entry)}<time>{formatDate(entry.at)}</time></div>{entry.body_html ? <div className="markdown" dangerouslySetInnerHTML={{ __html: entry.body_html }} /> : entry.body ? <p>{entry.body}</p> : null}{reference?.url && <ResolvedReference reference={reference} />}</article>;
  })}</div></section>;
}
function detailToBoard(detail: TaskDetailValue) {
  return { id: detail.id, title: detail.title, status: detail.status, project: detail.project?.id, labels: detail.labels, assignee: detail.assignee, wait: detail.wait, references: detail.references, active_sessions: [], created_at: detail.created_at, updated_at: detail.updated_at, resource_count: detail.references.length + detail.attachments.length };
}

function safeURL(value: string) { try { const parsed = new URL(value); return ['http:', 'https:', 'file:'].includes(parsed.protocol) ? parsed.href : ''; } catch { return ''; } }

function activityTitle(entry: { type: string; data?: Record<string, unknown> }) {
  const data = entry.data || {}; if (entry.type === 'task.moved') return `moved ${humanize(String(data.from || ''))} → ${humanize(String(data.to || ''))}`;
  if (entry.type === 'comment') return 'commented'; if (entry.type === 'task.created') return 'created the task'; if (entry.type === 'task.updated') return 'updated the task'; if (entry.type === 'task.assigned') return 'changed the assignee'; if (entry.type === 'task.labeled') return 'changed labels'; if (entry.type === 'task.waiting') return 'set a wait'; if (entry.type === 'task.resumed') return 'resolved the wait'; if (entry.type === 'task.reference_added') return 'added a resource'; if (entry.type === 'task.reference_removed') return 'removed a resource'; if (entry.type === 'task.file_attached') return 'attached a file'; return humanize(entry.type.replace(/^task\./, '')).toLocaleLowerCase();
}
