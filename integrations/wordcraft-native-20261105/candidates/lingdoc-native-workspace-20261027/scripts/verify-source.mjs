import { createHash } from 'node:crypto';
import { readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const sourceRoot = path.join(root, 'engine');
const manifestPath = path.join(root, 'fixtures', 'source-baseline.sha256');
const expectedManifestSha256 = '71a5c3fcb86c93eb40f7812e686259820956eb51d76d7a7fd0a96550a360ec0a';
const sha256 = bytes => createHash('sha256').update(bytes).digest('hex');
const manifestBytes = await readFile(manifestPath);
const manifestSha256 = sha256(manifestBytes);
const lines = manifestBytes.toString('utf8').split(/\r?\n/).filter(Boolean);
const entries = lines.map(line => {
  const match = /^([0-9a-f]{64})  (.+)$/.exec(line);
  if (!match) throw new Error(`Invalid manifest line: ${line}`);
  return { expected: match[1], relativePath: match[2] };
});
const issues = [];
let matched = 0;
const seen = new Set();
for (const entry of entries) {
  const normalized = path.posix.normalize(entry.relativePath);
  if (seen.has(entry.relativePath) || normalized !== entry.relativePath || normalized.startsWith('../') || path.posix.isAbsolute(normalized)) {
    issues.push({ ...entry, issue: 'unsafe or duplicate manifest path' });
    continue;
  }
  seen.add(entry.relativePath);
  try {
    const actual = sha256(await readFile(path.join(sourceRoot, ...normalized.split('/'))));
    if (actual === entry.expected) matched += 1;
    else issues.push({ ...entry, actual, issue: 'hash mismatch' });
  } catch (error) {
    issues.push({ ...entry, issue: String(error) });
  }
}
const result = {
  kind: 'S0 frozen source manifest verification',
  checkedAt: new Date().toISOString(),
  sourceRoot,
  manifestPath,
  manifestSha256,
  expectedManifestSha256,
  expectedFiles: entries.length,
  matched,
  issues,
  passed: manifestSha256 === expectedManifestSha256 && entries.length === 638 && matched === 638 && issues.length === 0,
};
if (process.argv[2]) {
  const output = path.resolve(process.argv[2]);
  await writeFile(output, JSON.stringify(result, null, 2) + '\n');
}
console.log(JSON.stringify(result, null, 2));
if (!result.passed) process.exitCode = 1;
