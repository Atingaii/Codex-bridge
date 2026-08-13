import { readdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';

const outputDir = path.resolve(import.meta.dirname, '../../internal/web/static');

async function normalizeDirectory(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  await Promise.all(entries.map(async (entry) => {
    const filePath = path.join(directory, entry.name);
    if (entry.isDirectory()) {
      await normalizeDirectory(filePath);
      return;
    }
    if (!/\.(?:css|html|js|json)$/.test(entry.name)) return;
    const source = await readFile(filePath, 'utf8');
    const normalized = source.replace(/[\t ]+$/gm, '');
    if (normalized !== source) await writeFile(filePath, normalized);
  }));
}

await normalizeDirectory(outputDir);
