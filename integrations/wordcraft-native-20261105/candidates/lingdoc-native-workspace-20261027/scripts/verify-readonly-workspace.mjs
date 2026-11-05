import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { createServer } from 'node:net';
import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { startDeliveryRuntime } from './delivery-runtime.mjs';
import { root, edge } from './local-environment.mjs';

const runId = new Date().toISOString().replaceAll(':', '-') + '-' + process.pid;
const artifacts = path.join(root, 'artifacts', 'readonly-workspace', runId);
const runtimeDir = path.join(root, 'runtime', 'readonly-workspace', runId);
const result = { runId, passed: false, pageErrors: [], modelRequests: [], checks: {} };
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
let runtime, browser, socket, send;
async function freePort() {
  const server = createServer();
  await new Promise((resolve, reject) => server.once('error', reject).listen(0, '127.0.0.1', resolve));
  const port = server.address().port;
  await new Promise(resolve => server.close(resolve));
  return port;
}
async function stopBrowser() {
  if (!browser) return true;
  if (browser.exitCode === null && browser.pid) spawnSync('taskkill', ['/PID', String(browser.pid), '/T', '/F'], { stdio: 'ignore' });
  for (let i = 0; i < 30 && browser.exitCode === null; i++) await sleep(100);
  return browser.exitCode !== null;
}
try {
  assert(process.env.LINGDOC_LOCAL_AI_CONFIG, 'A local private configuration path is required');
  await mkdir(artifacts, { recursive: true });
  runtime = await startDeliveryRuntime({ runtimeDir, research: true });
  const backend = async (route, body) => {
    const response = await fetch(runtime.goOrigin + route, {
      method: body === undefined ? 'GET' : 'POST',
      headers: body === undefined ? {} : { 'Content-Type': 'application/json', 'Idempotency-Key': crypto.randomUUID() },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const payload = await response.json();
    assert(response.ok, response.status + ' ' + JSON.stringify(payload));
    return payload.data ?? payload;
  };
  const debugPort = await freePort();
  browser = spawn(edge, ['--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    '--disable-background-timer-throttling', '--disable-backgrounding-occluded-windows', '--disable-renderer-backgrounding',
    '--remote-debugging-port=' + debugPort, '--user-data-dir=' + path.join(runtimeDir, 'edge'), 'about:blank'],
  { windowsHide: true, stdio: ['ignore', 'ignore', 'pipe'] });
  browser.stderr.resume();
  let page;
  for (let i = 0; i < 100 && !page; i++) {
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
    if (message.method === 'Network.requestWillBeSent' && message.params.request.method === 'POST' && /\/agent-sessions\/[^/]+\/turns$/.test(message.params.request.url)) {
      result.modelRequests.push(message.params.request.url);
    }
    const operation = pending.get(message.id);
    if (!operation) return;
    pending.delete(message.id); clearTimeout(operation.timer);
    if (message.error) operation.reject(new Error(message.error.message)); else operation.resolve(message.result);
  };
  send = (method, params = {}) => new Promise((resolve, reject) => {
    const id = ++sequence;
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(method + ' timed out')); }, 30000);
    pending.set(id, { resolve, reject, timer }); socket.send(JSON.stringify({ id, method, params }));
  });
  const evaluate = async expression => {
    const reply = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (reply.exceptionDetails) throw new Error(reply.exceptionDetails.exception?.description || reply.exceptionDetails.text);
    return reply.result.value;
  };
  const waitFor = async expression => {
    for (let i = 0; i < 400; i++) { if (await evaluate(expression)) return; await sleep(100); }
    throw new Error('Timed out: ' + expression);
  };
  const screenshot = async name => {
    const shot = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
    await writeFile(path.join(artifacts, name + '.png'), Buffer.from(shot.data, 'base64'));
  };
  await send('Page.enable'); await send('Runtime.enable'); await send('Network.enable');
  // Never spend a model call in this regression, including if a disabled UI path regresses.
  await send('Page.addScriptToEvaluateOnNewDocument', { source: `
    window.__blockedModelAttempts=[];
    const originalFetch=window.fetch.bind(window);
    window.fetch=(input,options={})=>{
      const url=typeof input==='string'?input:input.url;
      const method=options.method||input?.method||'GET';
      if(method==='POST' && /\\/agent-sessions\\/[^/]+\\/turns$/.test(url)){
        window.__blockedModelAttempts.push({url,body:options.body});
        return Promise.reject(new Error('Test blocked unexpected model request'));
      }
      return originalFetch(input,options);
    };` });
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
  await send('Page.navigate', { url: runtime.origin });
  const settled = `window.wordcraftHost?.research && document.querySelector('#workspace-materials') && !document.querySelector('#research-panel').hasAttribute('aria-busy')`;
  await waitFor(settled);
  await evaluate(`window.wordcraftHost.research.ready`);
  const visibility = `(()=>{const visible=id=>{const el=document.getElementById(id);return !!el&&el.getClientRects().length>0&&getComputedStyle(el).display!=='none'};return{left:visible('research-materials')||visible('research-evidence'),right:visible('research-ai')}})()`;
  const assertPanels = async (left, right) => assert.deepEqual(await evaluate(visibility), { left, right });
  for (const width of [1440, 900]) {
    await send('Emulation.setDeviceMetricsOverride', { width, height: 950, deviceScaleFactor: 1, mobile: false });
    await evaluate(`document.querySelector('#workspace-focus').click()`);
    await assertPanels(false, false);
    await evaluate(`document.querySelector('#workspace-ai').click()`);
    await assertPanels(false, true);
    await evaluate(`document.querySelector('#workspace-materials').click()`);
    await assertPanels(true, true);
    await evaluate(`document.querySelector('#workspace-ai').click()`);
    await assertPanels(true, false);
    await evaluate(`document.querySelector('#workspace-materials').click()`);
    await assertPanels(false, false);
    await evaluate(`document.querySelector('#workspace-materials').click()`);
    await assertPanels(true, false);
    await evaluate(`document.querySelector('#workspace-ai').click();document.querySelector('#research-close').click()`);
    await assertPanels(false, true);
    await evaluate(`document.querySelector('#research-ai-close').click()`);
    await assertPanels(false, false);
    result.checks['independentPanels' + width] = true;
  }
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
  await evaluate(`document.querySelector('#workspace-materials').click();document.querySelector('#workspace-ai').click()`);
  const disabledEntries = ['assistant-add-source', 'assistant-selection', 'assistant-cite', 'research-ai-add-source'];
  for (const id of disabledEntries) {
    assert(await evaluate(`(()=>{const el=document.getElementById(${JSON.stringify(id)});return !el||!el.getClientRects().length||getComputedStyle(el).display==='none'})()`), id + ' still visible');
    await evaluate(`document.getElementById(${JSON.stringify(id)})?.click()`);
    await waitFor(`!document.querySelector('#research-panel').hasAttribute('aria-busy')`);
  }
  assert(await evaluate(`!document.body.classList.contains('citation-selection-mode') && !document.querySelector('#source-document-select-text') && !document.querySelector('#assistant-source-picker')?.getClientRects().length && !document.querySelector('#research-ai-source-picker')?.getClientRects().length`));
  await evaluate(`document.querySelector('#research-chat-source').click();document.querySelector('#research-ai-question').value='不得发送的旧资料提问';document.querySelector('#research-ai-consent').checked=true;document.querySelector('#research-ai-ask').click()`);
  await waitFor(`!document.querySelector('#research-panel').hasAttribute('aria-busy')`);
  assert.deepEqual(await evaluate(`window.__blockedModelAttempts`), []);
  result.checks.disabledContextEntryAndSourceChat = true;
  assert(await evaluate(`document.querySelector('#research-panel').dataset.evidencePaused==='true' && !document.querySelector('.research-evidence-form').getClientRects().length`), 'Manual evidence form must be paused');
  const beforeEvidence = await backend('/api/local/research/bootstrap');
  const evidenceProjectPath = '/api/v1/lingdoc/projects/' + encodeURIComponent(beforeEvidence.projectId);
  const cardsBefore = await backend(evidenceProjectPath + '/evidence-cards');
  const cardCountBefore = (Array.isArray(cardsBefore) ? cardsBefore : cardsBefore.items ?? []).length;
  await evaluate(`document.querySelector('#research-create').click()`);
  await waitFor(`!document.querySelector('#research-panel').hasAttribute('aria-busy')`);
  const cardsAfter = await backend(evidenceProjectPath + '/evidence-cards');
  const cardCountAfter = (Array.isArray(cardsAfter) ? cardsAfter : cardsAfter.items ?? []).length;
  assert.equal(cardCountAfter, cardCountBefore, 'Hidden create handler must not create evidence');
  await evaluate(`document.querySelector('#workspace-evidence').click()`);
  await waitFor(`!document.querySelector('#research-panel').hasAttribute('aria-busy')`);
  assert(await evaluate(`[...document.querySelectorAll('#research-cards button')].every(button=>!/(确认|引用到|补登记)/.test(button.textContent))`), 'Manual confirmation/insertion controls must be paused');
  result.checks.manualEvidencePaused = true;

  // Seed a retained pending operation and body selection; attempting to continue
  // it must preserve its exact payload instead of silently dropping context.
  const bootstrap = await backend('/api/local/research/bootstrap');
  const projectPath = '/api/v1/lingdoc/projects/' + encodeURIComponent(bootstrap.projectId);
  const project = await backend(projectPath);
  const session = await backend(projectPath + '/agents/workspace_assistant/sessions', {
    expected_project_version: project.project_version, expected_context_revision: project.current_context_revision, expected_version: 0,
  });
  const storedContext = {
    selection: { text: '保留的正文选区', document_id: 1, document_revision: 1, text_sha256: 'retained-hash', locator: '正文圈选' },
    sources: [], draft: '保留草稿',
    pending: { question: '旧请求', sourceIds: ['retained-source'], sourceHashes: {}, documentSelection: { text: '保留的正文选区' }, steps: {},
      model: { key: 'retained-idempotency-key', body: { user_input: '旧请求', source_ids: ['retained-source'], document_selection: { text: '保留的正文选区' } } } },
  };
  const storageKey = 'lingdoc-workspace-assistant:' + session.id;
  await evaluate(`localStorage.setItem(${JSON.stringify(storageKey)},${JSON.stringify(JSON.stringify(storedContext))});localStorage.setItem(${JSON.stringify('lingdoc-workspace-assistant:last:' + projectPath)},${JSON.stringify(session.id)})`);
  await send('Page.navigate', { url: runtime.origin });
  await waitFor(settled);
  await evaluate(`window.wordcraftHost.research.ready`);
  await evaluate(`document.querySelector('#assistant-composer').requestSubmit()`);
  await waitFor(`!document.querySelector('#research-panel').hasAttribute('aria-busy')`);
  const retained = await evaluate(`JSON.parse(localStorage.getItem(${JSON.stringify(storageKey)}))`);
  assert.deepEqual(retained.pending, storedContext.pending, 'Paused context must retain exact pending operation');
  assert.deepEqual(retained.selection, storedContext.selection, 'Paused context must retain body attachment');
  assert.deepEqual(await evaluate(`window.__blockedModelAttempts`), []);
  result.checks.retainedPendingBlocked = true;
  await evaluate(`document.querySelector('#assistant-new').click()`);
  await waitFor(`!document.querySelector('#research-panel').hasAttribute('aria-busy')`);
  assert(await evaluate(`!document.querySelector('#assistant-send').disabled && !document.querySelector('#assistant-question').disabled`), 'Plain new chat unavailable');
  result.checks.plainChatAvailable = true;

  await waitFor(`window.wordcraftHost.engine({op:'inspect'}).then(value=>value.blocks?.some(block=>block.type==='paragraph'&&block.text?.includes('2026'))).catch(()=>false)`);
  const dirtyState = await evaluate(`(async()=>{
    window.__retainedCanvas=document.querySelector('#wordcraft_canvas');
    const state=await window.wordcraftHost.engine({op:'status'});
    const inspect=await window.wordcraftHost.engine({op:'inspect'});
    const target=inspect.blocks.find(block=>block.type==='paragraph'&&block.text?.includes('2026')&&block.len>0);
    const position=inspect.selection.anchor;
    await window.wordcraftHost.engine({op:'select',range:{anchor:{...position,path:[target.index],off:target.len},focus:{...position,path:[target.index],off:target.len}},expectedDocumentId:state.documentId,expectedRevision:state.revision});
    const selected=await window.wordcraftHost.engine({op:'selection'});
    await window.wordcraftHost.engine({op:'insert',text:'READONLY-NAV-UNSAVED',bookmark:selected.selection,expectedDocumentId:selected.state.documentId,expectedRevision:selected.state.revision});
    return window.wordcraftHost.engine({op:'status'});
  })()`);
  assert(dirtyState.dirty);
  assert.equal(await evaluate(`!!document.querySelector('#project-library')`), false, 'New library page is deferred');
  await evaluate(`document.querySelector('#workspace-focus').click();document.querySelector('#workspace-ai').click();document.querySelector('#workspace-materials').click()`);
  assert.deepEqual(await evaluate(`window.wordcraftHost.engine({op:'status'})`), dirtyState);
  assert(await evaluate(`document.querySelector('#wordcraft_canvas')===window.__retainedCanvas`));
  assert(await evaluate(`window.wordcraftHost.engine({op:'inspect'}).then(value=>value.blocks.some(block=>block.text?.includes('READONLY-NAV-UNSAVED')))`));
  result.checks.navigationPreservesDirtyDocument = true;
  await screenshot('independent-sidebar-workspace');
  assert.deepEqual(await evaluate(`window.__blockedModelAttempts`), []);
  assert.deepEqual(result.modelRequests, []);
  assert.deepEqual(result.pageErrors, []);
  result.passed = true;
} catch (error) {
  result.error = error.stack; process.exitCode = 1;
} finally {
  if (socket) try { socket.close(); } catch {}
  result.browserExited = await stopBrowser();
  if (runtime) {
    try { await runtime.stop(); result.runtimeStopped = true; }
    catch (error) { result.cleanupError = error.stack; result.passed = false; process.exitCode = 1; }
  }
  await mkdir(artifacts, { recursive: true });
  await writeFile(path.join(artifacts, 'verification.json'), JSON.stringify(result, null, 2));
  console.log(JSON.stringify({ artifacts, ...result }, null, 2));
}
