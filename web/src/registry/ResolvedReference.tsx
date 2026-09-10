import { useEffect, useState } from 'react';
import { safeHref, type ResolvedReference as ResolvedValue, type TaskReference } from '@docket/plugin-ui';
import { fallbackReference, resolveReference, useRegistryVersion } from './registry';
import { usePluginScope } from './scope';
export function ResolvedReference({reference,compact=false,taskId=''}:{reference:TaskReference;compact?:boolean;taskId?:string}) {
  const env=usePluginScope(),version=useRegistryVersion();
  const key=JSON.stringify([env.workspace,env.config.resolver_generation,version,reference,taskId]);
  const [result,setResult]=useState<{key:string;value:ResolvedValue}>();
  useEffect(()=>{let active=true;void resolveReference(reference,env.references,taskId).then(value=>{if(active)setResult({key,value});});return()=>{active=false;};},[key,env.references]);
  const resolved=result?.key===key?result.value:fallbackReference(reference);
  const contents=<><span aria-hidden="true">{resolved.icon==='plan'?'▤':'↗'}</span><span>{resolved.label}</span>{!compact && resolved.meta && <small>{Object.values(resolved.meta).join(' · ')}</small>}</>;
  const href=safeHref({kind:reference.kind,url:resolved.href || reference.url,title:resolved.label},{workspace:env.workspace,taskId,widgetType:reference.resolver_id || 'core/reference',instanceId:reference.id});
  return href?<a className={`reference-chip ${compact?'compact':''}`} href={href} target="_blank" rel="noreferrer">{contents}</a>:<span className={`reference-chip ${compact?'compact':''}`}>{contents}</span>;
}
