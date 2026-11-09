import { readFile, writeFile, mkdir } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';

export const UPSTREAM_COMMIT = '04b1de949f9f2f14971bf2e26f4a7fd8cc392626';
export const UPSTREAM_REPOSITORY = 'https://github.com/DayuanJiang/next-ai-draw-io.git';
export const here = path.dirname(fileURLToPath(import.meta.url));
export const sourceDirectory = path.join(here, 'runtime', 'source');
const digest = bytes => createHash('sha256').update(bytes).digest('hex');
const gitDigest = bytes => createHash('sha1').update(`blob ${bytes.length}\0`).update(bytes).digest('hex');

const patches = [
  {
    file: 'components/workspace/workspace.tsx',
    changes: [
      ['import { CanvasStage } from "@/components/canvas/canvas-stage"', 'import { LingDocBridge } from "@/components/lingdoc-bridge"\nimport { CanvasStage } from "@/components/canvas/canvas-stage"'],
      ['            <ChatVersionsProvider>\n                <WorkspaceLayout />', '            <ChatVersionsProvider>\n                <LingDocBridge />\n                <WorkspaceLayout />'],
    ],
  },
  {
    file: 'proxy.ts',
    changes: [
      ['import { i18n } from "./lib/i18n/config"', 'import { i18n } from "./lib/i18n/config"\nimport { lingdocBoundary, lingdocContinue } from "./lib/lingdoc-boundary"'],
      ['export function proxy(request: NextRequest) {\n    const pathname', 'export function proxy(request: NextRequest) {\n    const refused = lingdocBoundary(request)\n    if (refused) return refused\n    const pathname'],
      ['        return\n    }\n\n    // Skip static files', '        // LingDoc owns the model. Never select browser-stored credentials.\n        const headers = new Headers(request.headers)\n        for (const name of [...headers.keys()]) {\n            if (name.startsWith("x-ai-") || name === "x-selected-model-id") headers.delete(name)\n        }\n        return lingdocContinue(headers)\n    }\n\n    // Skip static files'],
      ['        return\n    }\n\n    // Check if there is any supported locale', '        return lingdocContinue()\n    }\n\n    // Check if there is any supported locale'],
      ['    }\n}\n\nexport const config', '    }\n    return lingdocContinue()\n}\n\nexport const config'],
    ],
  },
  {
    file: 'components/chat/chat-engine.tsx',
    changes: [
      ['const MAX_AUTO_RETRY_COUNT = 3', 'const MAX_AUTO_RETRY_COUNT = 0 // LingDoc requires explicit retry.'],
      ['const MAX_CONTINUATION_RETRY_COUNT = 2', 'const MAX_CONTINUATION_RETRY_COUNT = 0 // LingDoc requires explicit continuation.'],
    ],
  },
  {
    file: 'app/api/chat/route.ts',
    changes: [
      ['    const result = streamText({\n        model,', '    const result = streamText({\n        maxRetries: 0, // LingDoc preserves uncertain calls for explicit retry.\n        model,'],
    ],
  },
];

export function patchText(file, original) {
  const patch = patches.find(item => item.file === file);
  if (!patch) throw new Error('Unknown upstream patch target');
  let result = original;
  for (const [anchor, replacement] of patch.changes) {
    if (result.split(anchor).length !== 2) throw new Error(`Pinned upstream anchor changed in ${file}`);
    result = result.replace(anchor, replacement);
  }
  return '// Modified for LingDoc independent drawing integration.\n' + result;
}

export const patchFiles = patches.map(item => item.file);
const overlayFiles = ['components/lingdoc-bridge.tsx', 'lib/lingdoc-boundary.ts', 'app/api/lingdoc-bridge-config/route.ts'];

export async function applyOverlay(directory = sourceDirectory) {
  const upstreamFiles = JSON.parse(await readFile(path.join(here, 'upstream-files.json'), 'utf8'));
  const markerFile = path.join(directory, '.lingdoc-overlay.json');
  let marker;
  try { marker = JSON.parse(await readFile(markerFile, 'utf8')); }
  catch (error) { if (error.code !== 'ENOENT') throw error; }
  if (marker && (marker.commit !== UPSTREAM_COMMIT || marker.schema !== 1)) throw new Error('Unknown drawing overlay marker');
  const pending = [], files = {};
  for (const file of patchFiles) {
    const filename = path.join(directory, file);
    const bytes = await readFile(filename);
    const previous = marker?.files[file];
    if (previous) {
      if (previous.originalGitSha !== upstreamFiles[file]) throw new Error(`Drawing source marker changed in ${file}`);
      if (digest(bytes) === previous.sha256) { files[file] = previous; continue; }
    }
    if (gitDigest(bytes) !== upstreamFiles[file]) throw new Error(`Pinned drawing source differs in ${file}`);
    const patched = Buffer.from(patchText(file, bytes.toString('utf8')));
    files[file] = { originalGitSha: upstreamFiles[file], sha256: digest(patched) };
    pending.push({ file, bytes: patched });
  }
  for (const file of overlayFiles) {
    const bytes = await readFile(path.join(here, 'overlay', file));
    let existing;
    try { existing = await readFile(path.join(directory, file)); }
    catch (error) { if (error.code !== 'ENOENT') throw error; }
    if (existing && digest(existing) !== digest(bytes)) throw new Error(`Drawing overlay differs in ${file}`);
    files[file] = { sha256: digest(bytes) };
    if (!existing) pending.push({ file, bytes });
  }
  // All anchors and existing overlays are checked before the first write.
  // Write the intended hashes first. An interrupted patch can resume only
  // from exact pinned originals or exact already-applied bytes.
  await writeFile(markerFile, JSON.stringify({ schema: 1, commit: UPSTREAM_COMMIT, files }, null, 2));
  for (const item of pending) {
    const destination = path.join(directory, item.file);
    await mkdir(path.dirname(destination), { recursive: true });
    await writeFile(destination, item.bytes, { flag: patchFiles.includes(item.file) ? 'w' : 'wx' });
  }
  return { commit: UPSTREAM_COMMIT, files: Object.keys(files) };
}
