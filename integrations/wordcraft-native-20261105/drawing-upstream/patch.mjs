import { readFile, writeFile, mkdir, rename, unlink } from 'node:fs/promises';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash, randomUUID } from 'node:crypto';

export const UPSTREAM_COMMIT = '04b1de949f9f2f14971bf2e26f4a7fd8cc392626';
export const UPSTREAM_REPOSITORY = 'https://github.com/DayuanJiang/next-ai-draw-io.git';
export const here = path.dirname(fileURLToPath(import.meta.url));
export const sourceDirectory = path.join(here, 'runtime', 'source');
const digest = bytes => createHash('sha256').update(bytes).digest('hex');
const gitDigest = bytes => createHash('sha1').update(`blob ${bytes.length}\0`).update(bytes).digest('hex');

async function atomicWrite(filename, bytes) {
  const temporary = filename + '.' + randomUUID() + '.tmp';
  try {
    await writeFile(temporary, bytes, { flag: 'wx' });
    await rename(temporary, filename);
  } catch (error) {
    await unlink(temporary).catch(() => {});
    throw error;
  }
}

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
  {
    file: 'app/[lang]/layout.tsx',
    changes: [
      ['import { Instrument_Sans, JetBrains_Mono } from "next/font/google"\n', ''],
      ['import "../globals.css"', 'import "../globals.css"\nimport "../lingdoc-fonts.css"'],
      ['const instrumentSans = Instrument_Sans({\n    variable: "--font-latin",\n    subsets: ["latin"],\n    weight: ["400", "500", "600", "700"],\n    // Chinese and Japanese text falls through to system fonts, in the\n    // order globals.css picks for the page\'s language\n    fallback: [],\n})\n\nconst jetbrainsMono = JetBrains_Mono({\n    variable: "--font-mono",\n    subsets: ["latin"],\n    weight: ["400", "500"],\n})\n\n', ''],
      ['className={`${instrumentSans.variable} ${jetbrainsMono.variable} antialiased`}', 'className="lingdoc-system-fonts antialiased"'],
    ],
  },
  {
    file: 'stores/ui-store.ts',
    changes: [
      ['    heroDismissed: false,\n    mobileView: "chat",', '    heroDismissed: true, // LingDoc opens directly on the drawing canvas.\n    mobileView: "canvas",'],
      ['    setHeroDismissed: (dismissed) => set({ heroDismissed: dismissed }),', '    setHeroDismissed: (_dismissed) => set({ heroDismissed: true }), // LingDoc entry policy also applies to New Chat.'],
    ],
  },
  {
    file: 'lib/drawio/editor-bridge.ts',
    changes: [
      ['export function refitIfRecent(windowMs = 1500) {', 'export function refitIfRecent(windowMs = 1500) {\n    if (centerEmptyPage()) return true'],
      ['    if (!graph() || isEmptyModel()) return\n    lastOpenedAt', '    if (!graph()) return\n    if (isEmptyModel()) { centerEmptyPage(); return }\n    lastOpenedAt'],
      [`export function centerEmptyPage(): boolean {
    if (!ui || !isEmptyModel() || typeof ui.resetScrollbars !== "function") {
        return false
    }
    try {
        ui.resetScrollbars()
        return true
    } catch {
        return false
    }
}`, `export function centerEmptyPage(): boolean {
    const g = graph()
    if (!ui || !g || g.pageVisible === false || !isEmptyModel() || typeof ui.resetScrollbars !== "function") {
        return false
    }
    const previousFitting = fitting
    fitting = true
    try {
        // LingDoc fits an empty sheet to the available editor viewport.
        // Shapes keep the upstream view and manual zoom behavior.
        const pageWidth = g.pageFormat?.width * (g.pageScale ?? 1)
        const pageHeight = g.pageFormat?.height * (g.pageScale ?? 1)
        const width = g.container?.clientWidth - 32
        const height = g.container?.clientHeight - 32
        const scale = Math.min(1, width / pageWidth, height / pageHeight)
        if (pageWidth > 0 && pageHeight > 0 && width > 0 && height > 0 &&
            Number.isFinite(scale) && scale > 0 && typeof g.zoomTo === "function" &&
            Math.abs(g.view.scale - scale) > 0.0001) g.zoomTo(scale, true)
        ui.resetScrollbars()
        const bounds = g.view.getBackgroundPageBounds?.()
        if (bounds && bounds.width > 0 && bounds.height > 0 &&
            [bounds.x, bounds.y, bounds.width, bounds.height].every(Number.isFinite)) {
            g.container.scrollLeft = Math.max(0, bounds.x - (g.container.clientWidth - bounds.width) / 2)
            g.container.scrollTop = Math.max(0, bounds.y - (g.container.clientHeight - bounds.height) / 2)
        }
        return true
    } catch {
        return false
    } finally {
        fitting = previousFitting
    }
}`],
    ],
  },
];

// Keep the existing per-session delete affordance visible without hover.
patches.push({
  file:'components/chat/session-menu.tsx',
  changes:[['text-faint [@media(hover:hover)]:opacity-0 hover:bg-destructive/10 hover:text-destructive group-hover/session:opacity-100 focus-visible:opacity-100',
            'text-faint hover:bg-destructive/10 hover:text-destructive focus-visible:opacity-100']],
});

