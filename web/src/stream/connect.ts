import type { LivePayload, StreamConfig, StreamInit, StreamPatch } from '../types';
import type { BoardStore } from '../store/board-store';

function parse<T>(event: MessageEvent<string>): T | null {
  try { return JSON.parse(event.data) as T; } catch { return null; }
}

export function connectWorkspaceStream(workspace: string, store: BoardStore): () => void {
  store.setConnection('connecting');
  const source = new EventSource(`/api/workspaces/${encodeURIComponent(workspace)}/stream`, { withCredentials: true });
  let opened = false;
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
    if (value) store.applyConfig(value);
  });
  source.addEventListener('live', (raw) => {
    const value = parse<LivePayload>(raw as MessageEvent<string>);
    if (value) store.applyLive(value);
  });
  return () => {
    source.close();
    store.setConnection('closed');
  };
}
