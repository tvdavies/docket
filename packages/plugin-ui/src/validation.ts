import type { LivePayload, WidgetIdentity, WidgetLivePayload, WidgetPresentation, WidgetReference, WidgetRecordV1 } from './contracts';
export const WIDGET_BUDGETS = Object.freeze({ previewBytes: 16384, recordBytes: 8192, detailBytes: 65536, cacheEntries: 2048, cacheBytes: 32 * 1024 * 1024, resolverEntries: 512, timeoutMS: 5000, renderMS: 1000, activityMounts: 20 });
export const safeInteger = (value: unknown): value is number => Number.isSafeInteger(value) && Number(value) > 0;
export function jsonBytes(value: unknown): number { try { return new TextEncoder().encode(JSON.stringify(value)).length; } catch { return Infinity; } }
export function validWidgetPayload(value: unknown): value is WidgetLivePayload {
  const v = value as WidgetLivePayload;
  return !!v && v.widget_version === 1 && safeInteger(v.revision) && !!v.data && safeInteger(v.data.version) && Object.hasOwn(v.data, 'value') && (!v.last_activity_at || Number.isFinite(Date.parse(v.last_activity_at))) && jsonBytes(v) <= WIDGET_BUDGETS.previewBytes;
}
export function validWidgetRecord(value: unknown, workspace: string): value is WidgetRecordV1 {
  const r = value as WidgetRecordV1;
  const bounded = (v: unknown, max: number, required = false): v is string => typeof v === 'string' && [...v].length <= max && !v.includes('\0') && (!required || !!v.trim());
  const timestamp = (v: unknown) => typeof v === 'string' && /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(v) && Number.isFinite(Date.parse(v));
  if (!r || r.version !== 1 || typeof r.widget_type !== 'string' || r.widget_type.length > 100 || !/^[a-z0-9][a-z0-9_-]*\/[a-zA-Z0-9][a-zA-Z0-9_/-]*$/.test(r.widget_type) || ![r.task_id,r.instance_id].every(id => typeof id === 'string' && /^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,199}$/.test(id)) || !safeInteger(r.revision) || !timestamp(r.created_at) || !['created','finalised'].includes(r.phase) || jsonBytes(r) > WIDGET_BUDGETS.recordBytes) return false;
  const f = r.fallback;
  if (!f || !bounded(f.label,120,true) || !bounded(f.status_label,120,true) || (f.summary !== undefined && !bounded(f.summary,2000)) || !['attention','error','active','history'].includes(f.priority) || (f.started_at !== undefined && !timestamp(f.started_at)) || (f.ended_at !== undefined && !timestamp(f.ended_at))) return false;
  return f.references === undefined || Array.isArray(f.references) && f.references.length <= 8 && f.references.every(ref => !!ref && bounded(ref.kind,120,true) && bounded(ref.title,120,true) && bounded(ref.url,2048,true) && !!safeHref(ref,{workspace,taskId:r.task_id,instanceId:r.instance_id,widgetType:r.widget_type}));
}
export function widgetLive(identity: WidgetIdentity, payload: WidgetLivePayload, ttl_ms = 30000): LivePayload {
  if (!validWidgetPayload(payload)) throw new Error('invalid_payload');
  return { kind: identity.widgetType, task: identity.taskId, session: identity.instanceId, payload, ttl_ms };
}
export function serviceBase(name: string, path?: string): string | undefined {
  if (!path) return undefined;
  if (!/^[-a-z0-9_]+$/.test(name) || /[%\\?#\s]/.test(path) || path.split('/').some(p => p === '.' || p === '..')) return undefined;
  return path === `/plugins/${name}` || path.startsWith(`/plugins/${name}/`) ? path : undefined;
}
/** External HTTPS is allowed for resources, never for task/session destinations. */
export function safeHref(ref: WidgetReference, identity: WidgetIdentity): string | null {
  const value = ref.url;
  if (typeof value !== 'string' || value.length > 2048 || /[\\\s\x00-\x1f]/.test(value) || /%2[ef]|%5c|%25/i.test(value)) return null;
  try {
    const parsed = new URL(value, 'https://docket.invalid');
    if (parsed.username || parsed.password || value.startsWith('//') || value.split('/').some(p => p === '.' || p === '..')) return null;
    const plugin = `/plugins/${identity.widgetType.split('/')[0]}/`;
    const workspace = `/workspaces/${encodeURIComponent(identity.workspace)}/`;
    if (value.startsWith('/')) return parsed.pathname.startsWith(plugin) || parsed.pathname.startsWith(workspace) ? value : null;
    if (['session', 'task'].includes(ref.kind)) return null;
    return parsed.protocol === 'https:' ? parsed.href : null;
  } catch { return null; }
}
const text = (value: unknown, cap: number) => typeof value === 'string' ? [...value].slice(0, cap).join('') : '';
export function boundedPresentation(value: WidgetPresentation, expanded = false): WidgetPresentation {
  const tones = ['neutral', 'positive', 'warning', 'danger', 'info'];
  const tone = (t: unknown) => tones.includes(String(t)) ? t as WidgetPresentation['status']['tone'] : 'neutral';
  let remaining = expanded ? 1600 : 600;
  const seen = new Set<string>();
  const rows = (Array.isArray(value.rows) ? value.rows : []).filter(row => row && typeof row.key === 'string' && Number.isFinite(row.order)).sort((a,b) => a.order - b.order).slice(-(expanded ? 12 : 4)).filter(row => { if (seen.has(row.key)) return false; seen.add(row.key); return true; }).map(row => {
    const label = text(row.label, Math.min(120, remaining)); remaining -= [...label].length;
    const body = text(row.text, remaining); remaining -= [...body].length;
    return { ...row, key: text(row.key, 200), label, text: body };
  });
  return { label: text(value.label, 120), status: { text: text(value.status?.text, 120), tone: tone(value.status?.tone) }, terminal: value.terminal === true,
    priority: ['attention','error','active','history'].includes(value.priority) ? value.priority : 'history', action: text(value.action,120), summary: text(value.summary,2000),
    notice: value.notice ? { text: text(value.notice.text, 600), tone: tone(value.notice.tone) } : undefined, rows,
    references: (Array.isArray(value.references) ? value.references : []).slice(0,8).filter(r => r && typeof r.url === 'string').map(r => ({ kind: text(r.kind,120), title: text(r.title,120), url: text(r.url,2048) })), startedAt: value.startedAt, endedAt: value.endedAt };
}
