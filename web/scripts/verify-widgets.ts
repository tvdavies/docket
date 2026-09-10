import { createServer } from 'vite';
import { chromium } from 'playwright';
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import { cpus, platform, arch } from 'node:os';
import { resolve } from 'node:path';
import type { ServerResponse } from 'node:http';
// Only synthetic, process-local state. No Docket home, registry, CLI or services.
const root=resolve(import.meta.dirname,'..'),evidence=resolve(root,'../docs/widget-evidence');await mkdir(evidence,{recursive:true});
let revision=2,enabled=true,finalised=false,version=1,attention=false,ttl=30000;
const clients=new Map<ServerResponse,string>();let taskReads=0,streamOpens=0;
const progress={name:'fixture-progress',version:'1.0.0',api_version:2,cards:[{type:'fixture-progress/job',title:'Progress',locations:['board','activity']}],reference_resolvers:[],service_base:'/plugins/fixture-progress'};
const chart={name:'fixture-chart',version:'1.0.0',api_version:2,cards:[{type:'fixture-chart/samples',title:'Samples',locations:['board','activity']}],reference_resolvers:[],service_base:'/plugins/fixture-chart'};
const config=(workspace:string)=>({statuses:['todo'],terminal:[],labels:[],resolver_generation:enabled?'g1':'g2',plugins:workspace==='plain'||!enabled?[]:workspace==='other'?[chart]:[progress,chart]});
const record=(task:string,type='fixture-progress/job',instance='run-1')=>({version:1,widget_type:type,instance_id:instance,task_id:task,created_at:'2026-09-10T10:00:01Z',revision:finalised?2000:1,phase:finalised?'finalised':'created',fallback:{label:type.startsWith('fixture-chart')?'Sample chart':'Fixture progress',status_label:finalised?'Finished':'Processing',priority:finalised?'history':'active',summary:finalised?'Saved terminal outcome':'Saved starting summary',references:[{kind:'plan',url:'https://example.test/plan',title:'Published plan'}]}});
const records=(workspace:string,id:string)=>workspace==='plain'?[]:workspace==='other'?[record(id,'fixture-chart/samples')]:[record(id),...(id==='TASK-1'?[record(id,'fixture-progress/job','run-2'),record(id,'fixture-chart/samples')]:[])];
const task=(workspace:string,id:string)=>({id,title:`Synthetic widget task ${id}`,status:'todo',labels:[],references:[],active_sessions:[{session:'audit-only'}],created_at:'2026-09-10T10:00:00Z',updated_at:'2026-09-10T10:00:00Z',resource_count:0,widget_summaries:records(workspace,id),widget_revision:finalised?'final':'created'});
const tasks=(workspace:string)=>Array.from({length:100},(_,i)=>task(workspace,`TASK-${i+1}`));
const detail=(workspace:string,id:string)=>({...task(workspace,id),description:'Synthetic fixture, no agents or live services.',description_html:'<p>Synthetic fixture, no agents or live services.</p>',comments:[],attachments:[],widgets:records(workspace,id),activity:[{at:'2026-09-10T10:00:00Z',kind:'event',type:'task.created'},...records(workspace,id).map(r=>({at:r.created_at,kind:'widget',type:r.widget_type,data:{record:r}}))]});
const send=(response:ServerResponse,event:string,data:unknown)=>response.write(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`);
const publish=(response:ServerResponse,workspace:string)=>{if(finalised)return;for(const t of tasks(workspace))for(const r of records(workspace,t.id))send(response,'live',{kind:r.widget_type,task:t.id,session:r.instance_id,ttl_ms:ttl,payload:{widget_version:1,revision,data:{version,value:{label:r.fallback.label,text:`Live fixture content at revision ${revision}`,count:revision,attention}}}});};
const advance=()=>{revision++;for(const [response,workspace]of clients)publish(response,workspace);};
const vite=await createServer({root,configFile:resolve(root,'vite.config.ts'),appType:'custom',server:{host:'127.0.0.1',port:0,fs:{allow:[resolve(root,'..')]}},plugins:[{name:'isolated-widget-api',configureServer(server){server.middlewares.use(async(req,res,next)=>{
  const path=new URL(req.url || '/', 'http://fixture').pathname;
  const json=(value:unknown)=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify(value));};
  if(path==='/api/workspaces')return json(['demo','other','plain'].map(name=>({name,state:'watching'})));
  const match=path.match(/^\/api\/workspaces\/([^/]+)\/(stream|board|tasks\/([^/]+))$/);
  if(match){const workspace=match[1];if(match[2]==='stream'){res.setHeader('Content-Type','text/event-stream');res.setHeader('Cache-Control','no-cache');res.flushHeaders();clients.set(res,workspace);streamOpens++;send(res,'init',{workspace,config:config(workspace),tasks:tasks(workspace),cursor:`c${revision}`});publish(res,workspace);req.on('close',()=>clients.delete(res));return;}if(match[2]==='board')return json({...config(workspace),workspace,tasks:tasks(workspace)});taskReads++;return json(detail(workspace,match[3]));}
  if(path.startsWith('/api/')){res.statusCode=404;res.end();return;}
  if(path==='/'||path.startsWith('/workspaces/')){res.setHeader('Content-Type','text/html');res.end(await server.transformIndexHtml(path,await readFile(resolve(root,'tests/fixtures/widgets/index.html'),'utf8')));return;}
  next();
});}}]});
await vite.listen();const address=vite.httpServer!.address() as {port:number};const origin=`http://127.0.0.1:${address.port}`;
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_PATH,headless:true,args:['--no-sandbox']});
const page=await browser.newPage({viewport:{width:1440,height:1000}});const errors:string[]=[],violations:string[]=[],passed:string[]=[];
page.on('pageerror',e=>errors.push(e.message));await page.route('**/*',route=>{const url=new URL(route.request().url());if(url.origin!==origin){violations.push(url.href);return route.abort();}return route.continue();});
const assert=(condition:unknown,label:string)=>{if(!condition)throw Error(label);passed.push(label);console.log('PASS',label);};
const stats=()=>page.evaluate(()=>(window as any).widgetFixture.counters);
const widget=()=>page.locator('[data-widget="fixture-progress/job"][data-instance="run-1"]');
try {
  await page.goto(origin+'/workspaces/demo/tasks/TASK-1');await widget().getByText('Live fixture content at revision 2').waitFor();
  const initialReads=taskReads,url=page.url();advance();await widget().getByText('Live fixture content at revision 3').waitFor();assert(page.url()===url&&taskReads===initialReads,'unexpanded activity streams without navigation or task refresh');assert((await stats()).detail===0,'zero detail connections before expansion');assert(await page.locator('svg[aria-label="Fixture sample chart"]').count()===1,'custom Web Component renders a real SVG with accessible value table');
  await page.screenshot({path:resolve(evidence,'activity-light-desktop.png'),fullPage:true});
  const body=widget().locator('[data-row="text"]');await body.evaluate(el=>{el.setAttribute('tabindex','0');(el as HTMLElement).focus();});const held=await body.textContent();const scroll=await page.evaluate(()=>scrollY);attention=true;advance();await widget().getByText('Fixture input notice').waitFor();await page.waitForTimeout(1100);
  assert(await body.textContent()===held,'focused shadow body is held while attention notice updates');assert(await page.evaluate(()=>scrollY)===scroll,'streaming does not move the page reading position');
  await widget().getByRole('button',{name:'New activity · show'}).click();assert((await body.textContent())?.includes('revision 4'),'explicit New activity applies the held snapshot');
  await widget().getByRole('button',{name:'Expand activity'}).click();assert((await stats()).detail===1,'expansion acquires one view-owned detail lease');
  const chartWidget=page.locator('[data-widget="fixture-chart/samples"]');await chartWidget.getByRole('button',{name:'Expand activity'}).click();assert((await stats()).detail===1&&(await stats()).maximumDetail===1,'selecting another widget revokes the old transport');assert(await widget().getByRole('button',{name:'Expand activity'}).isVisible(),'reselected widget collapses');
  await chartWidget.locator('summary').click();const beforeTheme=await stats();await page.emulateMedia({colorScheme:'dark',reducedMotion:'reduce'});await page.waitForFunction(()=>document.querySelector('.docket-widget')?.getAttribute('data-theme')==='dark');assert((await stats()).mounts===beforeTheme.mounts,'theme and reduced motion update without remount');assert(await chartWidget.locator('details').getAttribute('open')!==null,'custom disclosure state survives theme change');
  await page.setViewportSize({width:390,height:1000});await page.screenshot({path:resolve(evidence,'activity-dark-phone.png'),fullPage:true});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'390px task page has no horizontal overflow');
  await page.setViewportSize({width:320,height:1000});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'320px task page has no horizontal overflow');
  await page.evaluate(()=>{document.documentElement.style.zoom='2';});assert(await page.locator('.widget-controls button').evaluateAll(nodes=>nodes.filter(n=>!(n as HTMLElement).hidden).every(n=>n.getBoundingClientRect().height>=44)),'phone controls retain 44px targets at 200% zoom');await page.evaluate(()=>{document.documentElement.style.zoom='';});
  await chartWidget.locator('summary').focus();await page.keyboard.press('Enter');assert(await chartWidget.locator('details').getAttribute('open')===null,'keyboard disclosure works inside Shadow DOM');
  // Expiry releases transport but preserves the expanded reader. A heartbeat recovers freshness, not selection.
  ttl=40;advance();await page.waitForTimeout(250);assert((await stats()).detail===0,'TTL expiry releases selected transport');assert(await chartWidget.getByRole('button',{name:'Collapse activity'}).isVisible(),'expiry retains the expanded reading view');ttl=30000;advance();await page.waitForTimeout(1100);assert((await stats()).detail===0,'fresh publication does not silently reacquire detail');
  version=99;advance();await page.waitForTimeout(200);assert(await page.locator('svg[aria-label="Fixture sample chart"]').count()===0,'unknown display version retires custom body');version=1;advance();await chartWidget.locator('svg').waitFor();
  finalised=true;for(const [response,workspace]of clients)send(response,'patch',{event:{type:'task.widget_finalised',seq:1,time:'2026-09-10T10:01:00Z',task:'TASK-1'},task:task(workspace,'TASK-1')});
  await widget().getByText('Saved terminal outcome').waitFor();assert(await page.locator('[data-widget="fixture-progress/job"][data-instance="run-1"]').count()===1,'finalisation updates the same activity identity');
  enabled=false;for(const [response,workspace]of clients)send(response,'config',config(workspace));await page.waitForTimeout(200);assert((await stats()).active===0,'disable tears down every authored body');assert(await widget().getByText('Saved terminal outcome').isVisible(),'saved outcome remains after plugin disable');
  await page.reload();await widget().getByText('Saved terminal outcome').waitFor();assert((await stats()).mounts===0,'fresh browser recovers terminal history with no module execution');
  await page.setViewportSize({width:1440,height:1000});await page.goto(origin+'/workspaces/plain');await page.locator('.task-card').first().waitFor();assert(await page.locator('.docket-widget,.plugin-card-host').count()===0,'plugin-less board stays plain');
  enabled=true;finalised=false;attention=false;revision++;await page.goto(origin+'/workspaces/other/tasks/TASK-1');await page.locator('[data-widget="fixture-chart/samples"] svg').waitFor();assert(await page.locator('[data-widget="fixture-progress/job"]').count()===0,'same task and instance in another workspace only mounts its enabled plugin');
  await page.setViewportSize({width:1440,height:1000});await page.goto(origin+'/workspaces/demo');await page.locator('.docket-widget').first().waitFor();await page.waitForTimeout(250);
  assert(await page.locator('.task-card').count()<=20,'100-task board retains a bounded visible virtualized window');assert((await stats()).detail===0,'board cards open no detailed streams');
  for(let i=0;i<5;i++){advance();await page.waitForTimeout(1050);}
  await page.screenshot({path:resolve(evidence,'board-desktop.png'),fullPage:true});
  const performanceResult=await page.evaluate(()=>{const f=(window as any).widgetFixture;const samples=[...f.samples].sort((a:number,b:number)=>a-b);return {samples:samples.length,p95:samples[Math.floor(samples.length*.95)],max:Math.max(...samples),nodes:document.querySelectorAll('*').length,counters:f.counters};});
  assert(performanceResult.p95<=16 && performanceResult.max<=50,'measured host work p95 <=16ms, maximum <=50ms at 1Hz');
  assert(clients.size===1,'one shared preview connection for the active workspace');assert(violations.length===0,'all browser traffic remains on the isolated loopback server');assert(errors.length===0,'no browser runtime errors: '+errors.join('; '));
  await writeFile(resolve(evidence,'browser-results.json'),JSON.stringify({passed,machine:{platform:platform(),arch:arch(),cpu:cpus()[0]?.model,browser:await browser.version()},performance:performanceResult,connections:{active:clients.size,totalOpened:streamOpens},taskReads,manualScreenReader:'NOT RUN: explicit outstanding human gate'},null,2)+'\n');
} finally {await browser.close();for(const response of clients.keys())response.end();await vite.close();}
