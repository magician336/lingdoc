import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile, writeFile, mkdir, mkdtemp, rm, cp } from 'node:fs/promises';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { createRequire } from 'node:module';
import { createHash } from 'node:crypto';
import { deflateRawSync } from 'node:zlib';
import { applyOverlay, here, patchFiles, patchText } from './patch.mjs';
import { exactLoopbackOrigin, modelEnvironment, startDrawingUpstream } from './launch.mjs';

const fixture = process.env.LINGDOC_DRAWING_TEST_SOURCE || path.join(here, 'runtime', 'contract-fixture');
const runtime = path.join(here, 'runtime');
const require = createRequire(import.meta.url);
let ts;
try { ts = require(path.join(runtime, 'typecheck', 'node_modules', 'typescript')); } catch {}

async function temporary(t) {
  await mkdir(runtime, { recursive: true });
  const directory = await mkdtemp(path.join(runtime, 'test-'));
  t.after(async () => {
    assert.ok(path.resolve(directory).startsWith(path.resolve(runtime) + path.sep));
    await rm(directory, { recursive: true, force: true });
  });
  return directory;
}

test('exact loopback origins and supported model schema fail closed', () => {
  assert.equal(exactLoopbackOrigin('http://localhost:7212'), 'http://localhost:7212');
  for (const origin of ['http://localhost:7212/', 'https://localhost:7212', 'http://localhost.example:7212', 'http://127.0.0.1:7212?q=1', 'http://user@localhost:7212'])
    assert.throws(() => exactLoopbackOrigin(origin));
  const synthetic = { model: 'deepseek-flash', api_key: 'fixture-credential', system_aes_key: '0'.repeat(32) };
  const environment = modelEnvironment(synthetic);
  assert.equal(environment.AI_PROVIDER, 'deepseek');
  assert.equal(environment.DEEPSEEK_BASE_URL, 'https://api.deepseek.com/v1');
  assert.equal(JSON.parse(environment.AI_MODELS_CONFIG).providers[0].models[0], synthetic.model);
  for (const change of [{ model: 'unknown' }, { api_key: 'bad\nkey' }, { api_key: '' }, { base_url: 'https://untrusted.invalid' }, { system_aes_key: 'short' }])
    assert.throws(() => modelEnvironment({ ...synthetic, ...change }));
});

test('missing upstream build provides actionable failure without a model call', async t => {
  if (process.env.LINGDOC_DRAWING_ORIGIN) return t.skip('External drawing origin configured');
  const directory = await temporary(t);
  await assert.rejects(startDrawingUpstream({ parentOrigin: 'http://127.0.0.1:7212', configPath: null, applicationDirectory: directory }), /prepare\.mjs --build/);
});

test('patch applies to exact pinned official sources, resumes, and rejects edits', async t => {
  try { await readFile(path.join(fixture, patchFiles[0])); }
  catch { t.skip('Fetch the pinned official contract sources or set LINGDOC_DRAWING_TEST_SOURCE'); return; }
  const directory = await temporary(t);
  for (const file of patchFiles) {
    const destination = path.join(directory, file);
    await mkdir(path.dirname(destination), { recursive: true });
    await cp(path.join(fixture, file), destination);
  }
  await applyOverlay(directory);
  await applyOverlay(directory);
  const proxy = await readFile(path.join(directory, 'proxy.ts'), 'utf8');
  assert.match(proxy, /lingdocBoundary\(request\)/);
  assert.match(proxy, /headers\.delete\(name\)/);
  // Simulate interruption after the intent marker but before one source write.
  await cp(path.join(fixture, patchFiles[0]), path.join(directory, patchFiles[0]));
  await applyOverlay(directory);
  const filename = path.join(directory, patchFiles[0]);
  await writeFile(filename, (await readFile(filename, 'utf8')) + '\n// local change\n');
  await assert.rejects(applyOverlay(directory), /differs|changes/);
  assert.throws(() => patchText(patchFiles[0], 'different source'), /anchor changed/);
});

