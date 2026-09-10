import { createRoot } from 'react-dom/client';
import { App } from '../../../src/app/App';
import { registerPluginUI } from '../../../src/registry/registry';
import { WidgetHost } from '../../../src/registry/widget-host';
import { progressPlugin, counters, faults } from './progress';
import { chartPlugin } from './chart';
import '../../../src/styles.css';
import '../../../src/registry/widgets.css';
registerPluginUI(progressPlugin);registerPluginUI(chartPlugin);
const samples:number[]=[];
for(const method of ['update','render'] as const){const original=(WidgetHost.prototype as any)[method];(WidgetHost.prototype as any)[method]=function(...args:unknown[]){const start=performance.now();try{return original.apply(this,args);}finally{samples.push(performance.now()-start);if(samples.length>10000)samples.shift();}};}
(window as any).widgetFixture={counters,faults,samples};
createRoot(document.getElementById('root')!).render(<App/>);
