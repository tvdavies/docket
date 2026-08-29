import { gzipSync } from 'node:zlib';
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

const root = new URL('../dist/', import.meta.url).pathname;
const files = [];
const walk = (dir) => {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) walk(path);
    else if (/\.(html|css|js)$/.test(entry.name)) files.push(path);
  }
};
walk(root);
const rows = files.map((path) => ({ path: path.slice(root.length), bytes: gzipSync(readFileSync(path)).byteLength }));
const total = rows.reduce((sum, row) => sum + row.bytes, 0);
for (const row of rows) console.log(`${String(row.bytes).padStart(7)} B gzip  ${row.path}`);
console.log(`${String(total).padStart(7)} B gzip  initial total`);
if (total > 170 * 1024) {
  console.error(`bundle budget exceeded: ${total} > ${170 * 1024} bytes gzip`);
  process.exit(1);
}
