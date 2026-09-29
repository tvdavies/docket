import { build } from "vite";
import {
  mkdtemp,
  readdir,
  readFile,
  rm,
  mkdir,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { resolve, join } from "node:path";
import { gzipSync } from "node:zlib";
const root = resolve(import.meta.dirname, "..");
async function size(path: string): Promise<number> {
  let bytes = 0;
  for (const entry of await readdir(path, { withFileTypes: true })) {
    const full = join(path, entry.name);
    if (entry.isDirectory()) bytes += await size(full);
    else if (/\.(js|css|html)$/.test(entry.name))
      bytes += gzipSync(await readFile(full)).length;
  }
  return bytes;
}
const temporary = await mkdtemp(join(tmpdir(), "docket-widget-bundle-"));
try {
  const production = await size(resolve(root, "dist"));
  // b8005ef includes the merged settings foundation. Do not reset this baseline
  // upward to hide growth: the approved incremental kit/adapter limit is 30 KiB.
  const baseline = 148604;
  await build({
    root,
    configFile: resolve(root, "vite.config.ts"),
    build: {
      outDir: temporary,
      emptyOutDir: true,
      rollupOptions: {
        input: resolve(root, "tests/fixtures/widgets/index.html"),
      },
    },
  });
  const fixture = await size(temporary),
    result = {
      baseline,
      production,
      coreIncrease: production - baseline,
      fixture,
      fixtureIncrease: fixture - production,
      totalCeiling: 170 * 1024,
      incrementCeiling: 30 * 1024,
    };
  if (
    production > result.totalCeiling ||
    result.coreIncrease > result.incrementCeiling ||
    result.fixtureIncrease > result.incrementCeiling
  )
    throw Error("Widget bundle budget exceeded: " + JSON.stringify(result));
  const evidence = resolve(
    process.env.WIDGET_EVIDENCE_DIR || resolve(root, "../docs/widget-evidence"),
  );
  await mkdir(evidence, { recursive: true });
  await writeFile(
    join(evidence, "bundle-results.json"),
    JSON.stringify(result, null, 2) + "\n",
  );
  console.log(result);
} finally {
  await rm(temporary, { recursive: true, force: true });
}
