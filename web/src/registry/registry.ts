import { useSyncExternalStore } from 'react';
import type { BoardTask, TaskReference } from '../types';
import type { DocketPluginUI, ReferenceResolverModule, ResolvedReference, TaskCardModule } from './contracts';

const cards: TaskCardModule[] = [];
const resolvers: ReferenceResolverModule[] = [];
const listeners = new Set<() => void>();
let version = 0;
let loaded = false;

export function registerPluginUI(plugin: DocketPluginUI) {
  for (const card of plugin.cards || []) if (!cards.some((item) => item.type === card.type)) cards.push(card);
  for (const resolver of plugin.referenceResolvers || []) if (!resolvers.some((item) => item.id === resolver.id)) resolvers.push(resolver);
  version += 1; for (const listener of listeners) listener();
}

export function cardModules(task: BoardTask) {
  return cards.filter((module) => { try { return module.appliesTo(task); } catch { return false; } });
}

export async function resolveReference(reference: TaskReference): Promise<ResolvedReference> {
  for (const resolver of resolvers) {
    let matches = false;
    try { matches = (!resolver.kinds?.length || resolver.kinds.includes(reference.kind)) && new RegExp(resolver.pattern).test(reference.url); } catch { matches = false; }
    if (!matches) continue;
    try { return await resolver.resolve(reference, { pluginBase: `/plugins/${resolver.id.split('/')[0]}` }); } catch { break; }
  }
  let hostname = reference.kind;
  try { hostname = new URL(reference.url).hostname || reference.kind; } catch { /* fallback */ }
  return { label: reference.title || hostname, icon: 'link', meta: { kind: reference.kind, host: hostname } };
}

export function useRegistryVersion() {
  return useSyncExternalStore((listener) => { listeners.add(listener); return () => listeners.delete(listener); }, () => version, () => version);
}

export function loadBuiltinPluginUI() {
  if (loaded) return;
  loaded = true;
  void import('./demo-plugin').then(({ demoPluginUI }) => registerPluginUI(demoPluginUI)).catch((error) => console.warn('Demo plugin UI failed to load', error));
}