test('CRLF checkout is accepted only when normalized bytes match every pinned blob', async t => {
  try { await readFile(path.join(fixture, patchFiles[0])); }
  catch { t.skip('Fetch the pinned official contract sources or set LINGDOC_DRAWING_TEST_SOURCE'); return; }
  const directory = await temporary(t);
  for (const file of patchFiles) {
    const destination = path.join(directory, file);
    await mkdir(path.dirname(destination), { recursive: true });
    const original = (await readFile(path.join(fixture, file), 'utf8')).replace(/\r\n/g, '\n');
    await writeFile(destination, original.replace(/\n/g, '\r\n'));
  }
  await applyOverlay(directory);
  await applyOverlay(directory);
  for (const file of patchFiles) {
    const original = (await readFile(path.join(fixture, file), 'utf8')).replace(/\r\n/g, '\n');
    assert.equal(await readFile(path.join(directory, file), 'utf8'), patchText(file, original));
  }
  const changedDirectory = await temporary(t);
  for (const file of patchFiles) {
    const destination = path.join(changedDirectory, file);
    await mkdir(path.dirname(destination), { recursive: true });
    const original = (await readFile(path.join(fixture, file), 'utf8')).replace(/\r\n/g, '\n');
    await writeFile(destination, original.replace(/\n/g, '\r\n'));
  }
  const changedFile = path.join(changedDirectory, patchFiles.at(-1));
  await writeFile(changedFile, (await readFile(changedFile, 'utf8')) + '// unrelated edit\r\n');
  const untouched = await readFile(path.join(changedDirectory, patchFiles[0]));
  await assert.rejects(applyOverlay(changedDirectory), /Pinned drawing source differs/);
  assert.deepEqual(await readFile(path.join(changedDirectory, patchFiles[0])), untouched);
  await assert.rejects(readFile(path.join(changedDirectory, '.lingdoc-overlay.json')), { code: 'ENOENT' });
});

