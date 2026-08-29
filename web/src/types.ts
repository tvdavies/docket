export type Wait = {
  id: string;
  kind: string;
  reason: string;
  reference?: string;
  since: string;
  actor?: string;
};

export type TaskReference = {
  id: string;
  kind: string;
  url: string;
  title?: string;
  added_at: string;
  added_by?: string;
};

export type BoardTask = {
  id: string;
  title: string;
  status: string;
  project?: string;
  labels: string[];
  assignee?: string;
  wait?: Wait;
  references: TaskReference[];
  active_sessions: unknown[];
  created_at: string;
  updated_at: string;
  resource_count: number;
};

export type PluginCardDeclaration = { type: string; title: string };
export type PluginReferenceResolverDeclaration = { id: string; pattern: string; kinds?: string[] };
export type PluginMetadata = {
  name: string;
  version: string;
  cards: PluginCardDeclaration[];
  reference_resolvers: PluginReferenceResolverDeclaration[];
  service_base?: string;
};
export type StreamConfig = { statuses: string[]; terminal: string[]; labels: string[]; plugins?: PluginMetadata[] };
export type StreamInit = { workspace: string; config: StreamConfig; tasks: BoardTask[]; cursor: string };
export type LedgerEvent = { seq: number; time: string; type: string; task?: string; title?: string; actor?: string; assignee?: string; data?: Record<string, unknown> };
export type StreamPatch = { event: LedgerEvent; task?: BoardTask };
export type LivePayload = { kind: string; task?: string; session?: string; payload: unknown; ttl_ms: number };

export type WorkspaceStatus = {
  name: string;
  path: string;
  state: string;
  event_count: number;
  handler_count: number;
  last_event?: string;
  last_error?: string;
  updated_at: string;
};

export type ProjectRef = { id: string; name?: string };
export type TaskRef = { id: string; title?: string };
export type Attachment = { file: string; mime: string; caption?: string; added_by?: string; added_at: string; bytes: number };
export type ActivityEntry = { at: string; kind: string; type: string; actor?: string; session?: string; body?: string; body_html?: string; data?: Record<string, unknown> };
export type Comment = { author: string; session?: string; created_at: string; body: string };

export type TaskDetail = {
  id: string;
  title: string;
  status: string;
  created_at: string;
  updated_at: string;
  project?: ProjectRef;
  labels: string[];
  assignee?: string;
  wait?: Wait;
  references: TaskReference[];
  description: string;
  description_html: string;
  relationships?: Record<string, TaskRef[]>;
  comments: Comment[];
  attachments: Attachment[];
  activity: ActivityEntry[];
  cursor?: string;
};

export type TaskPatch = Partial<Pick<TaskDetail, 'title' | 'description' | 'assignee' | 'labels' | 'status'>>;
export type CreateTaskInput = { title: string; description?: string; project?: string; labels?: string[]; assignee?: string; status?: string };
