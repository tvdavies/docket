/** Canonical build-time UI contract. v1 is an ABI, not a snapshot alias. */
export interface TaskWait { id: string; kind: string; reason: string; reference?: string; since: string; actor?: string }
export type Wait = TaskWait;
export interface TaskReference { id: string; kind: string; url: string; title?: string; added_at: string; added_by?: string; resolver_id?: string; resolver_generation?: string }
export interface BoardTask {
  id: string; title: string; status: string; project?: string; labels: string[]; assignee?: string; wait?: TaskWait;
  references: TaskReference[]; active_sessions: unknown[]; sessions?: unknown[]; created_at: string; updated_at: string; resource_count: number;
  widget_summaries?: WidgetSummary[]; widget_revision?: string;
}
export type WidgetLocation = 'board' | 'activity';
export interface PluginCardDeclaration { type: string; title: string; locations?: WidgetLocation[] }
export interface PluginReferenceResolverDeclaration { id: string; pattern: string; kinds?: string[] }
export interface PluginMetadata { name: string; version: string; api_version?: number; cards: PluginCardDeclaration[]; reference_resolvers: PluginReferenceResolverDeclaration[]; service_base?: string }
export interface DocketPluginUI { cards?: TaskCardModule[]; referenceResolvers?: ReferenceResolverModule[] }
export interface TaskCardModule { type: string; appliesTo(task: BoardTask): boolean; mount(el: HTMLElement, ctx: CardContext): CardInstance }
export interface CardContext { workspace: string; task: BoardTask; pluginBase: string; refresh(): void }
export interface CardInstance { update(task: BoardTask): void; destroy(): void }
export interface ReferenceResolverModule { id: string; pattern: string; kinds?: string[]; resolve(ref: TaskReference, ctx: { pluginBase: string }): ResolvedReference | Promise<ResolvedReference> }
export interface ResolvedReference { label: string; icon?: string; meta?: Record<string, string>; href?: string }
export type PluginConfigFieldType = 'string' | 'number' | 'boolean' | 'list' | 'map';
export interface PluginConfigField { type: PluginConfigFieldType; required?: boolean; default?: unknown; enum?: unknown[]; secret?: boolean; description?: string }
export interface PluginConfigSchemas { instance?: Record<string, PluginConfigField>; workspace?: Record<string, PluginConfigField>; status?: Record<string, PluginConfigField> }
export type WidgetPriority = 'attention' | 'error' | 'active' | 'history';
export type WidgetTone = 'neutral' | 'positive' | 'warning' | 'danger' | 'info';
export interface WidgetReference { kind: string; url: string; title: string }
export interface WidgetFallback { label: string; status_label: string; summary?: string; priority: WidgetPriority; started_at?: string; ended_at?: string; references?: WidgetReference[] }
export interface WidgetRecordV1 { version: 1; widget_type: string; instance_id: string; task_id: string; created_at: string; revision: number; phase: 'created' | 'finalised'; fallback: WidgetFallback }
export interface WidgetSummary extends Omit<WidgetRecordV1, 'fallback'> { fallback: Pick<WidgetFallback, 'label' | 'status_label' | 'priority'> & { references?: WidgetReference[] } }
export interface WidgetIdentity { workspace: string; taskId: string; widgetType: string; instanceId: string }
export interface WidgetPreferences { theme: 'light' | 'dark'; density: 'compact' | 'comfortable'; reducedMotion: boolean }
export type WidgetAvailability = 'available' | 'missing_service' | 'plugin_disabled' | 'module_missing' | 'unsupported' | 'error';
export interface WidgetData { version: number; revision: number; value: unknown }
export interface WidgetFreshness { connection: string; receivedAt?: number; expiresAt?: number; stale: boolean; rehydrating: boolean; lastActivityAt?: string }
export interface WidgetSnapshot { task: BoardTask; data?: WidgetData; freshness: WidgetFreshness; availability: WidgetAvailability; fallback?: WidgetRecordV1 }
export interface WidgetContext {
  apiVersion: 2; identity: WidgetIdentity; location: WidgetLocation; serviceBase?: string; preferences: WidgetPreferences; signal: AbortSignal;
  helpers: { refreshTask(): void; hrefFor(reference: WidgetReference): string | null; requestDetail?(listener: (frame: DetailFrame) => void, onRevoked: (reason: DetailRevocation) => void): DetailLease | null };
}
export interface WidgetRow { key: string; order: number; role: 'text' | 'step' | 'code' | 'reference'; label: string; text?: string; reference?: WidgetReference }
export interface WidgetPresentation {
  label: string; status: { text: string; tone: WidgetTone }; terminal: boolean; priority: WidgetPriority;
  action?: string; notice?: { text: string; tone: WidgetTone }; summary?: string; rows?: WidgetRow[]; references?: WidgetReference[]; startedAt?: string; endedAt?: string;
}
export interface WidgetRenderState { expanded: boolean; held: boolean; displayed?: WidgetPresentation; data?: WidgetData; pending: boolean }
export interface WidgetInstance { update(snapshot: WidgetSnapshot, context: WidgetContext, view: WidgetRenderState): void; destroy(): void }
export interface WidgetModule { type: string; dataVersions: readonly number[]; present(snapshot: WidgetSnapshot, context: WidgetContext): WidgetPresentation | null; mount(body: HTMLElement, context: WidgetContext): WidgetInstance; detail?: DetailProvider }
export interface ReferenceResolverV2 { id: string; resolve(reference: TaskReference, context: { workspace: string; taskId: string; serviceBase?: string; signal: AbortSignal }): ResolvedReference | Promise<ResolvedReference> }
export interface DocketPluginUIV2 { apiVersion: 2; name: string; widgets?: WidgetModule[]; referenceResolvers?: ReferenceResolverV2[] }
export type PluginUI = DocketPluginUI | DocketPluginUIV2;
export type WidgetErrorCode = 'incompatible_api' | 'incompatible_data' | 'invalid_identity' | 'invalid_url' | 'invalid_payload' | 'duplicate_definition' | 'missing_service' | 'mount_failed' | 'update_failed' | 'destroy_failed' | 'detail_gap';
export interface WidgetError { code: WidgetErrorCode; phase: 'registration' | 'mount' | 'update' | 'destroy' | 'detail'; retryable: boolean }
export interface WidgetLivePayload { widget_version: 1; revision: number; data: { version: number; value: unknown }; last_activity_at?: string }
export interface LivePayload { kind: string; task?: string; session?: string; payload: unknown; ttl_ms: number }
export interface DetailFrame { version: 1; identity: WidgetIdentity; dataVersion: number; revision: number; baseSeq: number; throughSeq: number; reset: boolean; value: unknown }
export type DetailStatus = 'ready' | 'unavailable' | 'not_found' | 'error';
export type DetailRevocation = 'released' | 'reselected' | 'unavailable' | 'retired' | 'gap_timeout' | 'error' | 'not_found';
export interface DetailLease { release(): void; requestReset(): void }
export interface DetailConnection { requestReset(): void; close(): void }
export interface DetailProvider { open(context: { identity: WidgetIdentity; serviceBase?: string; signal: AbortSignal }, sink: { frame(value: DetailFrame): void; status(value: DetailStatus): void }): DetailConnection }