test('layout overlay uses existing font variables without build-time remote fonts', async t => {
  let original;
  try { original = (await readFile(path.join(fixture, 'app/[lang]/layout.tsx'), 'utf8')).replace(/\r\n/g, '\n'); }
  catch { t.skip('Fetch the pinned official layout contract source'); return; }
  const layout = patchText('app/[lang]/layout.tsx', original);
  assert.doesNotMatch(layout, /next\/font|Instrument_Sans|JetBrains_Mono|instrumentSans|jetbrainsMono/);
  assert.match(layout, /import "\.\.\/lingdoc-fonts\.css"/);
  assert.match(layout, /className="lingdoc-system-fonts antialiased"/);
  const stylesheet = await readFile(path.join(here, 'overlay/app/lingdoc-fonts.css'), 'utf8');
  assert.match(stylesheet, /--font-latin:.*Segoe UI/);
  assert.match(stylesheet, /--font-mono:[\s\S]*Consolas/);
  assert.doesNotMatch(stylesheet, /@import|url\(/);
});

test('recorded overlays update safely, recover interrupted intent, and reject unknown edits', async t => {
  try { await readFile(path.join(fixture, patchFiles[0])); }
  catch { t.skip('Fetch the pinned official contract sources or set LINGDOC_DRAWING_TEST_SOURCE'); return; }
  const directory = await temporary(t);
  for (const file of patchFiles) {
    const destination = path.join(directory, file);
    await mkdir(path.dirname(destination), { recursive: true });
    await cp(path.join(fixture, file), destination);
  }
  await applyOverlay(directory);
  const file = 'lib/lingdoc-boundary.ts';
  const destination = path.join(directory, file);
  const desired = await readFile(path.join(here, 'overlay', file));
  assert.match(desired.toString('utf8'), /frame-ancestors 'self'/);
  const previous = Buffer.from(desired.toString('utf8').replace("frame-ancestors 'self'", 'frame-ancestors'));
  const digest = bytes => createHash('sha256').update(bytes).digest('hex');
  const markerFile = path.join(directory, '.lingdoc-overlay.json');
  const marker = JSON.parse(await readFile(markerFile, 'utf8'));
  marker.files[file] = { sha256: digest(previous) };
  await writeFile(destination, previous);
  await writeFile(markerFile, JSON.stringify(marker));
  await applyOverlay(directory);
  assert.deepEqual(await readFile(destination), desired);
  assert.deepEqual(JSON.parse(await readFile(markerFile, 'utf8')).files[file], { sha256: digest(desired) });
  // Crash after recording intent, before the old owned bytes were replaced.
  marker.files[file] = { sha256: digest(desired), previousSha256: digest(previous) };
  await writeFile(destination, previous);
  await writeFile(markerFile, JSON.stringify(marker));
  await applyOverlay(directory);
  assert.deepEqual(await readFile(destination), desired);
  assert.equal(JSON.parse(await readFile(markerFile, 'utf8')).files[file].previousSha256, undefined);
  await writeFile(destination, Buffer.concat([desired, Buffer.from('\n// user edit\n')]));
  await assert.rejects(applyOverlay(directory), /unrecorded changes/);
  // Even matching desired bytes need an ownership record when already present.
  await writeFile(destination, desired);
  const unrecorded = JSON.parse(await readFile(markerFile, 'utf8'));
  delete unrecorded.files[file];
  await writeFile(markerFile, JSON.stringify(unrecorded));
  await assert.rejects(applyOverlay(directory), /unrecorded changes/);
});

function interfaceMembers(source, name, wanted) {
  const ast = ts.createSourceFile('source.tsx', source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const declaration = ast.statements.find(node => ts.isInterfaceDeclaration(node) && node.name.text === name);
  assert.ok(declaration, `Official interface ${name} exists`);
  const selected = declaration.members.filter(member => wanted.includes(member.name?.getText(ast)));
  assert.equal(selected.length, wanted.length);
  return selected.map(member => member.getText(ast)).join('\n');
}

test('bridge typechecks against member signatures extracted from official hooks', async t => {
  if (!ts) { t.skip('Install TypeScript into runtime/typecheck'); return; }
  let engine, diagram;
  try { engine = await readFile(path.join(fixture, 'components/chat/chat-engine.tsx'), 'utf8'); diagram = await readFile(path.join(fixture, 'contexts/diagram-context.tsx'), 'utf8'); }
  catch { t.skip('Official hook source fixture is unavailable'); return; }
  const engineMembers = interfaceMembers(engine, 'ChatEngine', ['messages', 'isBusy', 'isLeaving', 'setMessages', 'setInput', 'currentTitle', 'isRestored', 'renameSession', 'loadedMessageIdsRef']);
  const diagramMembers = interfaceMembers(diagram, 'DiagramContextType', ['chartXMLRef', 'loadDiagram', 'clearDiagram', 'hasPendingLoad', 'requestExport', 'isDrawioReady']);
  const directory = await temporary(t);
  const contracts = `
declare module "@/components/chat/chat-engine" {
  type ChatUIMessage = {id:string;role:"system"|"user"|"assistant";parts:Array<{type:string;[key:string]:unknown}>};
  interface ChatEngine { ${engineMembers.replaceAll('React.RefObject', 'import("react").RefObject')} }
  export function useChatEngine(): ChatEngine;
}
declare module "@/contexts/diagram-context" {
  type LoadMode = "load"|"preview"|"commit"|"revert";
  type LoadMeta = unknown;
  interface ExportRequest {format:string;[key:string]:unknown}
  interface ExportResult {data?:string;xml?:string}
  interface DiagramContextType { ${diagramMembers.replaceAll('React.MutableRefObject', 'import("react").MutableRefObject')} }
  export function useDiagram(): DiagramContextType;
}
declare module "@/lib/session-storage" {
  export function sanitizeMessages(input:unknown[]): {id:string;role:"system"|"user"|"assistant";parts:Array<{type:string;[key:string]:unknown}>}[];
}
declare module "@/lib/diagram-pages" {
  export function parseXml(xml:string):Document|null;
  export function modelOfPage(page:Element):Element|null;
}
declare module "@/packages/mcp-server/src/pages.ts" {export function hasCells(xml:string):boolean}
declare module "@/stores/ui-store" {export const useUiStore:{getState():{setHeroDismissed(value:boolean):void}}}
declare module "next/server" {
  export interface NextRequest {headers:Headers;method:string;nextUrl:{pathname:string}}
  export class NextResponse extends Response {static next(options?:{request:{headers:Headers}}):NextResponse; static json(value:unknown, options?:ResponseInit):NextResponse}
}`;
  const contractFile = path.join(directory, 'contracts.d.ts');
  await writeFile(contractFile, contracts);
  const overlay = ['components/lingdoc-bridge.tsx', 'lib/lingdoc-boundary.ts', 'app/api/lingdoc-bridge-config/route.ts'].map(file => path.join(here, 'overlay', file));
  const program = ts.createProgram([...overlay, contractFile], {
    strict: true, noEmit: true, target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext,
    moduleResolution: ts.ModuleResolutionKind.Bundler, jsx: ts.JsxEmit.ReactJSX,
    typeRoots: [path.join(runtime, 'typecheck/node_modules/@types')],
    paths: { react: [path.join(runtime, 'typecheck/node_modules/@types/react/index.d.ts')] },
  });
  const diagnostics = ts.getPreEmitDiagnostics(program);
  assert.equal(diagnostics.length, 0, ts.formatDiagnosticsWithColorAndContext(diagnostics, { getCanonicalFileName: value => value, getCurrentDirectory: () => directory, getNewLine: () => '\n' }));
});

async function mountBridge(options = {}) {
  const sent = [], handlers = new Map(), exports = [];
  const xml = options.xml ?? '<mxfile><diagram id="a"/><diagram id="b"/></mxfile>';
  const parent = { postMessage: (message, origin) => sent.push({ message, origin }) };
  const engine = { isRestored: true, isBusy: false, isLeaving: false, messages: [], currentTitle: '图稿', loadedMessageIdsRef: { current: new Set() },
    setInput: value => { engine.input = value; }, setMessages: value => { engine.messages = value; }, renameSession: async () => {} };
  const diagram = { isDrawioReady: true, hasPendingLoad: () => false, clearDiagram: () => {},
    loadDiagram: value => { diagram.loaded = value; return null; },
    requestExport: async request => { exports.push(request); return request.format === 'xml' ? { xml } : { data: 'data:image/png;base64,fixture', xml: '<current-page-only/>' }; } };
  const context = {
    exports: {}, TextEncoder, URL, AbortController, setTimeout, XMLSerializer: options.helpers?.XMLSerializer,
    window: { parent, addEventListener: (name, fn) => handlers.set(name, fn), removeEventListener: name => handlers.delete(name) },
    fetch: async () => ({ ok: true, json: async () => ({ parentOrigins: ['http://127.0.0.1:7212', 'http://localhost:7212'] }) }),
    require: name => ({
      react: { useRef: current => ({ current }), useEffect: callback => callback() },
      '@/components/chat/chat-engine': { useChatEngine: () => engine },
      '@/contexts/diagram-context': { useDiagram: () => diagram },
      '@/lib/session-storage': { sanitizeMessages: messages => messages.filter(message => message?.id && message?.role && Array.isArray(message.parts)) },
      '@/lib/diagram-pages': options.helpers?.diagramPages ?? { parseXml: () => null, modelOfPage: () => null },
      '@/packages/mcp-server/src/pages.ts': options.helpers?.pages ?? { hasCells: () => true },
      '@/stores/ui-store': { useUiStore: { getState: () => ({ setHeroDismissed: () => {} }) } },
    })[name],
  };
  const source = await readFile(path.join(here, 'overlay/components/lingdoc-bridge.tsx'), 'utf8');
  const js = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
  vm.runInNewContext(js, context);
  context.exports.LingDocBridge();
  await new Promise(resolve => setImmediate(resolve));
  const send = (data, origin = 'http://localhost:7212', source = parent) => handlers.get('message')({ data: { channel: 'lingdoc-drawing', ...data }, origin, source });
  return { sent, exports, engine, diagram, send, xml };
}

test('real bridge rejects foreign windows and restores, prefills, exports full multipage XML', async t => {
  if (!ts) { t.skip('Install TypeScript into runtime/typecheck'); return; }
  const bridge = await mountBridge();
  assert.equal(bridge.sent.filter(item => item.message.type === 'ready').length, 2);
  await bridge.send({ type: 'load', requestId: 'bad', xml: 'bad' }, 'http://evil.invalid');
  await bridge.send({ type: 'load', requestId: 'bad', xml: 'bad' }, 'http://localhost:7212', {});
  assert.equal(bridge.diagram.loaded, undefined);
  await bridge.send({ type: 'load', requestId: 'load-1', xml: bridge.xml, messages: [] });
  assert.equal(bridge.diagram.loaded, bridge.xml);
  await bridge.send({ type: 'prompt', requestId: 'prompt-1', prompt: '中'.repeat(12500) });
  assert.equal(bridge.engine.input.length, 12500);
  assert.equal(bridge.diagram.loaded, bridge.xml);
  await bridge.send({ type: 'export', requestId: 'save-1' });
  const result = bridge.sent.find(item => item.message.requestId === 'save-1').message;
  assert.equal(result.xml, bridge.xml);
  assert.match(result.png, /^data:image\/png;base64,/);
  assert.equal(bridge.exports.filter(item => item.format === 'xml').length, 2);
  bridge.engine.isBusy = true;
  await bridge.send({ type: 'export', requestId: 'busy-1' });
  assert.equal(bridge.sent.at(-1).message.type, 'error');
  bridge.engine.isBusy = false;
  await bridge.send({ type: 'load', requestId: 'bytes-1', xml: '中'.repeat(400000), messages: [] });
  assert.equal(bridge.sent.at(-1).message.type, 'error');
  let renamed = false;
  bridge.diagram.loadDiagram = () => 'Invalid XML';
  bridge.engine.renameSession = async () => { renamed = true; };
  await bridge.send({ type: 'load', requestId: 'invalid-xml', xml: '<invalid/>', messages: [], title: 'Do not rename' });
  assert.equal(bridge.sent.at(-1).message.type, 'error');
  assert.equal(renamed, false);
});

test('restore acknowledgement waits for the actual exposed message snapshot after rename', async t => {
  if (!ts) { t.skip('Install TypeScript into runtime/typecheck'); return; }
  for (const withTitle of [false, true]) {
    const bridge = await mountBridge();
    const previous = [{ id: 'drawing-a', role: 'user', parts: [{ type: 'text', text: 'Drawing A history' }] }];
    const desired = [...previous, { id: 'edited-message', role: 'user', parts: [{ type: 'text', text: 'Unsaved B changes' }] }];
    bridge.engine.messages = previous;
    let queuedMessages, completeRename;
    const renamed = new Promise(resolve => { completeRename = resolve; });
    // The SDK updates its internal chat immediately but publishes the hook's
    // exposed snapshot through a throttled useSyncExternalStore subscription.
    bridge.engine.setMessages = value => { queuedMessages = value; };
    bridge.engine.renameSession = async () => { await renamed; bridge.engine.messages = previous; };
    const loading = bridge.send({ type: 'load', requestId: 'restore-b', xml: bridge.xml,
      messages: desired, ...(withTitle ? { title: 'B edited' } : {}) });
    completeRename();
    await new Promise(resolve => setImmediate(resolve));
    try {
      assert.equal(bridge.sent.some(item => item.message.requestId === 'restore-b'), false,
        'load must not acknowledge while the hook still exposes drawing A messages');
    } finally {
      bridge.engine.messages = queuedMessages ?? desired;
      await loading;
    }
    assert.equal(bridge.sent.at(-1).message.loaded, true);
    assert.deepEqual([...bridge.engine.loadedMessageIdsRef.current], desired.map(message => message.id));
    await bridge.send({ type: 'export', requestId: 'export-restored-b' });
    assert.deepEqual(bridge.sent.at(-1).message.messages, desired);
  }
});

function officialDiagramHelpers() {
  const upstream = path.join(runtime, 'source');
  const upstreamRequire = createRequire(path.join(upstream, 'package.json'));
  const { JSDOM } = upstreamRequire('jsdom');
  const { DOMParser, XMLSerializer } = new JSDOM('').window;
  const cache = new Map();
  function load(filename) {
    if (cache.has(filename)) return cache.get(filename).exports;
    const module = { exports: {} };
    cache.set(filename, module);
    const source = readFileSync(filename, 'utf8');
    const js = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
    vm.runInNewContext(js, {
      module, exports: module.exports, DOMParser, XMLSerializer, atob, Uint8Array,
      require: specifier => {
        if (specifier.startsWith('@/') || specifier.startsWith('.')) {
          let target = specifier.startsWith('@/') ? path.join(upstream, specifier.slice(2)) : path.resolve(path.dirname(filename), specifier);
          if (!target.endsWith('.ts')) target += '.ts';
          return load(target);
        }
        return upstreamRequire(specifier);
      },
    });
    return module.exports;
  }
  return {
    diagramPages: load(path.join(upstream, 'lib/diagram-pages.ts')),
    pages: load(path.join(upstream, 'packages/mcp-server/src/pages.ts')),
    XMLSerializer,
  };
}

test('skipEmpty proves every decoded page empty, preserves messages and uncertain diagrams', async t => {
  if (!ts) { t.skip('Install TypeScript into runtime/typecheck'); return; }
  let helpers;
  try { helpers = officialDiagramHelpers(); }
  catch (error) {
    if (error.code === 'MODULE_NOT_FOUND' || error.code === 'ENOENT') {
      t.skip('Install the pinned upstream dependencies to test official XML and compressed-page helpers'); return;
    }
    throw error;
  }
  const blank = '<mxGraphModel><root><mxCell id="0"/><mxCell id="1" parent="0"/></root></mxGraphModel>';
  const drawn = blank.replace('</root>', '<mxCell id="2" vertex="1" parent="1"><mxGeometry width="80" height="30"/></mxCell></root>');
  const compressed = xml => deflateRawSync(Buffer.from(encodeURIComponent(xml))).toString('base64');
  for (const xml of [blank, `<mxfile><diagram>${blank}</diagram></mxfile>`,
    `<mxfile><diagram>${compressed(blank)}</diagram><diagram>${compressed(blank)}</diagram></mxfile>`]) {
    const bridge = await mountBridge({ xml, helpers });
    await bridge.send({ type: 'export', requestId: 'empty', skipEmpty: true });
    assert.equal(bridge.sent.at(-1).message.empty, true);
    assert.equal(bridge.exports.length, 1);
    assert.equal(bridge.exports[0].format, 'xml');
  }
  for (const xml of [drawn, '<mxfile><diagram>undecodable</diagram></mxfile>',
    '<mxGraphModel><root/></mxGraphModel>', '<mxfile>',
    blank.replace('<mxGraphModel>', '<mxGraphModel backgroundImage="image-data">'),
    `<mxfile><diagram>${blank}</diagram><diagram>${compressed(drawn)}</diagram></mxfile>`]) {
    const bridge = await mountBridge({ xml, helpers });
    await bridge.send({ type: 'export', requestId: 'preserve', skipEmpty: true });
    assert.equal(bridge.sent.at(-1).message.empty, undefined);
    assert.match(bridge.sent.at(-1).message.png, /^data:image\/png;base64,/);
  }
  const withMessages = await mountBridge({ xml: blank, helpers });
  withMessages.engine.messages = [{ id: 'question', role: 'user', parts: [{ type: 'text', text: '保留会话' }] }];
  await withMessages.send({ type: 'export', requestId: 'messages', skipEmpty: true });
  assert.match(withMessages.sent.at(-1).message.png, /^data:image\/png;base64,/);
  const clearedExisting = await mountBridge({ xml: blank, helpers });
  await clearedExisting.send({ type: 'export', requestId: 'clear-existing' });
  assert.match(clearedExisting.sent.at(-1).message.png, /^data:image\/png;base64,/);
  const busy = await mountBridge({ xml: blank, helpers });
  busy.engine.isBusy = true;
  await busy.send({ type: 'export', requestId: 'busy-empty', skipEmpty: true });
  assert.equal(busy.sent.at(-1).message.type, 'error');
  assert.equal(busy.exports.length, 0);
  const startedDuringExport = await mountBridge({ xml: blank, helpers });
  const exportBeforeBusy = startedDuringExport.diagram.requestExport;
  startedDuringExport.diagram.requestExport = async request => {
    const result = await exportBeforeBusy(request);
    startedDuringExport.engine.isBusy = true;
    return result;
  };
  await startedDuringExport.send({ type: 'export', requestId: 'started-during-export', skipEmpty: true });
  assert.equal(startedDuringExport.sent.at(-1).message.type, 'error');
  assert.equal(startedDuringExport.sent.at(-1).message.empty, undefined);
  assert.equal(startedDuringExport.exports.length, 1);
  const messageDuringExport = await mountBridge({ xml: blank, helpers });
  const exportBeforeMessage = messageDuringExport.diagram.requestExport;
  messageDuringExport.diagram.requestExport = async request => {
    const result = await exportBeforeMessage(request);
    messageDuringExport.engine.messages = [{ id: 'new-message', role: 'user', parts: [{ type: 'text', text: '导出期间的新消息' }] }];
    return result;
  };
  await messageDuringExport.send({ type: 'export', requestId: 'message-during-export', skipEmpty: true });
  assert.match(messageDuringExport.sent.at(-1).message.png, /^data:image\/png;base64,/);
  assert.equal(messageDuringExport.sent.at(-1).message.empty, undefined);
  busy.engine.isBusy = false;
  busy.diagram.hasPendingLoad = () => true;
  await busy.send({ type: 'export', requestId: 'loading-empty', skipEmpty: true });
  assert.equal(busy.sent.at(-1).message.type, 'error');
  assert.equal(busy.exports.length, 0);
});
