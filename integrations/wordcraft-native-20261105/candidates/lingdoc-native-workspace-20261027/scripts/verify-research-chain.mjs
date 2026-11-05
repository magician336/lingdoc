// Real file -> production material/G2 services -> WordCraft -> DOCX/SQLite.
// All data, processes and first-failure evidence belong to this invocation.
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { createServer } from 'node:net';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import path from 'node:path';
import { startDeliveryRuntime } from './delivery-runtime.mjs';
import { root, edge } from './local-environment.mjs';

const runId = new Date().toISOString().replaceAll(':', '-') + '-' + process.pid;
const artifacts = path.join(root, 'artifacts', 'research-chain', runId);
const runtimeDir = path.join(root, 'runtime', 'research-check', runId);
const result = { runId, kind: 'real upload/G2/SQLite/DS/WASM/Edge', passed: false, pageErrors: [], httpFailures: [], modelTurnRequests: 0 };
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
let runtime, browser, socket, send;
async function freePort() {
  const server = createServer();
  await new Promise((resolve, reject) => server.once('error', reject).listen(0, '127.0.0.1', resolve));
  const port = server.address().port;
  await new Promise(resolve => server.close(resolve)); return port;
}
try {
  await mkdir(artifacts, { recursive: true });
  const sourceFile = process.env.LINGDOC_RESEARCH_SOURCE_FILE ? path.resolve(process.env.LINGDOC_RESEARCH_SOURCE_FILE) : path.join(artifacts, 'source-example.md');
  if (!process.env.LINGDOC_RESEARCH_SOURCE_FILE) await writeFile(sourceFile, '# LingDoc 本地接入试用资料\n\nLingDoc 资料引用试用：一条可追溯的引用需要保留原文片段、来源标识以及证据卡版本。保存后应能重新打开原文核对。\n\n这份文件仅用于软件功能验证，不是论文或研究证据。\n', 'utf8');
  const originalBytes = await readFile(sourceFile);
  result.sourceFile = { file: sourceFile, sha256: createHash('sha256').update(originalBytes).digest('hex') };
  runtime = await startDeliveryRuntime({ runtimeDir, research: true });
  result.runtime = { origin: runtime.origin, goOrigin: runtime.goOrigin, database: runtime.database, logs: runtime.logs };
  const debugPort = await freePort();
  browser = spawn(edge, ['--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    '--disable-background-timer-throttling', '--disable-backgrounding-occluded-windows', '--disable-renderer-backgrounding',
    '--remote-debugging-port=' + debugPort, '--user-data-dir=' + path.join(runtimeDir, 'edge'), 'about:blank'],
  { windowsHide: true, stdio: ['ignore', 'ignore', 'pipe'] });
  let launchError;
  browser.once('error', error => { launchError = error; }); browser.stderr.resume();
  let page;
  for (let i = 0; i < 100 && !page; i++) {
    if (launchError) throw launchError;
    try { page = (await fetch('http://127.0.0.1:' + debugPort + '/json/list').then(r => r.json())).find(item => item.type === 'page'); } catch {}
    if (!page) await sleep(150);
  }
  assert(page, 'Edge did not expose a page');
  socket = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  let sequence = 0;
  const pending = new Map();
  socket.onmessage = event => {
    const message = JSON.parse(event.data);
    if (message.method === 'Runtime.exceptionThrown') result.pageErrors.push(message.params.exceptionDetails);
    if (message.method === 'Network.requestWillBeSent' && message.params.request.method === 'POST' && /\/agent-sessions\/[^/]+\/turns$/.test(message.params.request.url)) result.modelTurnRequests++;
    if (message.method === 'Network.responseReceived' && message.params.response.status >= 400) {
      result.httpFailures.push({ requestId: message.params.requestId, url: message.params.response.url, status: message.params.response.status });
    }
    if (message.method === 'Network.loadingFinished') {
      const failure = result.httpFailures.find(item => item.requestId === message.params.requestId);
      if (failure) send('Network.getResponseBody', { requestId: failure.requestId }).then(reply => { failure.body = reply.body; }).catch(() => {});
    }
    const request = pending.get(message.id);
    if (!request) return;
    pending.delete(message.id); clearTimeout(request.timer);
    if (message.error) request.reject(new Error(message.error.message)); else request.resolve(message.result);
  };
  send = (method, params = {}) => new Promise((resolve, reject) => {
    const id = ++sequence;
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(method + ' timed out')); }, 60000);
    pending.set(id, { resolve, reject, timer }); socket.send(JSON.stringify({ id, method, params }));
  });
  const evaluate = async expression => {
    const reply = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (reply.exceptionDetails) throw new Error(reply.exceptionDetails.exception?.description || reply.exceptionDetails.text);
    return reply.result.value;
  };
  const waitFor = async expression => {
    for (let i = 0; i < 400; i++) {
      if (await evaluate(expression)) return;
      const error = await evaluate("document.querySelector('#research-status.error')?.textContent");
      if (error) throw new Error(error);
      await sleep(100);
    }
    throw new Error('Timed out: ' + expression + '; ' + await evaluate("document.querySelector('#host-log')?.textContent"));
  };
  const click = async selector => {
    const point = await evaluate("(()=>{const b=document.querySelector(" + JSON.stringify(selector) + ");if(!b||b.disabled)throw Error('button unavailable');b.scrollIntoView({block:'center'});const r=b.getBoundingClientRect();return{x:r.x+r.width/2,y:r.y+r.height/2}})()");
    await send('Input.dispatchMouseEvent', { type: 'mousePressed', button: 'left', clickCount: 1, ...point });
    await send('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', clickCount: 1, ...point });
  };
  const screenshot = async name => {
    const shot = await send('Page.captureScreenshot', { format: 'png' });
    await writeFile(path.join(artifacts, name), Buffer.from(shot.data, 'base64'));
  };
  const editorKey = async (key, code, vk) => {
    // Clicking the actual document also gives egui keyboard focus. DOM focus()
    // alone leaves its internal focus on the side panel's previous control.
    const point = await evaluate("(()=>{const r=document.querySelector('#wordcraft_canvas').getBoundingClientRect();return{x:r.x+r.width/2,y:r.y+Math.max(250,r.height*.7)}})()");
    await send('Input.dispatchMouseEvent', { type: 'mouseMoved', ...point });
    await send('Input.dispatchMouseEvent', { type: 'mousePressed', button: 'left', clickCount: 1, ...point });
    await send('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', clickCount: 1, ...point });
    await sleep(100);
    await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Control', code: 'ControlLeft', modifiers: 2 });
    await send('Input.dispatchKeyEvent', { type: 'keyDown', key, code, windowsVirtualKeyCode: vk, modifiers: 2 });
    await send('Input.dispatchKeyEvent', { type: 'keyUp', key, code, windowsVirtualKeyCode: vk, modifiers: 2 });
    await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Control', code: 'ControlLeft', modifiers: 0 });
  };
  const navigate = async () => {
    await send('Page.navigate', { url: runtime.origin });
    await waitFor("window.WORDCRAFT_HOST_CONFIG?.origin===" + JSON.stringify(runtime.origin));
    await waitFor("(()=>{try{return !!JSON.parse(document.querySelector('#host-log').textContent).opened}catch{return false}})()");
    await click('#research-toggle');
    await waitFor("document.querySelector('#research-panel').getAttribute('aria-busy')!=='true' && document.querySelector('#research-chapter').options.length===6");
  };
  await send('Page.enable'); await send('Runtime.enable'); await send('Network.enable');
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: 1, mobile: false });
  await navigate();
  await evaluate("(()=>{window.__selectionReads=0;const original=window.wasmBindings.host_request;window.__originalHostRequest=original;window.wasmBindings.host_request=request=>{if(JSON.parse(request).op==='selection')window.__selectionReads++;return original(request)}})()");
  const doc = await send('DOM.getDocument');
  const fileNode = await send('DOM.querySelector', { nodeId: doc.root.nodeId, selector: '#research-file' });
  await send('DOM.setFileInputFiles', { nodeId: fileNode.nodeId, files: [sourceFile] });
  await waitFor("document.querySelector('#research-status').textContent.includes('已入库，待选择引用片段')");
  await evaluate("document.querySelector('#research-query').value='LingDoc'");
  await click('#research-search');
  await waitFor("document.querySelectorAll('#research-hits button').length>0");
  await click('#research-hits button');
  await waitFor("document.querySelector('#research-source').value.length>0");
  await evaluate("(()=>{const s=document.querySelector('#research-source');s.focus();s.setSelectionRange(0,s.value.length);document.querySelector('#research-claim').value='资料引用需要保留原文、来源标识和证据卡版本。';document.querySelector('#research-scope').value='仅用于本地接入试用说明';document.querySelector('#research-limits').value='这是功能试用资料，不是学术证据。';})()");
  await click('#research-reviewed'); await click('#research-create');
  await waitFor("document.querySelector('#research-status').textContent.includes('候选证据卡已保存')");
  await evaluate("document.querySelectorAll('#research-cards article button')[1].id='verify-confirm-card'");
  await click('#verify-confirm-card');
  await waitFor("document.querySelector('#research-status').textContent.includes('证据卡已确认')");
  await evaluate("document.querySelectorAll('#research-cards article button')[1].id='verify-insert-card'");
  await click('#verify-insert-card');
  await waitFor("document.querySelector('#research-status').textContent.includes('尚未保存')");
  await click('#research-save');
  await waitFor("window.wordcraftHost.lastSave?.version?.citations?.length>0");
  result.saved = await evaluate("window.wordcraftHost.lastSave");
  assert.equal(result.saved.version.docSha256, result.saved.receipt.sha256);
  assert.equal(result.saved.live.dirty, false);
  assert.equal(result.saved.version.citations.length, 1);
  assert(result.saved.version.projection.sections.some(section => section.bodyText.includes('[[source:')));
  const selectionReadsBeforeIdle = await evaluate("window.__selectionReads");
  await sleep(600);
  assert.equal(await evaluate("window.__selectionReads"), selectionReadsBeforeIdle, 'Evidence flow must not poll editor selection');
  assert.equal(result.modelTurnRequests, 0, 'Evidence flow must not create a chat turn');
  assert(await evaluate("document.body.dataset.side==='left' && document.querySelectorAll('#assistant-messages .assistant-turn').length===0 && document.querySelectorAll('#research-ai-answers article').length===0"));
  result.independentEvidence = { modelTurnRequests: 0, noAutomaticChatNavigation: true, backgroundSelectionReads: 0 };
  await screenshot('saved-citation.png');
  // Undo/save/redo/save must derive the ledger from actual DOCX content, while
  // retaining the card mapping for undo history.
  const beforeUndo = await evaluate("window.wordcraftHost.engine({op:'status'})");
  await editorKey('z', 'KeyZ', 90);
  await waitFor("(async()=>{const s=await window.wordcraftHost.engine({op:'status'});return s.revision!==" + beforeUndo.revision + "})()");
  await evaluate("window.wordcraftHost.saveIntent('verify-undo')");
  result.afterUndo = await evaluate("window.wordcraftHost.lastSave.version");
  assert.equal(result.afterUndo.citations.length, 0);
  const undoState = await evaluate("window.wordcraftHost.engine({op:'status'})");
  await editorKey('y', 'KeyY', 89);
  await waitFor("(async()=>{const s=await window.wordcraftHost.engine({op:'status'});return s.revision!==" + undoState.revision + "})()");
  await evaluate("window.wordcraftHost.saveIntent('verify-redo')");
  result.afterRedo = await evaluate("window.wordcraftHost.lastSave.version");
  assert.deepEqual(result.afterRedo.citations, result.saved.version.citations);
  // Exercise a stale client reference against the real current card. Confirmed
  // cards themselves require formal change and must not be edited around it.
  // A definite 422 permits explicit correction while retaining the DS bytes.
  async function backend(endpoint, body) {
    const response = await fetch(runtime.goOrigin + endpoint, {
      method: body === undefined ? 'GET' : 'POST',
      headers: { 'Content-Type': 'application/json', 'Idempotency-Key': crypto.randomUUID() },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const value = await response.json();
    assert(response.ok, JSON.stringify(value));
    return value.data ?? value;
  }
  const boot = await backend('/api/local/research/bootstrap');
  const uploaded = await backend('/api/v1/knowledge-bases/' + boot.knowledgeBaseId + '/knowledge');
  const knowledge = (Array.isArray(uploaded) ? uploaded : uploaded.items).find(item => item.file_name === path.basename(sourceFile));
  assert(knowledge, 'Original uploaded knowledge must exist');
  const original = await fetch(runtime.goOrigin + '/api/v1/knowledge/' + knowledge.id + '/download');
  assert.equal(original.status, 200);
  result.originalFileReadback = createHash('sha256').update(Buffer.from(await original.arrayBuffer())).digest('hex');
  assert.equal(result.originalFileReadback, result.sourceFile.sha256);
  const prefix = '/api/v1/lingdoc/projects/' + boot.projectId;
  const reference = result.afterRedo.citations[0];
  const card = await backend(prefix + '/evidence-cards/' + reference.evidenceCardId);
  assert(card.version > 1);
  await evaluate('window.wordcraftHost.research.restore(' + JSON.stringify([{ ...reference, evidenceCardVersion: card.version - 1 }]) + ')');
  // Redo may leave the caret inside protected guidance. Use the same safe
  // section-body placement as evidence insertion before changing the DOCX.
  await evaluate("(async()=>{const state=await window.wordcraftHost.engine({op:'status'});const manifest=await window.wordcraftHost.engine({op:'manifest',expectedDocumentId:state.documentId,expectedRevision:state.revision,templateId:'nsfc2026-annual-application',adapterVersion:'nsfc2026-r4-adapter-v1'});await window.wordcraftHost.engine({op:'focusSection',sectionId:'topic-summary',expectedDocumentId:state.documentId,expectedRevision:state.revision,expectedManifestHash:manifest.manifestHash,adapterVersion:'nsfc2026-r4-adapter-v1'});await window.wordcraftHost.selection();await window.wordcraftHost.insert(' 恢复登记验证。');})()");
  const rejected = await evaluate("window.wordcraftHost.saveIntent('verify-stale-card').then(()=>null,e=>({code:e.code,status:e.status,receipt:e.saveFacts?.receipt,version:e.saveFacts?.version}))");
  assert.equal(rejected.code, 'CITATION_INVALID');
  assert.equal(rejected.status, 422);
  assert(rejected.receipt && !rejected.version);
  const diskAfterRejection = await evaluate("window.wordcraftHost.diskVersion");
  // Await the actual refresh; the expected rejection remains visible until the
  // recovery action starts, so the generic error-watching waiter is unsuitable here.
  await evaluate("window.wordcraftHost.research.refresh()");
  await evaluate("(()=>{const b=[...document.querySelectorAll('#research-cards button')].find(x=>x.textContent.includes('用此卡补登记'));if(!b)throw Error('missing explicit recovery');b.id='verify-retry-citation'})()");
  await waitFor("document.querySelector('#verify-retry-citation') && !document.querySelector('#verify-retry-citation').disabled");
  await click('#verify-retry-citation');
  await waitFor("document.querySelector('#research-status').textContent.includes('已补登记原保存版本')");
  result.recovered = await evaluate("window.wordcraftHost.lastSave.version");
  assert.equal(await evaluate("window.wordcraftHost.diskVersion"), diskAfterRejection, 'Recovery must not export or write DS again');
  assert.equal(result.recovered.docSha256, rejected.receipt.sha256);
  assert.equal(result.recovered.citations[0].evidenceCardVersion, card.version);
  result.knownRejectionRecovery = { code: rejected.code, dsDiskVersionUnchanged: diskAfterRejection, goVersion: result.recovered.diskVersion };
  // Real new Go + DS processes, same isolated persistence, new browser document.
  await runtime.stop();
  runtime = await startDeliveryRuntime({ runtimeDir, research: true });
  await navigate();
  result.reopened = await evaluate("window.wordcraftHost.lastSave.version");
  assert.equal(result.reopened.docSha256, result.recovered.docSha256);
  assert.deepEqual(result.reopened.citations, result.recovered.citations);
  await click('#research-citations button');
  await waitFor("document.querySelector('#source-document-target')?.textContent.includes('LingDoc')");
  result.originalReadback = await evaluate("document.querySelector('#source-document-target').textContent");
  if (process.env.LINGDOC_RESEARCH_EXPECT_TEXT) assert(result.originalReadback.includes(process.env.LINGDOC_RESEARCH_EXPECT_TEXT), 'Expected PDF/DOCX text, including table cells, must survive source readback');
  await screenshot('reopened-original.png');
  if (process.env.LINGDOC_RESEARCH_REJECT_SOURCE_FILE) {
    const rejectedFile = path.resolve(process.env.LINGDOC_RESEARCH_REJECT_SOURCE_FILE);
    const assetsBefore = await backend(prefix + '/assets');
    const cardsBefore = await backend(prefix + '/evidence-cards');
    const currentDocument = await send('DOM.getDocument');
    const input = await send('DOM.querySelector', { nodeId: currentDocument.root.nodeId, selector: '#research-file' });
    await send('DOM.setFileInputFiles', { nodeId: input.nodeId, files: [rejectedFile] });
    await click('#research-upload');
    let error;
    for (let i = 0; i < 500 && !error; i++) {
      error = await evaluate("document.querySelector('#research-status.error')?.textContent");
      if (!error) await sleep(100);
    }
    assert(error?.includes('OCR'), 'Scanned PDF must show the actual OCR limitation in the UI');
    const uploadedFiles = await backend('/api/v1/knowledge-bases/' + boot.knowledgeBaseId + '/knowledge');
    const rejectedKnowledge = (Array.isArray(uploadedFiles) ? uploadedFiles : uploadedFiles.items).find(item => item.file_name === path.basename(rejectedFile));
    assert.equal(rejectedKnowledge?.parse_status, 'failed');
    assert.deepEqual(await backend(prefix + '/assets'), assetsBefore);
    assert.deepEqual(await backend(prefix + '/evidence-cards'), cardsBefore);
    result.scannedRejection = { file: rejectedFile, knowledgeId: rejectedKnowledge.id, parseStatus: rejectedKnowledge.parse_status, displayedError: error, assetsAndCardsUnchanged: true };
    await screenshot('scanned-pdf-blocked.png');
  }
  assert.equal(result.modelTurnRequests, 0, 'Full evidence/save/readback flow must stay independent of chat');
  assert.deepEqual(result.pageErrors, []);
  result.passed = true;
} catch (error) {
  result.error = error.stack; process.exitCode = 1;
  try {
    const shot = await send?.('Page.captureScreenshot', { format: 'png' });
    if (shot) await writeFile(path.join(artifacts, 'failure.png'), Buffer.from(shot.data, 'base64'));
  } catch {}
} finally {
  if (browser?.pid && browser.exitCode === null) {
    try { await send?.('Browser.close'); } catch {}
    for (let i = 0; i < 20 && browser.exitCode === null; i++) await sleep(100);
    if (browser.exitCode === null) spawnSync('taskkill', ['/PID', String(browser.pid), '/T', '/F'], { windowsHide: true, stdio: 'ignore' });
    for (let i = 0; i < 30 && browser.exitCode === null; i++) await sleep(100);
  }
  socket?.close();
  result.browserStopped = !browser?.pid || browser.exitCode !== null;
  try { if (runtime) await runtime.stop(); result.servicesStopped = true; }
  catch (error) { result.cleanupError = error.message; result.servicesStopped = false; }
  if (!result.browserStopped || !result.servicesStopped) { result.passed = false; process.exitCode = 1; }
  await mkdir(artifacts, { recursive: true });
  await writeFile(path.join(artifacts, 'verification.json'), JSON.stringify(result, null, 2));
  console.log(JSON.stringify({ artifacts, passed: result.passed, error: result.error, browserStopped: result.browserStopped, servicesStopped: result.servicesStopped }, null, 2));
}
