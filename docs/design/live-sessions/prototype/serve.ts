// Loopback static server for the JOB-0092 fixture demo.
//
//   bun docs/design/live-sessions/prototype/serve.ts            # prints the URL
//   PORT=4173 bun docs/design/live-sessions/prototype/serve.ts
//
// Serves only allowlisted static assets and the demo board/task/session
// routes (with refresh fallback). Rejects /api, WebSocket upgrades and every
// non-GET/HEAD method. It never reads DOCKET_HOME, registry state or live
// task/session stores, and the page's CSP forbids all network connections.

import { resolve } from "node:path";

const root = import.meta.dir;

export const CSP = [
  "default-src 'none'",
  "script-src 'self'",
  "style-src 'self'",
  "img-src 'none'",
  "font-src 'none'",
  "connect-src 'none'",
  "form-action 'none'",
  "frame-ancestors 'none'",
  "base-uri 'none'",
  "object-src 'none'",
].join("; ");

const ASSETS: Record<string, { file: string; type: string }> = {
  "/demo.css": { file: "demo.css", type: "text/css; charset=utf-8" },
  "/components/tokens.css": { file: "components/tokens.css", type: "text/css; charset=utf-8" },
  "/components/kit.css": { file: "components/kit.css", type: "text/css; charset=utf-8" },
};

const DEMO_ROUTES = [
  /^\/$/,
  /^\/workspaces\/demo\/?$/,
  /^\/workspaces\/demo\/tasks\/[A-Z0-9-]+$/,
  /^\/plugins\/dispatch\/sessions\/[^/]+$/,
];

let bundle: { code: string; builtAt: number } | null = null;

async function buildBundle(): Promise<string> {
  const result = await Bun.build({ entrypoints: [resolve(root, "app.ts")], target: "browser", format: "esm", minify: false, sourcemap: "none" });
  if (!result.success) throw new Error(result.logs.map((log) => log.message).join("\n"));
  const code = await result.outputs[0].text();
  bundle = { code, builtAt: Date.now() };
  return code;
}

const baseHeaders = { "Content-Security-Policy": CSP, "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer" };

export async function handle(request: Request): Promise<Response> {
  const url = new URL(request.url);
  if (request.headers.get("upgrade")?.toLowerCase() === "websocket") return new Response("WebSocket not supported by the fixture demo", { status: 426, headers: baseHeaders });
  if (request.method !== "GET" && request.method !== "HEAD") return new Response("Fixture demo is read-only", { status: 405, headers: { ...baseHeaders, Allow: "GET, HEAD" } });
  if (url.pathname.startsWith("/api") || url.pathname.endsWith("/stream")) return new Response("No API in the fixture demo", { status: 404, headers: baseHeaders });
  if (url.pathname === "/app.js") {
    const code = bundle && process.env.DEMO_REBUILD !== "1" ? bundle.code : await buildBundle();
    return new Response(code, { headers: { ...baseHeaders, "Content-Type": "text/javascript; charset=utf-8" } });
  }
  const asset = ASSETS[url.pathname];
  if (asset) return new Response(Bun.file(resolve(root, asset.file)), { headers: { ...baseHeaders, "Content-Type": asset.type } });
  if (DEMO_ROUTES.some((route) => route.test(url.pathname))) return new Response(Bun.file(resolve(root, "index.html")), { headers: { ...baseHeaders, "Content-Type": "text/html; charset=utf-8" } });
  // Unknown path: still serve the shell so the app can render its honest not-found page.
  return new Response(Bun.file(resolve(root, "index.html")), { status: 404, headers: { ...baseHeaders, "Content-Type": "text/html; charset=utf-8" } });
}

export function startServer(port = Number(process.env.PORT ?? 0)) {
  return Bun.serve({ hostname: "127.0.0.1", port, fetch: handle });
}

if (import.meta.main) {
  await buildBundle();
  const server = startServer();
  console.log(`JOB-0092 fixture demo: http://127.0.0.1:${server.port}/workspaces/demo`);
  console.log("No live agents, no API, no network. Press Ctrl+C to stop.");
}
