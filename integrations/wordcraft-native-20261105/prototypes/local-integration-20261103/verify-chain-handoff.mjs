import { readFile, lstat } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
import path from 'node:path';

const root = path.dirname(fileURLToPath(import.meta.url));
const manifest = JSON.parse(await readFile(path.join(root, 'CHAIN-INTEGRATION.json'), 'utf8'));
if (manifest.baselineId !== 'LD-V1-20261103-R1' || manifest.componentVersion !== 'review-chain-0.2.0') {
  throw new Error('Unexpected baseline or component version');
}
for (const item of manifest.files) {
  const target = path.resolve(root, item.path);
  const relative = path.relative(root, target);
  if (!relative || relative.startsWith('..') || path.isAbsolute(relative)) throw new Error('Unsafe manifest path');
  const stat = await lstat(target);
  if (!stat.isFile() || stat.isSymbolicLink()) throw new Error(`Not a regular file: ${item.path}`);
  const sha256 = createHash('sha256').update(await readFile(target)).digest('hex');
  if (sha256 !== item.sha256) throw new Error(`Hash mismatch: ${item.path}`);
}
const frozen = spawnSync(process.execPath, [path.join(root, 'frontend/handoff/verify-handoff.mjs')], { encoding: 'utf8', windowsHide: true });
if (frozen.status !== 0) throw new Error(frozen.stderr || frozen.stdout || 'Frozen design verification failed');
console.log(JSON.stringify({ baselineId: manifest.baselineId, componentVersion: manifest.componentVersion, filesChecked: manifest.files.length, hashesMatch: true, frozenDesignCheck: 'passed', cloudKimiAcceptance: 'pending' }, null, 2));
