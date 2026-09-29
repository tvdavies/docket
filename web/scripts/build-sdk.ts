// Bundles the in-frame SDK client so plugins without a build step can import
// it from Docket: `import { connect } from "/plugin-sdk/v1/client.js"`.
import { resolve } from "node:path";
const root = resolve(import.meta.dirname, "../..");
const result = await Bun.build({
  entrypoints: [resolve(root, "packages/plugin-sdk/src/client.ts")],
  outdir: resolve(root, "web/dist/plugin-sdk/v1"),
  naming: "client.js",
  format: "esm",
  target: "browser",
  minify: true,
});
if (!result.success) {
  for (const log of result.logs) console.error(log);
  process.exit(1);
}
