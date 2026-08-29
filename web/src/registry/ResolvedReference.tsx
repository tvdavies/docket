import { useEffect, useState } from 'react';
import type { TaskReference } from '../types';
import type { ResolvedReference as ResolvedValue } from './contracts';
import { resolveReference, useRegistryVersion } from './registry';

const cache = new Map<string, ResolvedValue>();

export function ResolvedReference({ reference, compact = false }: { reference: TaskReference; compact?: boolean }) {
  const registryVersion = useRegistryVersion();
  const key = `${registryVersion}\0${reference.kind}\0${reference.url}`;
  const [resolved, setResolved] = useState<ResolvedValue>(() => cache.get(key) || { label: reference.title || reference.url, icon: 'link', meta: { kind: reference.kind } });
  useEffect(() => {
    let active = true;
    void resolveReference(reference).then((value) => { cache.set(key, value); if (active) setResolved(value); });
    return () => { active = false; };
  }, [key, reference]);
  const contents = <><span aria-hidden="true">{resolved.icon === 'plan' ? '▤' : '↗'}</span><span>{resolved.label}</span>{!compact && resolved.meta && <small>{Object.values(resolved.meta).join(' · ')}</small>}</>;
  const href = safeURL(reference.url);
  return href ? <a className={`reference-chip ${compact ? 'compact' : ''}`} href={href} target="_blank" rel="noreferrer" title={Object.values(resolved.meta || {}).join(' · ') || reference.url}>{contents}</a>
    : <span className={`reference-chip ${compact ? 'compact' : ''}`} title="Unsafe reference URL">{contents}</span>;
}

function safeURL(value: string) {
  try { const parsed = new URL(value); return ['http:', 'https:', 'file:'].includes(parsed.protocol) ? parsed.href : ''; } catch { return ''; }
}
