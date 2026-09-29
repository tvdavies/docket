import type { LivePayload, StreamConfig, StreamInit, StreamPatch } from '../types';
import type { BoardStore } from '../store/board-store';

function parse<T>(event: MessageEvent<string>): T | null {
  try { return JSON.parse(event.data) as T; } catch { return null; }
}

export function connectWorkspaceStream(workspace: string, store: BoardStore): () => void {
  store.setConnection('connecting');
  const source = new EventSource(`/api/workspaces/${encodeURIComponent(workspace)}/stream`, { withCredentials: true });
  let opened = false;
  const abort = new AbortController();
  let projectionRequest: Promise<void> | undefined;
  let requestedGeneration = '';
  const refreshProjections = () => {
    if (projectionRequest || abort.signal.aborted) return;
    const generation = store.getSnapshot().config.resolver_generation || '';
    requestedGeneration = generation;
    projectionRequest = fetch(`/api/workspaces/${encodeURIComponent(workspace)}/board`, { signal: abort.signal }).then(async response => {
      if (!response.ok) return;
      const value = await response.json();
      if (!abort.signal.aborted) store.replaceReferenceProjections(value.tasks, value.resolver_generation);
    }).catch(() => undefined).finally(() => { projectionRequest = undefined; if (!abort.signal.aborted && requestedGeneration !== (store.getSnapshot().config.resolver_generation || '')) refreshProjections(); });
  };
  source.onopen = () => {
    opened = true;
    store.setConnection('open');
  };
  source.onerror = () => store.setConnection(opened ? 'reconnecting' : 'connecting');
  source.addEventListener('init', (raw) => {
    const event = raw as MessageEvent<string>;
    const value = parse<StreamInit>(event);
    if (value) store.applyInit(value, event.lastEventId);
  });
  source.addEventListener('patch', (raw) => {
    const event = raw as MessageEvent<string>;
    const value = parse<StreamPatch>(event);
    if (value) store.applyPatch(value, event.lastEventId);
  });
  source.addEventListener('config', (raw) => {
    const value = parse<StreamConfig>(raw as MessageEvent<string>);
    if (value) {
      const previous = store.getSnapshot().config.resolver_generation;
      store.applyConfig(value);
      if (value.resolver_generation && previous !== value.resolver_generation) refreshProjections();
    }
  });
  source.addEventListener('live', (raw) => {
    const value = parse<LivePayload>(raw as MessageEvent<string>);
    if (value) store.applyLive(value);
  });
  return () => {
    abort.abort();
    source.close();
    store.setConnection('closed');
  };
}
