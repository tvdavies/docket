import type { WidgetContext, WidgetPresentation, WidgetReference, WidgetRow, WidgetTone } from './contracts';
import { boundedPresentation, safeHref } from './validation';
export const kitCSS = `:host{display:block;color:var(--docket-widget-text);font:var(--docket-widget-text-size)/var(--docket-widget-line-height) var(--docket-widget-font);overflow-wrap:anywhere}*{box-sizing:border-box}p,pre{margin:0}ol{padding:0;list-style:none;margin:0}li{padding:var(--docket-widget-space-1) 0}pre{white-space:pre-wrap;max-width:100%;font-family:var(--docket-widget-mono);background:var(--docket-widget-sunken)}button,a{color:var(--docket-widget-accent)}button{font:inherit;cursor:pointer;background:var(--docket-widget-raised);border:1px solid var(--docket-widget-border);border-radius:var(--docket-widget-radius);padding:4px 8px}:focus-visible{outline:var(--docket-widget-focus-width) solid var(--docket-widget-focus);outline-offset:var(--docket-widget-focus-offset)}.muted{color:var(--docket-widget-muted)}[data-tone=positive]{color:var(--docket-widget-positive-fg)}[data-tone=warning]{color:var(--docket-widget-warning-fg)}[data-tone=danger]{color:var(--docket-widget-danger-fg)}[data-tone=info]{color:var(--docket-widget-info-fg)}@media(max-width:700px){button,a{min-height:44px;display:inline-flex;align-items:center}}@media(prefers-reduced-motion:reduce){*{animation:none!important;scroll-behavior:auto!important}}`;
export function installKit(root: ShadowRoot) { const style=document.createElement('style'); style.textContent=kitCSS; root.append(style); }
export function safeText(value:string,limit=600) { const p=document.createElement('p'); p.textContent=[...value].slice(0,limit).join(''); return p; }
export function code(value:string) { const p=document.createElement('pre'); p.textContent=[...value].slice(0,2000).join(''); return p; }
export function status(label:string,tone:WidgetTone='neutral') { const node=document.createElement('span'); node.textContent=label.slice(0,120); node.dataset.tone=tone; return node; }
export function metadata(label:string) { const node=status(label); node.className='muted'; return node; }
export function notice(label:string,tone:WidgetTone='info') { const node=safeText(label); node.dataset.tone=tone; node.setAttribute('role',tone==='danger'?'alert':'status'); return node; }
export function reference(ref:WidgetReference,ctx:WidgetContext):HTMLElement { const href=safeHref(ref,ctx.identity); if(!href)return safeText(ref.title,120); const node=document.createElement('a'); node.href=href; node.textContent=ref.title.slice(0,120); node.rel='noreferrer'; return node; }
export function summary(value:string) { return safeText(value,2000); }
export function disclosure(label:string,id:string,toggle:(expanded:boolean)=>void) { const button=document.createElement('button'); button.type='button'; button.textContent=label; button.setAttribute('aria-controls',id); button.setAttribute('aria-expanded','false'); button.addEventListener('click',()=>{const next=button.getAttribute('aria-expanded')!=='true'; button.setAttribute('aria-expanded',String(next));toggle(next);}); return button; }
/** Small safe subset: paragraphs, emphasis, inline/fenced code and HTTPS links. No HTML/images. */
export function formattedText(value:string,ctx:WidgetContext) {
  const root=document.createElement('div');
  for(const block of value.slice(0,2000).split(/\n\n+/)) {
    if(block.startsWith('```')) { root.append(code(block.replace(/^```[^\n]*\n?/, '').replace(/```$/, '')));continue; }
    const p=document.createElement('p'); const pattern=/(`[^`]+`|\*[^*]+\*|\[([^\]]+)\]\(([^)]+)\))/g; let cursor=0;
    for(const match of block.matchAll(pattern)) { p.append(document.createTextNode(block.slice(cursor,match.index))); const token=match[0];
      if(token.startsWith('['))p.append(reference({kind:'resource',title:match[2],url:match[3]},ctx));
      else {const node=document.createElement(token[0]==='`'?'code':'em');node.textContent=token.slice(1,-1);p.append(node);} cursor=match.index!+token.length;
    }
    p.append(document.createTextNode(block.slice(cursor)));root.append(p);
  }return root;
}
/** Stable keyed nodes; caller must hold updates when a reader selects/focuses them. */
export class OrderedActivity {
  readonly element=document.createElement('ol');
  private nodes=new Map<string,{element:HTMLLIElement;signature:string}>();
  update(rows:WidgetRow[],ctx:WidgetContext,expanded=false) {
    const bounded=boundedPresentation({label:'',status:{text:'',tone:'neutral'},priority:'history',terminal:false,rows},expanded).rows || [];
    const keep=new Set(bounded.map(r=>r.key));
    for(const [key,node] of this.nodes)if(!keep.has(key)){node.element.remove();this.nodes.delete(key);}
    let previous:Element|null=null;
    for(const row of bounded){let node=this.nodes.get(row.key);if(!node){node={element:document.createElement('li'),signature:''};node.element.dataset.row=row.key;this.nodes.set(row.key,node);}
      const signature=JSON.stringify(row);if(node.signature!==signature){const label=document.createElement('b');label.textContent=row.label;const content=row.role==='reference'&&row.reference?reference(row.reference,ctx):row.role==='code'?code(row.text||''):safeText(row.text||'',expanded?1600:600);node.element.replaceChildren(label,content);node.signature=signature;}
      const expected:Element|null=previous?previous.nextElementSibling:this.element.firstElementChild;if(expected!==node.element)this.element.insertBefore(node.element,expected);previous=node.element;
    }
  }
}
export function standardWidgetBody(body:HTMLElement,context:WidgetContext) {
  const root=body.attachShadow({mode:'open'});installKit(root);const rows=new OrderedActivity();const outcome=summary('');root.append(rows.element,outcome);
  return {update(_snapshot:unknown,ctx:WidgetContext,view:{displayed?:WidgetPresentation;expanded:boolean}){const p=view.displayed;rows.update(ctx.location==='board'?[]:p?.rows||[],ctx,view.expanded);outcome.textContent=ctx.location==='board'?(p?.action || p?.summary || '').slice(0,120):p?.terminal?p.summary||'':'';},destroy(){root.replaceChildren();}};
}
