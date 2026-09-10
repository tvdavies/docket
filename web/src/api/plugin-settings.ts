import type { PluginConfigField, PluginConfigFieldType, PluginConfigSchemas } from '../../../docs/plugin-ui';
import { api, workspacePath } from './client';

export type { PluginConfigField, PluginConfigFieldType, PluginConfigSchemas };

export type PluginWorkspaceValues = { config: Record<string, unknown>; statuses: Record<string, Record<string, unknown>> };
export type PluginCatalogueEntry = {
  name: string;
  version: string;
  description?: string;
  source: { type: string; url?: string; ref?: string; commit?: string };
  schemas: PluginConfigSchemas;
  /** Default-resolved, secret-free instance values. The API does not say which were stored. */
  instance_values: Record<string, unknown>;
  /** Raw stored board and lane keys for every registered workspace that enables the plugin. */
  workspace_values: Record<string, PluginWorkspaceValues>;
};
export type ConfigPatchResponse = { plugin: string; status?: string; values: Record<string, unknown> };

export const FIELD_TYPES: readonly PluginConfigFieldType[] = ['string', 'number', 'boolean', 'list', 'map'];

const isRecord = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);

/** Rejects a schema whose shape the generated editors cannot honour. */
export function invalidSchemaReason(schema: unknown, scope?: 'instance' | 'workspace' | 'status'): string {
  if (schema === undefined || schema === null) return '';
  if (!isRecord(schema)) return 'schema is not an object';
  for (const [key, raw] of Object.entries(schema)) {
    if (!/^[a-z0-9][a-z0-9_-]*$/.test(key)) return 'schema contains an invalid field name';
    if (!isRecord(raw)) return `field ${key} is not an object`;
    if (!FIELD_TYPES.includes(raw.type as PluginConfigFieldType)) return `field ${key} has unsupported type ${JSON.stringify(raw.type)}`;
    if (raw.enum !== undefined && !Array.isArray(raw.enum)) return `field ${key} enum is not an array`;
    if (raw.description !== undefined && typeof raw.description !== 'string') return `field ${key} description is not text`;
    if (raw.required !== undefined && typeof raw.required !== 'boolean') return `field ${key} required marker is malformed`;
    if (raw.secret !== undefined && typeof raw.secret !== 'boolean') return `field ${key} secret marker is malformed`;
    if (raw.secret && scope && scope !== 'instance') return `field ${key} declares a secret outside instance scope`;
    if (raw.secret && (raw.default != null || (Array.isArray(raw.enum) && raw.enum.length))) return `secret field ${key} declares values`;
    const matches = (value: unknown) => raw.type === 'list' ? Array.isArray(value) : raw.type === 'map' ? isRecord(value) : typeof value === raw.type && (raw.type !== 'number' || Number.isFinite(value));
    if (raw.default != null && !matches(raw.default)) return `field ${key} default has the wrong type`;
    if (Array.isArray(raw.enum) && raw.enum.some((value) => !matches(value))) return `field ${key} enum has the wrong type`;
  }
  return '';
}

function checkEntry(raw: unknown): PluginCatalogueEntry {
  if (!isRecord(raw) || typeof raw.name !== 'string' || typeof raw.version !== 'string') throw new Error('Plugin catalogue entry is malformed');
  if (!isRecord(raw.schemas) || !isRecord(raw.instance_values) || !isRecord(raw.workspace_values)) throw new Error(`Plugin ${raw.name}: catalogue values are malformed`);
  const schemas = raw.schemas;
  // Never carry a declared secret into editor baselines, even from a faulty response.
  const instance = Object.fromEntries(Object.entries(raw.instance_values).filter(([key]) => !isRecord(schemas.instance) || !isRecord(schemas.instance[key]) || !schemas.instance[key].secret));
  const workspaces: Record<string, PluginWorkspaceValues> = Object.create(null);
  if (isRecord(raw.workspace_values)) {
    for (const [name, value] of Object.entries(raw.workspace_values)) {
      if (!isRecord(value)) throw new Error(`Plugin ${raw.name}: workspace ${name} values are malformed`);
      if (!isRecord(value.config) || !isRecord(value.statuses)) throw new Error(`Plugin ${raw.name}: workspace ${name} values are malformed`);
      const statuses: Record<string, Record<string, unknown>> = Object.create(null);
      if (isRecord(value.statuses)) for (const [status, lane] of Object.entries(value.statuses)) { if (!isRecord(lane)) throw new Error(`Plugin ${raw.name}: lane ${status} values are malformed`); statuses[status] = lane; }
      workspaces[name] = { config: isRecord(value.config) ? value.config : {}, statuses };
    }
  }
  return {
    name: raw.name, version: raw.version, description: typeof raw.description === 'string' ? raw.description : undefined,
    source: isRecord(raw.source) && typeof raw.source.type === 'string' ? raw.source as PluginCatalogueEntry['source'] : { type: 'unknown' },
    schemas: schemas as PluginConfigSchemas, instance_values: instance, workspace_values: workspaces,
  };
}

/** GET /api/plugins. A failed or malformed response is an error, never an empty catalogue. */
export async function listPluginCatalogue(signal?: AbortSignal): Promise<PluginCatalogueEntry[]> {
  const payload = await api<unknown>('/api/plugins', { signal });
  if (!Array.isArray(payload)) throw new Error('Plugin catalogue response was not a list');
  return payload.map(checkEntry);
}

const body = (values: Record<string, unknown>) => ({ method: 'PATCH', body: JSON.stringify({ values }) });
export const pluginConfigPath = (plugin: string) => `/api/plugins/${encodeURIComponent(plugin)}/config`;
export const workspacePluginConfigPath = (workspace: string, plugin: string) => `${workspacePath(workspace)}/plugins/${encodeURIComponent(plugin)}/config`;
export const statusPluginConfigPath = (workspace: string, plugin: string, status: string) => `${workspacePath(workspace)}/plugins/${encodeURIComponent(plugin)}/statuses/${encodeURIComponent(status)}`;

async function patchConfig(path: string, values: Record<string, unknown>): Promise<ConfigPatchResponse> {
  const result = await api<unknown>(path, body(values));
  if (!isRecord(result) || typeof result.plugin !== 'string' || !isRecord(result.values)) throw new TypeError('Docket returned an invalid save response');
  return result as ConfigPatchResponse;
}
export const patchInstanceConfig = (plugin: string, values: Record<string, unknown>) => patchConfig(pluginConfigPath(plugin), values);
export const patchWorkspaceConfig = (workspace: string, plugin: string, values: Record<string, unknown>) => patchConfig(workspacePluginConfigPath(workspace, plugin), values);
export const patchStatusConfig = (workspace: string, plugin: string, status: string, values: Record<string, unknown>) => patchConfig(statusPluginConfigPath(workspace, plugin, status), values);
