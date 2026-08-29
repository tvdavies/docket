import type { BoardTask, TaskReference } from '../types';

// Approved JOB-0047 plugin UI v1 contracts. They are intentionally framework
// neutral so the React board can host build-time modules and a future ESM
// loader without changing plugin implementations.
export interface DocketPluginUI {
  cards?: TaskCardModule[];
  referenceResolvers?: ReferenceResolverModule[];
}

export interface TaskCardModule {
  type: string;
  appliesTo(task: BoardTask): boolean;
  mount(el: HTMLElement, ctx: CardContext): CardInstance;
}

export interface CardContext {
  workspace: string;
  task: BoardTask;
  pluginBase: string;
  refresh(): void;
}

export interface CardInstance {
  update(task: BoardTask): void;
  destroy(): void;
}

export interface ReferenceResolverModule {
  id: string;
  pattern: string;
  kinds?: string[];
  resolve(ref: TaskReference, ctx: { pluginBase: string }): ResolvedReference | Promise<ResolvedReference>;
}

export interface ResolvedReference {
  label: string;
  icon?: string;
  meta?: Record<string, string>;
}
