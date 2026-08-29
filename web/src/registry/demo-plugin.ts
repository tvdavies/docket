import type { DocketPluginUI } from './contracts';

export const demoPluginUI: DocketPluginUI = {
  cards: [{
    type: 'demo/plan-status',
    appliesTo: (task) => task.references.some((reference) => reference.kind === 'plan'),
    mount(el, ctx) {
      el.className = 'demo-plugin-card';
      const render = (task: typeof ctx.task) => {
        const plans = task.references.filter((reference) => reference.kind === 'plan').length;
        el.textContent = `Plan linked · ${task.status} · ${plans} reference${plans === 1 ? '' : 's'}`;
      };
      render(ctx.task);
      el.addEventListener('dblclick', ctx.refresh);
      return { update: render, destroy() { el.replaceChildren(); } };
    },
  }],
  referenceResolvers: [{
    id: 'demo/plan',
    pattern: '^https://plans\\.myslop\\.app/p/',
    kinds: ['plan'],
    resolve(ref) {
      const id = ref.url.split('/').filter(Boolean).at(-1) || '';
      return { label: ref.title || 'Implementation plan', icon: 'plan', meta: { provider: 'myslop plans', id } };
    },
  }],
};
