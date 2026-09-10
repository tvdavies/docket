import { useEffect, useRef } from 'react';
import type { BoardTask, PluginMetadata, WidgetLocation, WidgetRecordV1 } from '@docket/plugin-ui';
import { cardModules, widgetModule, useRegistryVersion } from './registry';
import { WidgetHost } from './widget-host';
import { usePluginScope } from './scope';
import type { DetailController } from './detail-controller';

/** Legacy contributions remain explicitly labelled and retain the exact v1 ABI. */
export function PluginCardHost({workspace,task,refresh=()=>undefined,config,location='board'}:{workspace:string;task:BoardTask;refresh?():void;config?:PluginMetadata[];location?:WidgetLocation}) {
  const env=usePluginScope(), version=useRegistryVersion(), root=useRef<HTMLDivElement>(null);
  const instances=useRef<Array<{update(task:BoardTask):void;destroy():void}>>([]);
  const declarations=config || env.config.plugins || [];
  const modules=cardModules(task,declarations,location);
  const key=JSON.stringify([declarations,modules.map(m=>m.module.type)]);
  useEffect(()=>{
    const host=root.current;if(!host)return;host.replaceChildren();instances.current=[];
    for(const {module,base}of modules){const slot=document.createElement('div');slot.dataset.pluginCard=module.type;host.append(slot);let instance:ReturnType<typeof module.mount>|undefined;let failed=false;
      const retire=()=>{if(failed)return;failed=true;const focused=slot.contains(document.activeElement);try{instance?.destroy();}catch{}finally{slot.replaceChildren();slot.textContent='Plugin card unavailable';if(focused){slot.tabIndex=-1;slot.focus({preventScroll:true});}}};
      try{instance=module.mount(slot,{workspace,task,pluginBase:base,refresh});instances.current.push({update(value){if(!failed)try{instance!.update(value);}catch{retire();}},destroy:retire});}catch{retire();}
    }
    return()=>{for(const i of instances.current)i.destroy();instances.current=[];host.replaceChildren();};
  },[workspace,task.id,version,key,location]);
  useEffect(()=>{for(const i of instances.current)i.update(task);},[task]);
  if(!modules.length)return null;
  return <div className="plugin-card-host" data-legacy-widget="true">{location==='activity' && <small>Legacy plugin contribution</small>}<div ref={root}/></div>;
}
export function WidgetContribution({workspace,task,record,location,detail,refresh,config}:{workspace:string;task:BoardTask;record:WidgetRecordV1;location:WidgetLocation;detail?:DetailController;refresh?():void;config?:PluginMetadata[]}) {
  const env=usePluginScope(),version=useRegistryVersion(),root=useRef<HTMLDivElement>(null),host=useRef<WidgetHost|undefined>(undefined);
  const declarations=config || env.config.plugins || [];
  const match=widgetModule(declarations,record.widget_type,location);
  const key=JSON.stringify([match?.metadata,match?.declaration,version]);
  const current=env.router?.get(record.widget_type,task.id,record.instance_id);
  const availability=!match?'plugin_disabled':!match.module?'module_missing':current && !match.module.dataVersions.includes(current.data.version)?'unsupported':match.metadata.service_base && !match.base?'missing_service':'available';
  const snapshot={task,data:record.phase==='finalised'?undefined:current?.data,freshness:current?{...current.freshness,connection:env.connection}:{connection:env.connection,stale:true,rehydrating:record.phase!=='finalised'},availability,fallback:record} as const;
  const input={apiVersion:2 as const,identity:{workspace,taskId:task.id,widgetType:record.widget_type,instanceId:record.instance_id},location,serviceBase:match?.base,preferences:env.preferences,refresh:refresh || (() => env.refreshTask?.(task.id))};
  useEffect(()=>{if(!root.current)return;host.current=new WidgetHost(root.current,input,match?.module,detail);return()=>{host.current?.destroy();host.current=undefined;};},[workspace,task.id,record.widget_type,record.instance_id,location,key,detail]);
  useEffect(()=>{host.current?.update(snapshot,input,env.router?.generation || 0);});
  return <div ref={root}/>;
}
export function BoardWidgets({workspace,task,config}:{workspace:string;task:BoardTask;config:PluginMetadata[]}) {
  const priority={attention:0,error:1,active:2,history:3};
  const types=[...new Set([...config.flatMap(p=>p.cards.filter(c=>(c.locations || ['board','activity']).includes('board')).map(c=>c.type)),...(task.widget_summaries || []).map(r=>r.widget_type).sort()])];
  return <>{types.map(type=>{const records=(task.widget_summaries || []).filter(r=>r.widget_type===type).sort((a,b)=>priority[a.fallback.priority]-priority[b.fallback.priority] || Date.parse(b.created_at)-Date.parse(a.created_at) || a.instance_id.localeCompare(b.instance_id));if(!records.length)return null;return <div key={type}><WidgetContribution workspace={workspace} task={task} record={records[0]} location="board" config={config}/>{records.length>1 && <a href={`/workspaces/${encodeURIComponent(workspace)}/tasks/${encodeURIComponent(task.id)}#activity`}>+{records.length-1} more {records[0].fallback.label} widgets</a>}</div>;})}</>;
}
