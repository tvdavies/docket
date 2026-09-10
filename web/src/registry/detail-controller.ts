import { jsonBytes, safeInteger, WIDGET_BUDGETS, type DetailConnection, type DetailFrame, type DetailLease, type DetailProvider, type DetailRevocation, type WidgetIdentity } from '@docket/plugin-ui';
type Selection = { abort: AbortController; close?: DetailConnection; listener(frame: DetailFrame): void; revoked(reason: DetailRevocation): void; timer?: ReturnType<typeof setTimeout>; resetNeeded: boolean; resetSent: boolean; key: string; revision: number; through: number };
/** Owned by a task view, never a board card. Received continuity is independent of display. */
export class DetailController {
  private selected?: Selection;
  private marks=new Map<string,{revision:number;through:number}>();
  get activeCount() { return this.selected ? 1 : 0; }
  request(identity:WidgetIdentity,serviceBase:string|undefined,provider:DetailProvider,versions:readonly number[],signal:AbortSignal,listener:(f:DetailFrame)=>void,revoked:(r:DetailRevocation)=>void):DetailLease|null {
    if (signal.aborted) return null;
    this.revoke('reselected');
    const key=JSON.stringify(identity), mark=this.marks.get(key);
    const selected:Selection={abort:new AbortController(),listener,revoked,resetNeeded:true,resetSent:false,key,revision:mark?.revision || 0,through:mark?.through || 0};
    this.selected=selected;
    const live=()=>this.selected===selected && !selected.abort.signal.aborted;
    const retire=()=>{ if (live()) this.revoke('retired'); };
    signal.addEventListener('abort',retire,{once:true});
    selected.abort.signal.addEventListener('abort',()=>signal.removeEventListener('abort',retire),{once:true});
    const requestReset=()=>{
      if (!live()) return;
      selected.resetNeeded=true;
      if (!selected.timer) selected.timer=setTimeout(()=>{ if (live()) this.revoke('gap_timeout'); },WIDGET_BUDGETS.timeoutMS);
      if (selected.close && !selected.resetSent) { selected.resetSent=true; try { selected.close.requestReset(); } catch { this.revoke('error'); } }
    };
    requestReset();
    try {
      const connection=provider.open({identity,serviceBase,signal:selected.abort.signal},{
        status:state=>{ if (!live()) return; if (state!=='ready') this.revoke(state==='unavailable'?'unavailable':state==='not_found'?'not_found':'error'); },
        frame:f=>{
          if (!live() || !f || f.version!==1 || jsonBytes(f)>WIDGET_BUDGETS.detailBytes || !sameIdentity(identity,f.identity) || !versions.includes(f.dataVersion) || !safeInteger(f.revision) || !Number.isSafeInteger(f.baseSeq) || !Number.isSafeInteger(f.throughSeq) || f.baseSeq<0 || f.throughSeq<f.baseSeq || typeof f.reset!=='boolean') return;
          if (f.revision<selected.revision || f.throughSeq<selected.through || (!selected.resetNeeded && f.revision===selected.revision)) return;
          if (!f.reset && (selected.resetNeeded || f.baseSeq!==selected.through)) { requestReset(); return; }
          if (f.reset) { selected.resetNeeded=false; selected.resetSent=false; clearTimeout(selected.timer); selected.timer=undefined; }
          selected.revision=f.revision; selected.through=f.throughSeq;
          if (this.marks.size>=WIDGET_BUDGETS.cacheEntries && !this.marks.has(key)) this.marks.delete(this.marks.keys().next().value!);
          this.marks.set(key,{revision:f.revision,through:f.throughSeq});
          try { listener(f); } catch { this.revoke('error'); }
        },
      });
      if (!live()) { try { connection.close(); } catch { /* cleanup still complete */ } return null; }
      selected.close=connection; if (selected.resetNeeded) requestReset();
    } catch { if (live()) this.revoke('error'); return null; }
    return {release:()=>{if(live())this.revoke('released');},requestReset};
  }
  revoke(reason:DetailRevocation) {
    const selected=this.selected; if (!selected) return;
    this.selected=undefined; selected.abort.abort(); clearTimeout(selected.timer);
    try { selected.close?.close(); } catch { /* never prevent host cleanup */ }
    try { selected.revoked(reason); } catch { /* isolate authored callbacks */ }
  }
  destroy() { this.revoke('retired'); this.marks.clear(); }
}
function sameIdentity(a:WidgetIdentity,b:WidgetIdentity) { return !!b && a.workspace===b.workspace && a.taskId===b.taskId && a.widgetType===b.widgetType && a.instanceId===b.instanceId; }
