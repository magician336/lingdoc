import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile, writeFile, mkdir, mkdtemp, rm, cp } from 'node:fs/promises';
import path from 'node:path';
import vm from 'node:vm';
import { createRequire } from 'node:module';
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

test('missing upstream build provides actionable failure without a model call', async () => {
  if (process.env.LINGDOC_DRAWING_ORIGIN) return;
  await assert.rejects(startDrawingUpstream({ parentOrigin: 'http://127.0.0.1:7212' }), /prepare\.mjs --build/);
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
  const engineMembers = interfaceMembers(engine, 'ChatEngine', ['messages', 'isBusy', 'isLeaving', 'setMessages', 'setInput', 'currentTitle', 'isRestored', 'renameSession']);
  const diagramMembers = interfaceMembers(diagram, 'DiagramContextType', ['chartXMLRef', 'loadDiagram', 'clearDiagram', 'hasPendingLoad', 'requestExport', 'isDrawioReady']);
  const directory = await temporary(t);
  const contracts = `
declare module "@/components/chat/chat-engine" {
  type ChatUIMessage = {id:string;role:"system"|"user"|"assistant";parts:Array<{type:string;[key:string]:unknown}>};
  interface ChatEngine { ${engineMembers} }
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

async function mountBridge() {
  const sent = [], handlers = new Map(), exports = [];
  const xml = '<mxfile><diagram id="a"/><diagram id="b"/></mxfile>';
  const parent = { postMessage: (message, origin) => sent.push({ message, origin }) };
  const engine = { isRestored: true, isBusy: false, isLeaving: false, messages: [], currentTitle: '图稿',
    setInput: value => { engine.input = value; }, setMessages: value => { engine.messages = value; }, renameSession: async () => {} };
  const diagram = { isDrawioReady: true, hasPendingLoad: () => false, clearDiagram: () => {},
    loadDiagram: value => { diagram.loaded = value; return null; },
    requestExport: async request => { exports.push(request); return request.format === 'xml' ? { xml } : { data: 'data:image/png;base64,fixture', xml: '<current-page-only/>' }; } };
  const context = {
    exports: {}, TextEncoder, URL, AbortController,
    window: { parent, addEventListener: (name, fn) => handlers.set(name, fn), removeEventListener: name => handlers.delete(name) },
    fetch: async () => ({ ok: true, json: async () => ({ parentOrigins: ['http://127.0.0.1:7212', 'http://localhost:7212'] }) }),
    require: name => ({
      react: { useRef: current => ({ current }), useEffect: callback => callback() },
      '@/components/chat/chat-engine': { useChatEngine: () => engine },
      '@/contexts/diagram-context': { useDiagram: () => diagram },
      '@/lib/session-storage': { sanitizeMessages: messages => messages.filter(message => message?.id && message?.role && Array.isArray(message.parts)) },
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
});
