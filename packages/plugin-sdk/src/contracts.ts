/** Wire types shared by the Docket host and sandboxed plugin frames. */
export interface TaskWait {
  id: string;
  kind: string;
  reason: string;
  reference?: string;
  since: string;
  actor?: string;
}
export type Wait = TaskWait;
export interface TaskReference {
  id: string;
  kind: string;
  url: string;
  title?: string;
  added_at: string;
  added_by?: string;
  resolver_id?: string;
  resolver_generation?: string;
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
  sessions?: unknown[];
  created_at: string;
  updated_at: string;
  resource_count: number;
  widget_summaries?: WidgetSummary[];
  widget_revision?: string;
}
export type WidgetLocation = "board" | "activity";
/** Capabilities a frame may request through the host bridge. */
export type PluginCapability =
  | "task.read"
  | "task.comment"
  | "task.move"
  | "service.fetch"
  | "service.stream"
  | "open.external";
/** A ledger widget type. `entry` is the frame opened from the activity timeline. */
export interface PluginWidgetDeclaration {
  type: string;
  title: string;
  entry?: string;
  slots: WidgetLocation[];
}
/** A task detail panel or workspace page rendered as a sandboxed frame. */
export interface PluginViewDeclaration {
  id: string;
  title: string;
  entry: string;
}
/** @deprecated Retired build-time card declaration; read `widgets` instead. */
export interface PluginCardDeclaration {
  type: string;
  title: string;
  locations?: WidgetLocation[];
}
export interface PluginReferenceResolverDeclaration {
  id: string;
  pattern: string;
  kinds?: string[];
  /** Service path answering POST {workspace, task_id, reference} with a ResolvedReference. */
  endpoint?: string;
}
export interface PluginMetadata {
  name: string;
  version: string;
  api_version?: number;
  cards?: PluginCardDeclaration[];
  reference_resolvers: PluginReferenceResolverDeclaration[];
  service_base?: string;
  widgets?: PluginWidgetDeclaration[];
  panels?: PluginViewDeclaration[];
  pages?: PluginViewDeclaration[];
  capabilities?: PluginCapability[];
  /** `/plugin-ui/<name>/<hash>`; changes whenever the plugin's ui.dir changes. */
  ui_base?: string;
}
export interface ResolvedReference {
  label: string;
  icon?: string;
  meta?: Record<string, string>;
  href?: string;
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
  /**
   * Service path (for example "/options/models") whose JSON response lists the
   * field's choices: an array of values or of { value, label } objects.
   * Settings forms fetch it through the plugin proxy and fall back to free
   * input when it fails. Only valid on string and number fields.
   */
  options_from?: string;
}
export interface PluginConfigSchemas {
  instance?: Record<string, PluginConfigField>;
  workspace?: Record<string, PluginConfigField>;
  status?: Record<string, PluginConfigField>;
}
export type WidgetPriority = "attention" | "error" | "active" | "history";
export type WidgetTone = "neutral" | "positive" | "warning" | "danger" | "info";
export interface WidgetReference {
  kind: string;
  url: string;
  title: string;
}
export interface WidgetFallback {
  label: string;
  status_label: string;
  summary?: string;
  priority: WidgetPriority;
  started_at?: string;
  ended_at?: string;
  references?: WidgetReference[];
}
export interface WidgetRecordV1 {
  version: 1;
  widget_type: string;
  instance_id: string;
  task_id: string;
  created_at: string;
  revision: number;
  phase: "created" | "finalised";
  fallback: WidgetFallback;
}
export interface WidgetSummary extends Omit<WidgetRecordV1, "fallback"> {
  fallback: Pick<WidgetFallback, "label" | "status_label" | "priority"> & {
    references?: WidgetReference[];
  };
}
export interface WidgetIdentity {
  workspace: string;
  taskId: string;
  widgetType: string;
  instanceId: string;
}
export interface WidgetPreferences {
  theme: "light" | "dark";
  density: "compact" | "comfortable";
  reducedMotion: boolean;
}
export interface WidgetData {
  version: number;
  revision: number;
  value: unknown;
}
export interface WidgetFreshness {
  connection: string;
  receivedAt?: number;
  expiresAt?: number;
  stale: boolean;
  rehydrating: boolean;
  lastActivityAt?: string;
}
export interface WidgetRow {
  key: string;
  order: number;
  role: "text" | "step" | "code" | "reference";
  label: string;
  text?: string;
  reference?: WidgetReference;
}
/**
 * Declarative widget view. Hosts render it without running plugin code; a
 * live preview publishes it as `data.value.presentation`.
 */
export interface WidgetPresentation {
  label: string;
  status: { text: string; tone: WidgetTone };
  terminal: boolean;
  priority: WidgetPriority;
  action?: string;
  notice?: { text: string; tone: WidgetTone };
  summary?: string;
  rows?: WidgetRow[];
  references?: WidgetReference[];
  startedAt?: string;
  endedAt?: string;
}
export interface WidgetLivePayload {
  widget_version: 1;
  revision: number;
  data: { version: number; value: unknown };
  last_activity_at?: string;
}
export interface LivePayload {
  kind: string;
  task?: string;
  session?: string;
  payload: unknown;
  ttl_ms: number;
}
