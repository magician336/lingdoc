// Focused real Go/DS/Edge check. Own data and browser; never changes the trial.
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { createServer } from 'node:net';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { startDeliveryRuntime } from './delivery-runtime.mjs';
import { root, edge } from './local-environment.mjs';

const runId = `${new Date().toISOString().replaceAll(':', '-')}-${process.pid}`;
const artifacts = path.join(root, 'artifacts', 'delivery-feedback', runId);
const runtimeDir = path.join(root, 'runtime', 'delivery-feedback', runId);
const result = { runId, kind: 'real Go/SQLite/DS/WASM/Edge', passed: false, pageErrors: [] };
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
let runtime, browser, socket, send;

async function freePort() {
  const server = createServer();
  await new Promise((resolve, reject) => server.once('error', reject).listen(0, '127.0.0.1', resolve));
  const port = server.address().port;
  await new Promise(resolve => server.close(resolve));
  return port;
}

try {
  await mkdir(artifacts, { recursive: true });
  runtime = await startDeliveryRuntime({ runtimeDir });
  result.runtime = { origin: runtime.origin, goOrigin: runtime.goOrigin, database: runtime.database };
  const debugPort = await freePort();
  browser = spawn(edge, ['--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    '--disable-background-timer-throttling', '--disable-backgrounding-occluded-windows', '--disable-renderer-backgrounding',
    `--remote-debugging-port=${debugPort}`, `--user-data-dir=${path.join(runtimeDir, 'edge')}`, 'about:blank'],
  { windowsHide: true, stdio: ['ignore', 'ignore', 'pipe'] });
  let launchError;
  browser.once('error', error => { launchError = error; });
  browser.stderr.resume();
  let page;
  for (let i = 0; i < 100 && !page; i++) {
    if (launchError) throw launchError;
    try {
      const pages = await fetch(`http://127.0.0.1:${debugPort}/json/list`, { signal: AbortSignal.timeout(500) }).then(r => r.json());
      page = pages.find(item => item.type === 'page');
    } catch {}
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
    const request = pending.get(message.id);
    if (!request) return;
    pending.delete(message.id); clearTimeout(request.timer);
    if (message.error) request.reject(new Error(message.error.message)); else request.resolve(message.result);
  };
  send = (method, params = {}) => new Promise((resolve, reject) => {
    const id = ++sequence;
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`${method} timed out`)); }, 30000);
    pending.set(id, { resolve, reject, timer });
    socket.send(JSON.stringify({ id, method, params }));
  });
  const evaluate = async expression => {
    const reply = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (reply.exceptionDetails) throw new Error(reply.exceptionDetails.exception?.description || reply.exceptionDetails.text);
    return reply.result.value;
  };
  const waitFor = async expression => {
    for (let i = 0; i < 300; i++) { if (await evaluate(expression)) return; await sleep(100); }
    throw new Error(`Timed out: ${expression}; ${await evaluate(`document.querySelector('#delivery-status')?.textContent`)}`);
  };
  const click = async selector => {
    const point = await evaluate(`(()=>{const b=document.querySelector(${JSON.stringify(selector)});if(!b||b.disabled)throw Error('button unavailable');const r=b.getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()`);
    await send('Input.dispatchMouseEvent', { type: 'mouseMoved', ...point });
    await send('Input.dispatchMouseEvent', { type: 'mousePressed', button: 'left', clickCount: 1, ...point });
    await send('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', clickCount: 1, ...point });
  };
  const screenshot = async name => {
    const shot = await send('Page.captureScreenshot', { format: 'png' });
    await writeFile(path.join(artifacts, name), Buffer.from(shot.data, 'base64'));
  };
  await send('Page.enable'); await send('Runtime.enable');
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
  await send('Page.navigate', { url: runtime.origin });
  await waitFor(`(()=>{try{return !!JSON.parse(document.querySelector('#host-log').textContent).opened}catch{return false}})()`);
  await evaluate('window.wordcraftHost.prepareDelivery()');
  await waitFor(`!document.querySelector('#delivery-confirm-topic-summary').disabled`);
  await click('#delivery-confirm-topic-summary');
  await waitFor(`document.querySelector('#delivery-status').textContent.includes('RULE_CHECK_FAILED')`);
  result.rejected = await evaluate(`({message:document.querySelector('#delivery-status').textContent,confirmed:window.lingdocDiagnostics().lastSave.delivery.chapters.filter(x=>x.confirmed).length,freezeDisabled:document.querySelector('#delivery-freeze').disabled})`);
  assert.match(result.rejected.message, /选题说明：本章正文为空/);
  assert.equal(result.rejected.confirmed, 0);
  assert.equal(result.rejected.freezeDisabled, true);
  // A second failed action must replace the chapter context, without hiding the error.
  await click('#delivery-confirm-topic-basis');
  await waitFor(`document.querySelector('#delivery-status').textContent.includes('选题依据：本章正文为空')`);
  await sleep(500);
  result.secondRejection = await evaluate(`document.querySelector('#delivery-status').textContent`);
  assert.match(result.secondRejection, /选题依据：本章正文为空/);
  assert.doesNotMatch(result.secondRejection, /选题说明：本章正文为空/);
  await screenshot('rejected.png');
  await click('#delivery-close');

  const sections = ['topic-summary', 'topic-basis', 'research-content', 'innovation', 'expected-results', 'references'];
  async function appendEachChapter(textForSection) {
    for (const sectionId of sections) {
    await evaluate(`(async()=>{
      const h=window.wordcraftHost,s=await h.engine({op:'status'});
      const m=await h.engine({op:'manifest',expectedDocumentId:s.documentId,expectedRevision:s.revision,templateId:'nsfc2026-annual-application',adapterVersion:'nsfc2026-r4-adapter-v1'});
      await h.engine({op:'focusSection',sectionId:${JSON.stringify(sectionId)},expectedDocumentId:s.documentId,expectedRevision:s.revision,expectedManifestHash:m.manifestHash,adapterVersion:'nsfc2026-r4-adapter-v1'});
      await h.selection(); await h.insert(${JSON.stringify(textForSection(sectionId))});
    })()`);
    }
  }
  await appendEachChapter(sectionId => `演示正文：${sectionId}，用于验证保存和交付，不代表真实研究结论。`);
  // Confirmation and freeze have distinct checks. The seeded source ledger
  // also requires matching citations at freeze; do not silently pass that gate.
  await evaluate('window.wordcraftHost.prepareDelivery()');
  await waitFor(`!document.querySelector('#delivery-confirm-topic-summary').disabled`);
  assert.doesNotMatch(await evaluate(`document.querySelector('#delivery-status').textContent`), /RULE_CHECK_FAILED/);
  for (const [index, sectionId] of sections.entries()) {
    await click(`#delivery-confirm-${sectionId}`);
    await waitFor(`window.lingdocDiagnostics().lastSave.delivery.chapters.filter(x=>x.confirmed).length===${index + 1}`);
  }
  await click('#delivery-freeze');
  await waitFor(`window.lingdocDiagnostics().lastSave.delivery.snapshot?.check.status==='blocked'`);
  result.blockedFreeze = await evaluate(`({message:document.querySelector('#delivery-status').textContent,confirmed:window.lingdocDiagnostics().lastSave.delivery.chapters.filter(x=>x.confirmed).length,exportDisabled:document.querySelector('#delivery-export').disabled})`);
  assert.equal(result.blockedFreeze.confirmed, 6);
  assert.equal(result.blockedFreeze.exportDisabled, true);
  assert.match(result.blockedFreeze.message, /交付检查未通过/);
  assert.match(result.blockedFreeze.message, /选题说明：正文引用与已登记的来源不一致/);
  await screenshot('freeze-blocked.png');
  await click('#delivery-close');
  // Complete the controlled demo using its already-bound synthetic source.
  await appendEachChapter(() => ' 测试资料引用 [[source:source-1]] ');
  await evaluate('window.wordcraftHost.prepareDelivery()');
  await waitFor(`!document.querySelector('#delivery-confirm-topic-summary').disabled`);
  assert.doesNotMatch(await evaluate(`document.querySelector('#delivery-status').textContent`), /来源不一致|RULE_CHECK_FAILED/);
  for (const [index, sectionId] of sections.entries()) {
    await click(`#delivery-confirm-${sectionId}`);
    await waitFor(`window.lingdocDiagnostics().lastSave.delivery.chapters.filter(x=>x.confirmed).length===${index + 1}`);
  }
  await click('#delivery-freeze');
  await waitFor(`!document.querySelector('#delivery-export').disabled`);
  await send('Browser.setDownloadBehavior', { behavior: 'allow', downloadPath: artifacts });
  await click('#delivery-export');
  await waitFor(`window.lingdocDiagnostics().lastExport?.status==='verified'`);
  result.delivered = await evaluate(`({versionId:window.lingdocDiagnostics().lastSave.version.id,sha256:window.lingdocDiagnostics().lastSave.version.docSha256,snapshotId:window.lingdocDiagnostics().lastSave.delivery.snapshot.id,exportId:window.lingdocDiagnostics().lastExport.id,confirmed:window.lingdocDiagnostics().lastSave.delivery.chapters.filter(x=>x.confirmed).length,message:document.querySelector('#delivery-status').textContent})`);
  let bytes;
  for (let i = 0; i < 100 && !bytes; i++) {
    try { bytes = await readFile(path.join(artifacts, `lingdoc-${result.delivered.exportId}.docx`)); } catch { await sleep(100); }
  }
  assert(bytes, 'DOCX download missing');
  result.delivered.downloadSha256 = createHash('sha256').update(bytes).digest('hex');
  assert.equal(result.delivered.downloadSha256, result.delivered.sha256);
  assert.doesNotMatch(result.delivered.message, /RULE_CHECK_FAILED/);
  result.testData = 'Real engine: empty chapters rejected; six bodies without citations confirmed but freeze blocked by the seeded source ledger; matching source-1 demo citations then allow freeze/export. No change to the rule or ledger.';
  await screenshot('delivered.png');
  assert.deepEqual(result.pageErrors, []);
  result.passed = true;
} catch (error) {
  result.error = error.stack;
  process.exitCode = 1;
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
  console.log(JSON.stringify({ artifacts, ...result }, null, 2));
}