// These tracked replacement overlays are based on exactly pinned upstream blobs.
for (const file of ['components/canvas/version-strip.tsx', 'components/canvas/versions-context.tsx',
  'components/chat/chat-versions.tsx', 'stores/versions-store.ts']) patches.push({file, replacement:true});

export function patchText(file, original) {
  const patch = patches.find(item => item.file === file);
  if (!patch) throw new Error('Unknown upstream patch target');
  if (patch.replacement) {
    const expected = JSON.parse(readFileSync(path.join(here, 'upstream-files.json'), 'utf8'))[file];
    if (gitDigest(Buffer.from(original)) !== expected) throw new Error(`Pinned drawing source differs in ${file}`);
    return '// Modified for LingDoc independent drawing integration.\n' + readFileSync(path.join(here, 'overlay', file), 'utf8');
  }
  let result = original;
  for (const [anchor, replacement] of patch.changes) {
    if (result.split(anchor).length !== 2) throw new Error(`Pinned upstream anchor changed in ${file}`);
    result = result.replace(anchor, replacement);
  }
  return '// Modified for LingDoc independent drawing integration.\n' + result;
}

export const patchFiles = patches.map(item => item.file);
const overlayFiles = ['tests/unit/lingdoc-version-delete.test.tsx', 'components/lingdoc-bridge.tsx', 'lib/lingdoc-boundary.ts', 'app/api/lingdoc-bridge-config/route.ts', 'app/lingdoc-fonts.css'];

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
      if (digest(bytes) === previous.sha256) {
        const patch = patches.find(item => item.file === file);
        if (patch?.replacement) {
          const desired = Buffer.from('// Modified for LingDoc independent drawing integration.\n' + await readFile(path.join(here, 'overlay', file), 'utf8'));
          const hash = digest(desired);
          if (hash !== previous.sha256) {
            files[file] = {originalGitSha:upstreamFiles[file], sha256:hash, previousSha256:previous.sha256};
            pending.push({file, bytes:desired, replace:true});
          } else files[file] = previous;
        } else files[file] = previous;
        continue;
      }
      if (previous.previousSha256 && digest(bytes) === previous.previousSha256) {
        const patch = patches.find(item => item.file === file);
        if (!patch?.replacement) throw new Error(`Drawing overlay has unrecorded changes in ${file}`);
        const desired = Buffer.from('// Modified for LingDoc independent drawing integration.\n' + await readFile(path.join(here, 'overlay', file), 'utf8'));
        if (digest(desired) !== previous.sha256) throw new Error(`Drawing overlay changed during update in ${file}`);
        files[file] = previous; pending.push({file, bytes:desired, replace:true}); continue;
      }
    }
    // Windows checkouts may expand LF to CRLF. Accept that transformation
    // only when reversing it reproduces the exact pinned Git blob.
    const original = Buffer.from(bytes.toString('utf8').replace(/\r\n/g, '\n'));
    if (gitDigest(original) !== upstreamFiles[file]) throw new Error(`Pinned drawing source differs in ${file}`);
    const patched = Buffer.from(patchText(file, original.toString('utf8')));
    files[file] = { originalGitSha: upstreamFiles[file], sha256: digest(patched) };
    pending.push({ file, bytes: patched });
  }
  for (const file of overlayFiles) {
    const bytes = await readFile(path.join(here, 'overlay', file));
    let existing;
    try { existing = await readFile(path.join(directory, file)); }
    catch (error) { if (error.code !== 'ENOENT') throw error; }
    const desiredHash = digest(bytes);
    const previous = marker?.files[file];
    if (existing) {
      const existingHash = digest(existing);
      if (!previous || ![previous.sha256, previous.previousSha256].includes(existingHash))
        throw new Error(`Drawing overlay has unrecorded changes in ${file}`);
      if (existingHash !== desiredHash) {
        files[file] = { sha256: desiredHash, previousSha256: existingHash };
        pending.push({ file, bytes, replace: true });
      } else files[file] = { sha256: desiredHash };
    } else {
      files[file] = { sha256: desiredHash };
      pending.push({ file, bytes });
    }
  }
  // All anchors and existing overlays are checked before the first write.
  // Write the intended hashes first. An interrupted patch can resume only
  // from exact pinned originals or exact already-applied bytes.
  await atomicWrite(markerFile, JSON.stringify({ schema: 1, commit: UPSTREAM_COMMIT, files }, null, 2));
  for (const item of pending) {
    const destination = path.join(directory, item.file);
    await mkdir(path.dirname(destination), { recursive: true });
    if (item.replace || patchFiles.includes(item.file)) await atomicWrite(destination, item.bytes);
    else await writeFile(destination, item.bytes, { flag: 'wx' });
  }
  // Old overlay hashes are accepted only during an interrupted update.
  // Retire them after every planned replacement has completed.
  for (const value of Object.values(files)) delete value.previousSha256;
  await atomicWrite(markerFile, JSON.stringify({ schema: 1, commit: UPSTREAM_COMMIT, files }, null, 2));
  return { commit: UPSTREAM_COMMIT, files: Object.keys(files) };
}
