import { useEffect, useRef } from 'react';
import type { BoardTask } from '../types';
import type { CardInstance } from './contracts';
import { cardModules, useRegistryVersion } from './registry';

export function PluginCardHost({ workspace, task, refresh = () => undefined }: { workspace: string; task: BoardTask; refresh?(): void }) {
  const registryVersion = useRegistryVersion();
  const root = useRef<HTMLDivElement>(null);
  const instances = useRef<CardInstance[]>([]);
  const modules = cardModules(task);
  const moduleKey = modules.map((module) => module.type).join('\0');
  useEffect(() => {
    const host = root.current; if (!host) return;
    host.replaceChildren(); instances.current = [];
    for (const module of modules) {
      const slot = document.createElement('div'); slot.dataset.pluginCard = module.type; host.append(slot);
      try {
        const pluginName = module.type.split('/')[0];
        instances.current.push(module.mount(slot, { workspace, task, pluginBase: `/plugins/${pluginName}`, refresh }));
      } catch (error) {
        slot.className = 'plugin-card-error'; slot.textContent = 'Plugin card unavailable'; console.warn(`Plugin card ${module.type} failed`, error);
      }
    }
    return () => { for (const instance of instances.current) { try { instance.destroy(); } catch { /* isolate plugin cleanup */ } } instances.current = []; host.replaceChildren(); };
  }, [workspace, task.id, registryVersion, moduleKey]); // remount when applicability or registry changes
  useEffect(() => { for (const instance of instances.current) { try { instance.update(task); } catch (error) { console.warn('Plugin card update failed', error); } } }, [task]);
  return <div className="plugin-card-host" ref={root} />;
}
