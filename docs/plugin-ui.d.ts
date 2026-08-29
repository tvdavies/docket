export interface DocketPluginUI {
  cards?: TaskCardModule[];
  referenceResolvers?: ReferenceResolverModule[];
}

export interface BoardTask {
  id: string;
  title: string;
  status: string;
  project?: string;
  labels: string[];
  assignee?: string;
  wait?: TaskWait;
  references: TaskReference[];
  active_sessions: unknown[];
  resource_count: number;
  created_at: string;
  updated_at: string;
}

export interface TaskWait {
  id: string;
  kind: string;
  reason: string;
  reference?: string;
  since: string;
  actor: string;
}

export interface TaskReference {
  id: string;
  kind: string;
  url: string;
  title?: string;
  added_at: string;
  added_by: string;
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
  resolve(
    ref: TaskReference,
    ctx: { pluginBase: string },
  ): ResolvedReference | Promise<ResolvedReference>;
}

export interface ResolvedReference {
  label: string;
  icon?: string;
  meta?: Record<string, string>;
}

export type PluginConfigFieldType =
  | "string"
  | "number"
  | "boolean"
  | "list"
  | "map";

export interface PluginConfigField {
  type: PluginConfigFieldType;
  required?: boolean;
  default?: unknown;
  enum?: unknown[];
  secret?: boolean;
  description?: string;
}

export interface PluginConfigSchemas {
  instance?: Record<string, PluginConfigField>;
  workspace?: Record<string, PluginConfigField>;
  status?: Record<string, PluginConfigField>;
}
