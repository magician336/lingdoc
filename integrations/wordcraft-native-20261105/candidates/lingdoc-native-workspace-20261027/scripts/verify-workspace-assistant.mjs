import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { createServer } from 'node:net';
import { DatabaseSync } from 'node:sqlite';
import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { startDeliveryRuntime } from './delivery-runtime.mjs';
import { root, edge } from './local-environment.mjs';

const runId = new Date().toISOString().replaceAll(':', '-') + '-' + process.pid;
const artifacts = path.join(root, 'artifacts', 'workspace-assistant', runId);
const runtimeDir = path.join(root, 'runtime', 'workspace-assistant', runId);
const result = { runId, passed: false, pageErrors: [], modelTurnRequests: 0, requests: [], checks: {} };
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
let runtime, browser, socket, send;
async function freePort() {
  const server = createServer();
  await new Promise((resolve, reject) => server.once('error', reject).listen(0, '127.0.0.1', resolve));
  const port = server.address().port; await new Promise(resolve => server.close(resolve)); return port;
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
      const request = message.params.request;
      result.modelTurnRequests++;
      result.requests.push({ url: request.url, key: request.headers['Idempotency-Key'], body: JSON.parse(request.postData) });
    }
    const operation = pending.get(message.id);
    if (!operation) return;
    pending.delete(message.id); clearTimeout(operation.timer);
    if (message.error) operation.reject(new Error(message.error.message)); else operation.resolve(message.result);
  };
  send = (method, params = {}) => new Promise((resolve, reject) => {
    const id = ++sequence;
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(method + ' timed out')); }, 90000);
    pending.set(id, { resolve, reject, timer }); socket.send(JSON.stringify({ id, method, params }));
  });
  const evaluate = async expression => {
    const reply = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (reply.exceptionDetails) throw new Error(reply.exceptionDetails.exception?.description || reply.exceptionDetails.text);
    return reply.result.value;
  };
  const waitFor = async expression => {
    for (let i = 0; i < 600; i++) { if (await evaluate(expression)) return; await sleep(150); }
    throw new Error('Timed out: ' + expression + '\n' + await evaluate(`JSON.stringify({status:document.querySelector('#assistant-status')?.textContent,research:document.querySelector('#research-status')?.textContent,context:document.querySelector('#assistant-context')?.textContent,host:document.querySelector('#host-log')?.textContent?.slice(-1200)})`));
  };
  const click = async selector => {
    const point = await evaluate(`(()=>{const b=document.querySelector(${JSON.stringify(selector)});if(!b||b.disabled)throw Error('button unavailable');b.scrollIntoView({block:'center'});const r=b.getBoundingClientRect();return{x:r.x+r.width/2,y:r.y+r.height/2}})()`);
    await send('Input.dispatchMouseEvent', { type: 'mousePressed', button: 'left', clickCount: 1, ...point });
    await send('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', clickCount: 1, ...point });
  };
  const navigate = async () => {
    await send('Page.navigate', { url: runtime.origin });
    await waitFor(`window.wordcraftHost && !document.querySelector('#research-workspace-chat')?.hidden && document.querySelector('#assistant-send')`);
    await waitFor(`(()=>{try{return !!JSON.parse(document.querySelector('#host-log').textContent).opened}catch{return false}})()`);
  };
  const ask = async (question, count) => {
    const answered = `document.querySelectorAll('#assistant-messages .assistant-turn').length>=${count} && (()=>{const t=document.querySelectorAll('#assistant-messages .assistant-turn')[${count - 1}];return t && t.querySelector('.assistant-answer')?.textContent.trim().length>0})()`;
    await evaluate(`document.querySelector('#assistant-question').value=${JSON.stringify(question)}`);
    await click('#assistant-send');
    try { await waitFor(answered); }
    catch (error) {
      if (!(await evaluate(`document.querySelector('#assistant-status').textContent.includes('待确认')`))) throw error;
      for (let i = 0; i < 60 && !(await evaluate(answered)); i++) {
        await sleep(1000);
        await evaluate(`(()=>{const b=document.querySelector('#assistant-reload');if(!b.disabled)b.click()})()`);
      }
      if (!(await evaluate(answered))) throw error;
    }
    return evaluate(`(()=>{const t=document.querySelectorAll('#assistant-messages .assistant-turn')[${count - 1}];return {text:t.querySelector('.assistant-answer').textContent,usage:t.querySelector('.assistant-history-usage')?.textContent||''}})()`);
  };
  const backend = async (route, body, key) => {
    const response = await fetch(runtime.goOrigin + route, { method: body === undefined ? 'GET' : 'POST',
      headers: { 'Content-Type': 'application/json', ...(key ? { 'Idempotency-Key': key } : {}) },
      body: body === undefined ? undefined : JSON.stringify(body) });
    const payload = await response.json();
    assert(response.ok, response.status + ' ' + JSON.stringify(payload));
    return payload.data ?? payload;
  };
  const waitNode = async (check, label) => {
    for (let i = 0; i < 600; i++) { if (await check()) return; await sleep(150); }
    throw new Error('Timed out: ' + label);
  };
  const interceptModelResponse = question => evaluate(`(()=>{
    const original=window.fetch.bind(window);
    window.fetch=async(...args)=>{
      const response=await original(...args);
      const [input,options={}]=args;
      const url=typeof input==='string'?input:input.url;
      const method=(options.method||input.method||'GET').toUpperCase();
      if(method==='POST' && /\\/agent-sessions\\/[^/]+\\/turns$/.test(url) && String(options.body||'').includes(${JSON.stringify(question)})){
        await new Promise((resolve,reject)=>{window.__rejectHeldModel=reject});
      }
      return response;
    };
  })()`);
  const pendingStored = async (question, request) => {
    const values = await evaluate(`Object.entries(localStorage).map(([key,value])=>{try{return{key,state:JSON.parse(value)}}catch{return null}}).filter(Boolean)`);
    const matches = values.filter(item => item.state.pending?.question === question);
    assert.equal(matches.length, 1, 'One UI pending operation must survive the unknown result');
    const operation = matches[0].state.pending;
    assert.deepEqual(operation.model.body, request.body);
    assert.equal(operation.model.key, request.key);
    return matches[0].key;
  };
  const abortModelResponse = async question => {
    await waitNode(() => result.requests.some(item => item.body.user_input === question), 'browser model request for ' + question);
    const request = result.requests.find(item => item.body.user_input === question);
    const path = new URL(request.url).pathname;
    let persisted;
    await waitNode(async () => {
      persisted = (await backend(path)).find(turn => turn.user_input === question);
      return persisted?.status === 'completed';
    }, 'persisted turn before abort for ' + question);
    await evaluate(`window.__rejectHeldModel(new TypeError('Simulated unknown model result'))`);
    await waitFor(`document.querySelector('#research-panel').getAttribute('aria-busy')!=='true'`);
    const storageKey = await pendingStored(question, request);
    return { path, request, persisted, storageKey, turnCount: (await backend(path)).length };
  };
  const assertBrowserReplay = async (question, aborted) => {
    const requests = result.requests.filter(item => item.body.user_input === question);
    assert.equal(requests.length, 2, 'Initial and recovered browser requests are required');
    assert.deepEqual(requests[1].body, aborted.request.body);
    assert.equal(requests[1].key, aborted.request.key);
    const turns = await backend(aborted.path);
    assert.equal(turns.length, aborted.turnCount);
    assert(turns.some(turn => turn.id === aborted.persisted.id && turn.status === 'completed'));
    return { frozenBody: requests[1].body, frozenKey: requests[1].key, turnId: aborted.persisted.id, storageKey: aborted.storageKey };
  };
  const createCrlfDocx = async () => {
    const documentPath = path.join(artifacts, 'crlf-source.docx');
    const scriptPath = path.join(artifacts, 'make-crlf-source.py');
    await writeFile(scriptPath, "from docx import Document\nimport sys\ndoc=Document()\ndoc.add_paragraph('CRLF 标记 😀 前置')\ndoc.add_paragraph('不知道怎样开始')\ndoc.add_paragraph('结尾')\ndoc.save(sys.argv[1])\n");
    const made = spawnSync('python', [scriptPath, documentPath], { encoding: 'utf8' });
    assert.equal(made.status, 0, made.stderr || made.stdout);
    return documentPath;
  };
  const convertFixtureToCrlf = async sourceId => {
    const boot = await backend('/api/local/research/bootstrap');
    const prefix = '/api/v1/lingdoc/projects/' + encodeURIComponent(boot.projectId);
    const [source, assets] = await Promise.all([
      backend(prefix + '/sources/' + encodeURIComponent(sourceId)), backend(prefix + '/assets'),
    ]);
    const knowledgeId = assets.find(asset => asset.id === source.asset_id)?.knowledge_id;
    assert(knowledgeId, 'Fixture knowledge ID is required');
    const uiPort = Number(new URL(runtime.origin).port);
    await runtime.stop();
    const db = new DatabaseSync(path.join(runtimeDir, 'lingdoc.db'));
    try {
      db.exec('PRAGMA busy_timeout=5000');
      const target = db.prepare('SELECT chunk_type FROM chunks WHERE id=?').get(sourceId);
      const snapshot = db.prepare('SELECT text FROM lingdoc_parsed_origin_snapshots WHERE knowledge_id=? ORDER BY attempt DESC LIMIT 1').get(knowledgeId);
      assert(target && snapshot, 'DOCX parsed-origin fixture is incomplete');
      const normalized = snapshot.text.replace(/\r\n|\r/g, '\n');
      const rawText = normalized.replaceAll('\n', '\r\n');
      const rows = db.prepare('SELECT id, content, start_at, end_at FROM chunks WHERE knowledge_id=? AND chunk_type=? AND deleted_at IS NULL').all(knowledgeId, target.chunk_type);
      const rawOffset = offset => Array.from(Array.from(normalized).slice(0, offset).join('').replaceAll('\n', '\r\n')).length;
      db.exec('BEGIN');
      for (const row of rows) {
        const content = row.content.replaceAll('\n', '\r\n');
        db.prepare('UPDATE chunks SET content=?, source_content=?, content_hash=?, start_at=?, end_at=? WHERE id=?')
          .run(content, content, createHash('sha256').update(content).digest('hex'), rawOffset(row.start_at), rawOffset(row.end_at), row.id);
      }
      db.prepare('UPDATE lingdoc_parsed_origin_snapshots SET text=?, text_sha256=? WHERE knowledge_id=?')
        .run(rawText, createHash('sha256').update(rawText).digest('hex'), knowledgeId);
      db.exec('COMMIT');
    } finally { db.close(); }
    runtime = await startDeliveryRuntime({ runtimeDir, research: true, port: uiPort });
    await navigate();
    await click('#research-chat-source');
    let document;
    await waitNode(async () => {
      document = await backend(prefix + '/sources/' + encodeURIComponent(sourceId) + '/document');
      return document.text.includes('\r\n') && document.text.includes('😀');
    }, 'CRLF source document visibility');
    assert(document.segments.some(segment => segment.source_id === sourceId));
    return { document, prefix };
  };
  const screenshot = async name => {
    const shot = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
    await writeFile(path.join(artifacts, name), Buffer.from(shot.data, 'base64'));
  };
  const replay = async request => {
    const before = result.requests.length;
    const path = new URL(request.url).pathname;
    const original = (await backend(path)).find(turn => turn.user_input === request.body.user_input);
    assert(original, 'Original turn not found for replay');
    const replayed = await backend(path, request.body, request.key);
    assert.equal(replayed.id, original.id);
    assert.equal(result.requests.length, before);
    return replayed;
  };
  await send('Page.enable'); await send('Runtime.enable'); await send('Network.enable');
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
  await navigate();
  assert(await evaluate(`!!document.querySelector('#assistant-add-source') && !!document.querySelector('#research-ai-add-source')`), 'Both chats need right-side source pickers');
  const before = await evaluate(`window.wordcraftHost.engine({op:'status'})`);
  const first = await ask('用中文列出两个简短要点：写申请书时为什么要先核对资料？', 1);
  assert(first.text.length > 0 && !first.text.includes('资料不足'));
  const second = await ask('展开刚才第二点，限 80 字。', 2);
  assert(second.text.length > 0 && !second.text.includes('资料不足'));
  assert(second.usage.includes('携带 1 轮历史'));
  result.checks.multiTurn = { first: first.text, second: second.text, usage: second.usage };

  const selected = await evaluate(`(async()=>{const state=await window.wordcraftHost.engine({op:'status'});const inspect=await window.wordcraftHost.engine({op:'inspect'});const target=inspect.blocks.find(block=>block.type==='paragraph'&&block.text?.includes('2026')&&block.len>0);const position=inspect.selection.anchor;const range={anchor:{...position,path:[target.index],off:0},focus:{...position,path:[target.index],off:target.len}};await window.wordcraftHost.engine({op:'select',range,expectedDocumentId:state.documentId,expectedRevision:state.revision});return window.wordcraftHost.engine({op:'selection'});})()`);
  assert(selected.selection.text.trim().length > 0);
  await sleep(450);
  await evaluate(`document.querySelector('#assistant-tools').click()`);
  await click('#assistant-selection');
  const canvasPoint = await evaluate(`(()=>{const r=document.querySelector('#wordcraft_canvas').getBoundingClientRect();return{x:r.x+r.width/2,y:r.y+r.height/2}})()`);
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', clickCount: 1, ...canvasPoint });
  await waitFor(`document.querySelector('#assistant-context').textContent.includes('正文圈选')`);
  const attachment = await evaluate(`document.querySelector('#assistant-context').textContent`);
  await evaluate(`window.wordcraftHost.engine({op:'select',range:{anchor:${JSON.stringify(selected.selection.focus)},focus:${JSON.stringify(selected.selection.focus)}},expectedDocumentId:${selected.state.documentId},expectedRevision:${selected.state.revision}})`);
  assert.equal(await evaluate(`document.querySelector('#assistant-context').textContent`), attachment);
  const third = await ask('用一句话概括这段正文。', 3);
  assert(third.text.length > 0);
  const after = await evaluate(`window.wordcraftHost.engine({op:'status'})`);
  assert.equal(after.revision, before.revision);
  assert.equal(after.dirty, before.dirty);
  result.checks.selection = { characters: selected.selection.text.length, unchangedRevision: true, unchangedDirty: true };

  const sourceFile = path.join(artifacts, 'source-example.md');
  const workspaceExcerpt = '可追溯的引用需要保留原文片段';
  const researchExcerpt = '本文件仅用于软件验证';
  await writeFile(sourceFile, '# LingDoc 公开软件样例\n\n可追溯的引用需要保留原文片段、来源标识和证据卡版本。\n本文件仅用于软件验证，不是学术证据。\n');
  const documentNode = await send('DOM.getDocument');
  const fileNode = await send('DOM.querySelector', { nodeId: documentNode.root.nodeId, selector: '#research-file' });
  await send('DOM.setFileInputFiles', { nodeId: fileNode.nodeId, files: [sourceFile] });
  await waitFor(`document.querySelector('#research-status').textContent.includes('已入库，待选择引用片段')`);
  assert(!await evaluate(`document.querySelector('#assistant-context').textContent.includes('source-example.md · 已选')`));

  await evaluate(`document.querySelector('#assistant-tools').click()`);
  await click('#assistant-add-source');
  await evaluate(`document.querySelector('#assistant-source-query').value='LingDoc'`);
  await click('#assistant-source-search');
  await waitFor(`document.querySelectorAll('#assistant-source-hits button').length>0`);
  await click('#assistant-source-hits button');
  await waitFor(`document.querySelector('#source-document-select-text')?.value.includes('LingDoc')`);
  assert(await evaluate(`document.body.dataset.side==='right' && !document.querySelector('#research-source').value`));
  await evaluate(`(()=>{const t=document.querySelector('#source-document-select-text');const text=${JSON.stringify(workspaceExcerpt)};const start=t.value.indexOf(text);if(start<0)throw Error('workspace excerpt missing');t.focus();t.setSelectionRange(start,start+text.length);t.dispatchEvent(new Event('select',{bubbles:true}));})()`);
  await screenshot('workspace-source-selection.png');
  await click('#source-document-add-excerpt');
  await waitFor(`!document.querySelector('#source-document-page') && document.querySelector('#assistant-context').textContent.includes(${JSON.stringify(workspaceExcerpt)})`);
  await evaluate(`(()=>{const q=document.querySelector('#assistant-question');q.value='多轮独立草稿';q.dispatchEvent(new Event('input',{bubbles:true}));})()`);

  await click('#research-chat-source');
  assert.equal(await evaluate(`document.querySelector('#research-ai-question').value`), '');
  await click('#research-ai-add-source');
  await evaluate(`document.querySelector('#research-ai-source-query').value='LingDoc'`);
  await click('#research-ai-source-search');
  await waitFor(`document.querySelectorAll('#research-ai-source-hits button').length>0`);
  await click('#research-ai-source-hits button');
  await waitFor(`document.querySelector('#source-document-select-text')?.value.includes('LingDoc')`);
  assert(await evaluate(`document.body.dataset.side==='right' && !document.querySelector('#research-source').value`));
  await evaluate(`(()=>{const t=document.querySelector('#source-document-select-text');const text=${JSON.stringify(researchExcerpt)};const start=t.value.indexOf(text);if(start<0)throw Error('research excerpt missing');t.focus();t.setSelectionRange(start,start+text.length);t.dispatchEvent(new Event('select',{bubbles:true}));})()`);
  await screenshot('research-source-selection.png');
  await click('#source-document-add-excerpt');
  await waitFor(`!document.querySelector('#source-document-page') && document.querySelector('#research-ai-sources').textContent.includes(${JSON.stringify(researchExcerpt)})`);
  await evaluate(`(()=>{const q=document.querySelector('#research-ai-question');q.value='资料独立草稿';q.dispatchEvent(new Event('input',{bubbles:true}));})()`);
  await click('#research-chat-workspace');
  assert.equal(await evaluate(`document.querySelector('#assistant-question').value`), '多轮独立草稿');
  assert((await evaluate(`document.querySelector('#assistant-context').textContent`)).includes(workspaceExcerpt));
  assert(!(await evaluate(`document.querySelector('#assistant-context').textContent`)).includes(researchExcerpt));
  await click('#research-chat-source');
  assert.equal(await evaluate(`document.querySelector('#research-ai-question').value`), '资料独立草稿');
  assert((await evaluate(`document.querySelector('#research-ai-sources').textContent`)).includes(researchExcerpt));
  assert(!(await evaluate(`document.querySelector('#research-ai-sources').textContent`)).includes(workspaceExcerpt));
  result.checks.independentDraftsAndSources = true;

  await click('#research-chat-workspace');
  const workspaceQuestion = '结合这段资料和正文选区，用一句话说明核对原文的重要性。';
  await evaluate(`document.querySelector('#assistant-question').value=${JSON.stringify(workspaceQuestion)}`);
  await interceptModelResponse(workspaceQuestion);
  await click('#assistant-send');
  const workspaceAborted = await abortModelResponse(workspaceQuestion);
  await navigate();
  await waitFor(`document.querySelector('#assistant-send').textContent.includes('继续读取')`);
  await click('#assistant-send');
  await waitFor(`document.querySelectorAll('#assistant-messages .assistant-turn').length===4 && document.querySelectorAll('#assistant-messages .assistant-turn')[3].querySelector('.assistant-answer')?.textContent.trim().length>0 && document.querySelector('#assistant-release').hidden && document.querySelector('#assistant-send').textContent==='发送'`);
  const fourth = await evaluate(`(()=>{const t=document.querySelectorAll('#assistant-messages .assistant-turn')[3];return{text:t.querySelector('.assistant-answer').textContent,usage:t.querySelector('.assistant-history-usage')?.textContent||''}})()`);
  assert(fourth.text.length > 0);
  result.checks.workspaceBrowserReplay = await assertBrowserReplay(workspaceQuestion, workspaceAborted);
  await evaluate(`document.querySelectorAll('#assistant-messages .assistant-turn')[3].scrollIntoView({block:'end'})`);
  await screenshot('workspace-browser-replay.png');
  const workspaceRequest = workspaceAborted.request;
  assert.deepEqual(workspaceRequest.body.source_ids, []);
  assert.equal(workspaceRequest.body.source_excerpts.length, 1);
  assert.equal(workspaceRequest.body.source_excerpts[0].quoted_text, workspaceExcerpt);
  assert.equal(Object.keys(workspaceRequest.body.source_excerpts[0]).length, 6);
  const workspaceTurn = await replay(workspaceRequest);
  assert.equal(workspaceTurn.context.source_refs[0].selection_start, workspaceRequest.body.source_excerpts[0].selection_start);
  assert.equal(workspaceTurn.context.source_refs[0].selection_end, workspaceRequest.body.source_excerpts[0].selection_end);
  assert.equal(workspaceTurn.context.source_refs[0].excerpt_text_hash, workspaceRequest.body.source_excerpts[0].excerpt_text_hash);
  assert((await evaluate(`document.querySelectorAll('#assistant-messages .assistant-turn')[3].textContent`)).includes(workspaceExcerpt));
  result.checks.workspaceExcerpt = { request: workspaceRequest.body, answer: fourth.text, backendIdempotentReplay: true };

  await click('#research-chat-source');
  const researchQuestion = '只用所选片段说明这份文件的用途。';
  await evaluate(`document.querySelector('#research-ai-question').value=${JSON.stringify(researchQuestion)}`);
  await click('#research-ai-consent');
  await interceptModelResponse(researchQuestion);
  await click('#research-ai-ask');
  const researchAborted = await abortModelResponse(researchQuestion);
  await navigate();
  await click('#research-chat-source');
  await waitFor(`document.querySelector('#research-ai-ask').textContent.includes('继续读取')`);
  await click('#research-ai-ask');
  await waitFor(`document.querySelectorAll('#research-ai-answers article').length===1 && document.querySelector('#research-ai-answers article').dataset.status==='completed' && document.querySelector('#research-ai-release').hidden && document.querySelector('#research-ai-ask').textContent==='提问'`);
  result.checks.researchBrowserReplay = await assertBrowserReplay(researchQuestion, researchAborted);
  await screenshot('research-browser-replay.png');
  const researchRequest = researchAborted.request;
  assert.deepEqual(researchRequest.body.source_ids, []);
  assert.equal(researchRequest.body.source_excerpts.length, 1);
  assert.equal(researchRequest.body.source_excerpts[0].quoted_text, researchExcerpt);
  assert.equal(Object.keys(researchRequest.body.source_excerpts[0]).length, 6);
  const researchTurn = await replay(researchRequest);
  assert.equal(researchTurn.context.source_refs[0].selection_start, researchRequest.body.source_excerpts[0].selection_start);
  assert.equal(researchTurn.context.source_refs[0].selection_end, researchRequest.body.source_excerpts[0].selection_end);
  assert.equal(researchTurn.context.source_refs[0].excerpt_text_hash, researchRequest.body.source_excerpts[0].excerpt_text_hash);
  assert((await evaluate(`document.querySelector('#research-ai-answers').textContent`)).includes(researchExcerpt));
  result.checks.researchExcerpt = { request: researchRequest.body, backendIdempotentReplay: true };
  await screenshot('research-excerpt-history.png');

  const crlfExcerpt = '不知道怎样开始';
  const crlfDocx = await createCrlfDocx();
  const crlfDocumentNode = await send('DOM.getDocument');
  const crlfFileNode = await send('DOM.querySelector', { nodeId: crlfDocumentNode.root.nodeId, selector: '#research-file' });
  await send('DOM.setFileInputFiles', { nodeId: crlfFileNode.nodeId, files: [crlfDocx] });
  await waitFor(`document.querySelector('#research-status').textContent.includes('已入库，待选择引用片段')`);
  const crlfBoot = await backend('/api/local/research/bootstrap');
  const crlfPrefix = '/api/v1/lingdoc/projects/' + encodeURIComponent(crlfBoot.projectId);
  const crlfAssets = await backend(crlfPrefix + '/assets');
  const crlfHits = await backend(crlfPrefix + '/retrieval', { query: crlfExcerpt, asset_ids: crlfAssets.map(asset => asset.id) });
  const crlfHit = crlfHits.find(hit => hit.quoted_text.includes(crlfExcerpt));
  assert(crlfHit, 'CRLF DOCX source was not retrievable');
  const crlfFixture = await convertFixtureToCrlf(crlfHit.id);
  await click('#research-ai-add-source');
  await evaluate(`document.querySelector('#research-ai-source-query').value=${JSON.stringify(crlfExcerpt)}`);
  await click('#research-ai-source-search');
  await waitFor(`document.querySelectorAll('#research-ai-source-hits button').length>0`);
  await click('#research-ai-source-hits button');
  await waitFor(`document.querySelector('#source-document-select-text')?.value.includes(${JSON.stringify(crlfExcerpt)})`);
  assert(await evaluate(`!document.querySelector('#source-document-select-text').value.includes('\\r') && document.querySelector('#source-document-select-text').value.includes('😀')`));
  await evaluate(`(()=>{const t=document.querySelector('#source-document-select-text');const text=${JSON.stringify(crlfExcerpt)};const start=t.value.indexOf(text);if(start<0)throw Error('CRLF excerpt missing');t.focus();t.setSelectionRange(start,start+text.length);t.dispatchEvent(new Event('select',{bubbles:true}));})()`);
  await screenshot('crlf-source-selection.png');
  await click('#source-document-add-excerpt');
  await waitFor(`!document.querySelector('#source-document-page') && document.querySelector('#research-ai-sources').textContent.includes(${JSON.stringify(crlfExcerpt)})`);
  await evaluate(`document.querySelector('#research-ai-question').value='请逐字复述所选片段。'`);
  await click('#research-ai-consent');
  const beforeCrlfRequest = result.requests.length;
  await click('#research-ai-ask');
  await waitNode(() => result.requests.length > beforeCrlfRequest, 'CRLF model request');
  const crlfRequest = result.requests.at(-1);
  const crlfApiExcerpt = crlfRequest.body.source_excerpts.find(item => item.quoted_text === crlfExcerpt);
  assert(crlfApiExcerpt, 'Exact CRLF source substring was not sent');
  const rawSource = crlfFixture.document.source.quoted_text;
  const expectedCrlfStart = Array.from(rawSource.slice(0, rawSource.indexOf(crlfExcerpt))).length;
  assert.equal(crlfApiExcerpt.selection_start, expectedCrlfStart);
  assert.equal(crlfApiExcerpt.selection_end, expectedCrlfStart + Array.from(crlfExcerpt).length);
  assert.equal(crlfApiExcerpt.excerpt_text_hash, createHash('sha256').update(crlfExcerpt).digest('hex'));
  assert.equal(crlfApiExcerpt.quoted_text, Array.from(rawSource).slice(expectedCrlfStart, expectedCrlfStart + Array.from(crlfExcerpt).length).join(''));
  await waitFor(`document.querySelectorAll('#research-ai-answers article').length===2 && document.querySelector('#research-ai-answers article').dataset.status==='completed'`);
  result.checks.crlfNonBmp = { rawRange: [expectedCrlfStart, expectedCrlfStart + Array.from(crlfExcerpt).length], quotedText: crlfApiExcerpt.quoted_text, hash: crlfApiExcerpt.excerpt_text_hash, modelCompleted: true };
  await screenshot('crlf-excerpt-answer.png');

  await click('#research-chat-workspace');
  await evaluate(`(()=>{const b=[...document.querySelectorAll('#assistant-context button')].find(x=>x.textContent==='移除资料');b.click()})()`);
  await waitFor(`document.querySelector('#assistant-history-notice').textContent.includes('资料或授权已变化')`);
  await click('#research-chat-source');
  assert((await evaluate(`document.querySelector('#research-ai-sources').textContent`)).includes(researchExcerpt));
  result.checks.historyExclusionVisible = await evaluate(`document.querySelector('#assistant-history-notice').textContent`);

  await navigate();
  await waitFor(`document.querySelectorAll('#assistant-messages .assistant-turn').length===4`);
  assert((await evaluate(`document.querySelector('#assistant-context').textContent`)).includes('正文圈选'));
  await click('#research-chat-source');
  const restoredResearch = await evaluate(`document.querySelector('#research-ai-sources').textContent`);
  assert(restoredResearch.includes(researchExcerpt));
  assert(!restoredResearch.includes('不是学术证据'));
  result.checks.reloadRecovered = { excerptPreserved: true, independentResearchSource: true };
  const callsBeforeRestart = result.modelTurnRequests;
  const restartPort = Number(new URL(runtime.origin).port);
  await runtime.stop();
  runtime = await startDeliveryRuntime({ runtimeDir, research: true, port: restartPort });
  await navigate();
  await waitFor(`document.querySelectorAll('#assistant-messages .assistant-turn').length===4`);
  await click('#research-chat-source');
  await waitFor(`document.querySelector('#research-ai-sources').textContent.includes(${JSON.stringify(researchExcerpt)})`);
  result.checks.restartRecovered = true;
  assert.equal(result.modelTurnRequests, callsBeforeRestart);

  const stubChangedHistorySource = async sourceId => evaluate(`(()=>{window.__originalHistoryFetch=window.fetch.bind(window);const original=window.__originalHistoryFetch;window.fetch=async(input,options={})=>{const url=typeof input==='string'?input:input.url;const method=(options.method||input.method||'GET').toUpperCase();if(method==='GET'&&url.endsWith('/sources/'+${JSON.stringify(encodeURIComponent(sourceId))})){const response=await original(input,options);const payload=await response.json();const data=payload.data??payload;data.quoted_text='CURRENT-REPLACEMENT-TEXT';data.quoted_text_hash='changed-full-hash';data.asset_revision=Number(data.asset_revision||0)+1;return new Response(JSON.stringify({...payload,data}),{status:response.status,headers:{'Content-Type':'application/json'}})}return original(input,options)}})()`);
  const restoreHistoryFetch = () => evaluate(`window.fetch=window.__originalHistoryFetch`);
  await stubChangedHistorySource(researchRequest.body.source_excerpts[0].source_id);
  await click('#research-ai-reload');
  await waitFor(`document.querySelector('#research-ai-status').textContent.includes('已从本地数据库')`);
  const changedResearchHistory = await evaluate(`document.querySelector('#research-ai-answers').textContent`);
  assert(changedResearchHistory.includes('出处已变化'));
  assert(!changedResearchHistory.includes('CURRENT-REPLACEMENT-TEXT'));
  await restoreHistoryFetch();
  await click('#research-chat-workspace');
  await stubChangedHistorySource(workspaceRequest.body.source_excerpts[0].source_id);
  await click('#assistant-reload');
  await waitFor(`document.querySelector('#assistant-status').textContent.includes('已读取已有回答')`);
  const changedWorkspaceHistory = await evaluate(`document.querySelector('#assistant-messages').textContent`);
  assert(changedWorkspaceHistory.includes('出处已变化'));
  assert(!changedWorkspaceHistory.includes('CURRENT-REPLACEMENT-TEXT'));
  await restoreHistoryFetch();
  result.checks.changedSourceHistory = { workspace: true, research: true, currentTextNotSubstituted: true };

  const legacySourceId = workspaceRequest.body.source_excerpts[0].source_id;
  const legacySource = await backend(crlfPrefix + '/sources/' + encodeURIComponent(legacySourceId));
  const workspaceSessionId = decodeURIComponent(new URL(workspaceRequest.url).pathname.split('/').at(-2));
  const researchSessionId = decodeURIComponent(new URL(researchRequest.url).pathname.split('/').at(-2));
  const legacyQuestions = {
    workspace: { before: '旧多轮待创建模型请求', after: '旧多轮已创建模型请求', restored: '旧多轮恢复附件新提问' },
    research: { before: '旧资料待创建模型请求', after: '旧资料已创建模型请求', restored: '旧资料恢复附件新提问' },
  };
  const runLegacyRequest = async (mode, kind, pending) => {
    const question = legacyQuestions[mode][kind];
    const storageKey = mode === 'workspace' ? workspaceAborted.storageKey : researchAborted.storageKey;
    const state = mode === 'workspace'
      ? { selection: null, sources: [legacySource], pending, draft: pending?.question ?? '' }
      : { sessionId: researchSessionId, selected: [legacySource], pending, draft: '', unresolved: [] };
    await evaluate(`(()=>{localStorage.setItem(${JSON.stringify(storageKey)},${JSON.stringify(JSON.stringify(state))});if(${JSON.stringify(mode)}==='workspace'){const key=Object.keys(localStorage).find(key=>key.startsWith('lingdoc-workspace-assistant:last:'));localStorage.setItem(key,${JSON.stringify(workspaceSessionId)})}})()`);
    await navigate();
    if (mode === 'research') await click('#research-chat-source');
    const inputSelector = mode === 'workspace' ? '#assistant-question' : '#research-ai-question';
    const sendSelector = mode === 'workspace' ? '#assistant-send' : '#research-ai-ask';
    await waitFor(`!document.querySelector('#research-panel').hasAttribute('aria-busy')${pending ? ` && document.querySelector('${sendSelector}').textContent.includes('继续读取')` : ''}`);
    await evaluate(`(()=>{window.__legacyRequests=[];window.__legacyFetchLog=[];const original=window.fetch.bind(window);window.fetch=async(input,options={})=>{const url=typeof input==='string'?input:input.url;const method=(options.method||input.method||'GET').toUpperCase();window.__legacyFetchLog.push(method+' '+url);if(method==='POST'&&/\\/agent-sessions\\/[^/]+\\/turns$/.test(url)){const body=JSON.parse(options.body);const request={url,body,key:options.headers?.['Idempotency-Key']};window.__legacyRequests.push(request);return new Response(JSON.stringify({data:{id:'synthetic-legacy-'+window.__legacyRequests.length,status:'running',user_input:body.user_input,context:{source_refs:[]}}}),{status:200,headers:{'Content-Type':'application/json'}})}return original(input,options)}})()`);
    if (!pending) {
      await evaluate(`document.querySelector(${JSON.stringify(mode === 'workspace' ? '#assistant-question' : '#research-ai-question')}).value=${JSON.stringify(question)}`);
      if (mode === 'research') await click('#research-ai-consent');
    }
    await evaluate(`document.querySelector(${JSON.stringify(sendSelector)}).click()`);
    try { await waitFor(`window.__legacyRequests.some(request=>request.body.user_input===${JSON.stringify(question)})`); }
    catch (error) {
      const debug = await evaluate(`({log:window.__legacyFetchLog,status:document.querySelector('#assistant-status')?.textContent,researchStatus:document.querySelector('#research-ai-status')?.textContent,busy:document.querySelector('#research-panel')?.getAttribute('aria-busy'),disabled:document.querySelector(${JSON.stringify(sendSelector)})?.disabled,stored:localStorage.getItem(${JSON.stringify(storageKey)})})`);
      throw new Error(error.message + '\nLEGACY_DEBUG ' + JSON.stringify(debug));
    }
    await waitFor(`!document.querySelector('#research-panel').hasAttribute('aria-busy')`);
    return evaluate(`window.__legacyRequests.find(request=>request.body.user_input===${JSON.stringify(question)})`);
  };
  const legacyResults = {};
  for (const mode of ['workspace', 'research']) {
    const base = { sourceIds: [legacySourceId], sourceHashes: { [legacySourceId]: legacySource.quoted_text_hash }, steps: {} };
    const before = await runLegacyRequest(mode, 'before', { ...base, question: legacyQuestions[mode].before });
    assert.deepEqual(before.body.source_ids, [legacySourceId]);
    assert(!Object.hasOwn(before.body, 'source_excerpts'));
    const legacyBody = { user_input: legacyQuestions[mode].after, source_ids: [legacySourceId], card_refs: [], sensitive_context_confirmed: true };
    const after = await runLegacyRequest(mode, 'after', { ...base, question: legacyQuestions[mode].after,
      model: { key: 'legacy-model-key-' + mode, body: legacyBody } });
    assert.deepEqual(after.body, legacyBody);
    assert.equal(after.key, 'legacy-model-key-' + mode);
    const restored = await runLegacyRequest(mode, 'restored', null);
    assert.deepEqual(restored.body.source_ids, [legacySourceId]);
    assert(!Object.hasOwn(restored.body, 'source_excerpts'));
    legacyResults[mode] = { beforeModel: true, existingModelReplay: true, restoredAttachments: true };
  }
  result.checks.legacyPendingAndAttachments = legacyResults;
  await screenshot('workspace-assistant.png');
  assert.equal(result.pageErrors.length, 0);
  result.passed = true;
} catch (error) {
  result.error = error.stack; process.exitCode = 1;
} finally {
  if (socket) try { socket.close(); } catch {}
  result.browserExited = await stopBrowser();
  if (runtime) await runtime.stop();
  await mkdir(artifacts, { recursive: true });
  await writeFile(path.join(artifacts, 'verification.json'), JSON.stringify(result, null, 2));
  console.log(JSON.stringify({ artifacts, ...result }, null, 2));
}
