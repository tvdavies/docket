// Link validation shared by the host and tests. Only same-origin plugin
// session/task paths and https plan/PR references are rendered; everything
// else (localhost, javascript:, file:, http:) is rejected rather than shown.

import type { WidgetReference } from "../contracts.proposed";

export function safeHref(ref: WidgetReference): string | null {
  const url = ref.url;
  if (ref.kind === "session") return /^\/plugins\/[a-z0-9-]+\/sessions\/[A-Za-z0-9%._-]+$/.test(url) ? url : null;
  if (ref.kind === "task") return /^\/workspaces\/[a-z0-9-]+\/tasks\/[A-Z0-9-]+$/.test(url) ? url : null;
  if (/^https:\/\/[^/\s]+/.test(url)) return url;
  return null;
}
