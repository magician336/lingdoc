// Two bounded real model calls using public software fixtures. No credentials
// are embedded or copied to evidence. Existing G2 handles all source policy.
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { createServer } from 'node:net';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { startDeliveryRuntime } from './delivery-runtime.mjs';
import { root, edge } from './local-environment.mjs';

const runId = new Date().toISOString().replaceAll(':', '-') + '-' + process.pid;
const artifacts = path.join(root, 'artifacts', 'research-ai', runId);
const runtimeDir = path.join(root, 'runtime', 'ai-check', runId);
const fixtures = path.resolve(root, '../../evidence/local-document-sources-20261031/fixtures');
const result = { runId, passed: false, pageErrors: [], requests: [], checks: [] };
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
let runtime, browser, socket, send;
async function freePort() {
  const server = createServer();
  await new Promise((resolve, reject) => server.once('error', reject).listen(0, '127.0.0.1', resolve));
  const port = server.address().port; await new Promise(resolve => server.close(resolve)); return port;
}
try {
  assert(process.env.LINGDOC_LOCAL_AI_CONFIG, 'A local private configuration path is required');
  await mkdir(artifacts, { recursive: true });
  runtime = await startDeliveryRuntime({ runtimeDir, research: true });
  result.runtime = { origin: runtime.origin, goOrigin: runtime.goOrigin, database: runtime.database, logs: runtime.logs };
  const backend = async (route, body, key) => {
    const response = await fetch(runtime.goOrigin + route, { method: body === undefined ? 'GET' : 'POST',
      headers: { 'Content-Type': 'application/json', ...(key ? { 'Idempotency-Key': key } : {}) },
      body: body === undefined ? undefined : JSON.stringify(body) });
    const payload = await response.json();
    assert(response.ok, response.status + ' ' + JSON.stringify(payload)); return payload.data ?? payload;
  };
  const debugPort = await freePort();
  browser = spawn(edge, ['--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    '--disable-background-timer-throttling', '--disable-backgrounding-occluded-windows', '--disable-renderer-backgrounding',
    '--remote-debugging-port=' + debugPort, '--user-data-dir=' + path.join(runtimeDir, 'edge'), 'about:blank'],
  { windowsHide: true, stdio: ['ignore', 'ignore', 'pipe'] });
  browser.stderr.resume();
  let page, launchError;
  browser.once('error', error => { launchError = error; });
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
    if (message.method === 'Network.requestWillBeSent') {
      const req = message.params.request;
      if (req.method === 'POST' && /\/agent-sessions\/[^/]+\/turns$/.test(req.url)) {
        result.requests.push({ url: req.url, key: req.headers['Idempotency-Key'], body: JSON.parse(req.postData) });
      }
    }
    const operation = pending.get(message.id);
    if (!operation) return;
    pending.delete(message.id); clearTimeout(operation.timer);
    if (message.error) operation.reject(new Error(message.error.message)); else operation.resolve(message.result);
  };
  send = (method, params = {}) => new Promise((resolve, reject) => {
    const id = ++sequence;
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(method + ' timed out')); }, 85000);
    pending.set(id, { resolve, reject, timer }); socket.send(JSON.stringify({ id, method, params }));
  });
  const evaluate = async expression => {
    const reply = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (reply.exceptionDetails) throw new Error(reply.exceptionDetails.exception?.description || reply.exceptionDetails.text);
    return reply.result.value;
  };
  const waitFor = async (expression, { allowError = false, timeout = 85000 } = {}) => {
    const deadline = Date.now() + timeout;
    while (Date.now() < deadline) {
      if (await evaluate(expression)) return;
      if (!allowError) {
        const error = await evaluate("document.querySelector('#research-status.error')?.textContent");
        if (error) throw new Error(error);
      }
      await sleep(150);
    }
    throw new Error('Timed out: ' + expression + '; ' + await evaluate("document.querySelector('#research-ai-status')?.textContent"));
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
  const idle = () => waitFor("document.querySelector('#research-panel').getAttribute('aria-busy')!=='true'");
  const navigate = async () => {
    await send('Page.navigate', { url: runtime.origin });
    await waitFor("(()=>{try{return !!JSON.parse(document.querySelector('#host-log').textContent).opened}catch{return false}})()");
    await click('#research-toggle'); await idle();
    await waitFor("document.querySelector('#research-ai')?.hidden===false");
  };
  await send('Page.enable'); await send('Runtime.enable'); await send('Network.enable');
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: 1, mobile: false });
  await navigate();
  const boot = await backend('/api/local/research/bootstrap');
  assert.equal(boot.ai.configured, true); result.ai = boot.ai;
  const prefix = '/api/v1/lingdoc/projects/' + boot.projectId;
  result.beforeDocument = await evaluate("window.wordcraftHost.engine({op:'status'})");
  for (const filename of ['source-text.pdf', 'source-table.docx']) {
    const document = await send('DOM.getDocument');
    const input = await send('DOM.querySelector', { nodeId: document.root.nodeId, selector: '#research-file' });
    await send('DOM.setFileInputFiles', { nodeId: input.nodeId, files: [path.join(fixtures, filename)] });
    await click('#research-upload'); await idle();
    assert((await evaluate("document.querySelector('#research-status').textContent")).includes('资料已加入项目'));
  }
  await evaluate("document.querySelector('#research-query').value='LingDoc'");
  await click('#research-search'); await idle();
  assert.equal(await evaluate("document.querySelectorAll('#research-hits button').length"), 2);
  for (let i = 0; i < 2; i++) {
    await click('#research-hits button:nth-child(' + (i + 1) + ')'); await idle();
    await click('#research-ai-add'); await idle();
  }
  await evaluate("document.querySelector('#research-ai-question').value='资料说一条可追溯的引用需要保存什么？请引用相关原文。'");
  await click('#research-ai-ask');
  await waitFor("document.querySelector('#research-status.error')?.textContent.includes('勾选')", { allowError: true });
  assert.equal(result.requests.length, 0); result.checks.push('No model request without explicit consent');
  await click('#research-ai-consent'); await click('#research-ai-ask'); await idle();
  assert((await evaluate("document.querySelector('#research-ai-status').textContent")).includes('回答已保存'));
  const firstRequest = result.requests[0];
  assert(firstRequest?.key, 'Capture the actual idempotency key');
  const sessionPath = new URL(firstRequest.url).pathname;
  const firstTurns = await backend(sessionPath);
  assert.equal(firstTurns.length, 1);
  const answer = firstTurns[0];
  assert.equal(answer.status, 'completed'); assert.equal(answer.run_mode, 'real');
  assert(answer.suggestions.citations.length > 0);
  for (const ref of answer.suggestions.citations) {
    assert(firstRequest.body.source_ids.includes(ref.source_id));
    const source = await backend(prefix + '/sources/' + ref.source_id);
    assert(ref.quote && source.quoted_text.includes(ref.quote));
  }
  result.answer = answer;
  await evaluate("document.querySelector('#research-ai-answers').scrollIntoView({block:'start'})");
  await screenshot('answer-with-citations.png');
  await click('#research-ai-answers button[data-source-id]'); await idle();
  assert((await evaluate("document.querySelector('#research-source').value")).includes(answer.suggestions.citations[0].quote));
  result.checks.push('PDF/DOCX question answered with allowed verbatim citations; UI opens original');
  const replay = await backend(sessionPath, firstRequest.body, firstRequest.key);
  assert.equal(replay.id, answer.id); assert.equal(replay.output_tokens, answer.output_tokens);
  assert.equal((await backend(sessionPath)).length, 1);
  result.checks.push('Same request key returns the same persisted turn without a new model invocation');
  await evaluate("document.querySelector('#research-ai-question').value='这些资料所描述的项目，2028年的预算具体是多少元？请给出原文依据。'");
  await click('#research-ai-ask'); await idle();
  const secondTurns = await backend(sessionPath);
  assert.equal(secondTurns.length, 2);
  const insufficient = secondTurns.find(turn => turn.id !== answer.id);
  assert.equal(insufficient.status, 'completed');
  assert.equal(insufficient.suggestions.citations.length, 0);
  assert.match(insufficient.reply, /不足|没有|未提供|无法/);
  result.insufficient = insufficient;
  result.checks.push('Question outside supplied evidence returns insufficient evidence without invented citations');
  await evaluate("document.querySelector('#research-ai-answers').scrollIntoView({block:'start'})");
  await screenshot('insufficient-evidence.png');
  result.afterDocument = await evaluate("window.wordcraftHost.engine({op:'status'})");
  assert.equal(result.afterDocument.revision, result.beforeDocument.revision);
  assert.equal(result.afterDocument.dirty, result.beforeDocument.dirty);
  const assetPolicies = await backend(prefix + '/assets');
  result.grants = await Promise.all(assetPolicies.map(asset => backend(prefix + '/assets/' + asset.id + '/policy')));
  assert(result.grants.every(policy => policy.model_use_allowed));
  // Same persistent database, genuinely new Go and DS processes. History is
  // read through current access checks, without sending another model request.
  const prior = { origin: runtime.origin, runtimeId: runtime.runtimeId, dsPid: runtime.dsPid };
  await runtime.stop();
  runtime = await startDeliveryRuntime({ runtimeDir, research: true, port: Number(new URL(prior.origin).port) });
  assert.notEqual(runtime.runtimeId, prior.runtimeId); assert.notEqual(runtime.dsPid, prior.dsPid);
  await navigate();
  assert.equal(await evaluate("document.querySelectorAll('#research-ai-answers article').length"), 2);
  const reopened = await backend(sessionPath);
  assert.deepEqual(reopened.map(turn => turn.id).sort(), secondTurns.map(turn => turn.id).sort());
  assert.equal(result.requests.length, 2);
  await click('#research-ai-answers button[data-source-id]'); await idle();
  result.reopen = { prior, current: { origin: runtime.origin, runtimeId: runtime.runtimeId, dsPid: runtime.dsPid }, turnIds: reopened.map(turn => turn.id) };
  result.checks.push('New Go/DS process restores history and source readback; DOCX revision unchanged');
  assert.deepEqual(result.pageErrors, []); result.passed = true;
} catch (error) {
  result.error = error.stack; process.exitCode = 1;
  try { const shot = await send?.('Page.captureScreenshot', { format: 'png' }); if (shot) await writeFile(path.join(artifacts, 'failure.png'), Buffer.from(shot.data, 'base64')); } catch {}
} finally {
  if (browser?.pid && browser.exitCode === null) {
    try { await send?.('Browser.close'); } catch {}
    for (let i = 0; i < 20 && browser.exitCode === null; i++) await sleep(100);
    if (browser.exitCode === null) spawnSync('taskkill', ['/PID', String(browser.pid), '/T', '/F'], { windowsHide: true, stdio: 'ignore' });
    for (let i = 0; i < 30 && browser.exitCode === null; i++) await sleep(100);
  }
  socket?.close(); result.browserStopped = !browser?.pid || browser.exitCode !== null;
  try { if (runtime) await runtime.stop(); result.servicesStopped = true; }
  catch (error) { result.cleanupError = error.message; result.servicesStopped = false; }
  if (!result.browserStopped || !result.servicesStopped) { result.passed = false; process.exitCode = 1; }
  await mkdir(artifacts, { recursive: true });
  await writeFile(path.join(artifacts, 'verification.json'), JSON.stringify(result, null, 2));
  console.log(JSON.stringify({ artifacts, passed: result.passed, checks: result.checks, error: result.error,
    browserStopped: result.browserStopped, servicesStopped: result.servicesStopped }, null, 2));
}
