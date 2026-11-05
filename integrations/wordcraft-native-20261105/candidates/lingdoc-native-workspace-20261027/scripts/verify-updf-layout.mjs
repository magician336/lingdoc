import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { createServer } from 'node:net';
import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { startDeliveryRuntime } from './delivery-runtime.mjs';
import { root, edge } from './local-environment.mjs';

const runId = new Date().toISOString().replaceAll(':', '-') + '-' + process.pid;
const artifacts = path.join(root, 'artifacts', 'updf-layout', runId);
const runtimeDir = path.join(root, 'runtime', 'updf-layout', runId);
const result = { runId, passed: false, pageErrors: [], modelRequests: [], checks: {} };
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
  const backend = async (route, body) => {
    const response = await fetch(runtime.goOrigin + route, { method: body === undefined ? 'GET' : 'POST',
      headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body) });
    const text = await response.text();
    let payload;
    try { payload = JSON.parse(text); }
    catch { assert.fail(route + ' returned ' + response.status + ' non-JSON: ' + text.slice(0, 500)); }
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
      result.modelRequests.push(JSON.parse(message.params.request.postData));
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
    throw new Error('Timed out: ' + expression + '\n' + await evaluate(`JSON.stringify({log:document.querySelector('#host-log')?.textContent?.slice(-2000),delivery:document.querySelector('#delivery-status')?.textContent,research:document.querySelector('#research-status')?.textContent,assistant:document.querySelector('#assistant-status')?.textContent,context:document.querySelector('#assistant-context')?.textContent,latest:window.wordcraftHost?.lastNonemptySelection?.selection?.text?.length})`));
  };
  await send('Page.enable'); await send('Runtime.enable'); await send('Network.enable');
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
  await send('Page.navigate', { url: runtime.origin });
  await waitFor(`window.wordcraftHost && document.querySelector('#research-toggle')`);
  await evaluate(`document.querySelector('#research-toggle').click()`);
  await waitFor(`!document.querySelector('#research-panel').hidden && !document.querySelector('#research-ai').hidden && document.querySelector('#research-ai').getBoundingClientRect().width > 0 && !document.querySelector('#research-panel').hasAttribute('aria-busy')`);
  assert.equal(await evaluate(`!!document.querySelector('#research-ai-add')`), false, 'Left-side chat handoff must be removed');
  result.checks.noLeftChatHandoff = true;
  const initial = await evaluate(`(()=>{const rail=document.querySelector('#research-rail')?.getBoundingClientRect();
    const left=document.querySelector('#research-materials')?.getBoundingClientRect();
    const right=document.querySelector('#research-ai').getBoundingClientRect();
    return {rail:rail?.width,left:left?.width,right:right?.width,
      debugVisible:!!document.querySelector('.host-debug:not([hidden])')};})()`);
  result.checks.initial = initial;
  assert.equal(initial.rail, 56);
  assert.equal(initial.left, 260);
  assert.equal(initial.right, 340);
  assert.equal(initial.debugVisible, false);
  const composer = await evaluate(`(()=>{const tools=document.querySelector('#assistant-tools');const send=document.querySelector('#assistant-send');return {
    toolsLeft:tools?.getBoundingClientRect().left,
    sendRight:send?.getBoundingClientRect().right,
    panelRight:document.querySelector('#research-ai').getBoundingClientRect().right,
    menuHidden:document.querySelector('#assistant-tools-menu')?.hidden,
    items:[...document.querySelectorAll('#assistant-tools-menu button')].map(x=>x.textContent),
    modelSelect:!!document.querySelector('#assistant-model-select')};})()`);
  result.checks.composer = composer;
  assert.equal(composer.menuHidden, true);
  assert.deepEqual(composer.items, ['上传资料', '添加资料', '添加正文选区', '引用原文']);
  assert.equal(composer.modelSelect, false);
  assert(composer.sendRight <= composer.panelRight && composer.sendRight > composer.panelRight - 30);
  const leftControls = await evaluate(`({materialsRefresh:!!document.querySelector('#research-refresh-materials'),evidenceRefresh:!!document.querySelector('#research-refresh'),leftImport:!!document.querySelector('#research-import'),fileInComposer:document.querySelector('#assistant-composer #research-file')?.hidden===true,buttonCount:[...document.querySelectorAll('#research-materials button')].filter(button=>button.getClientRects().length>0).length,placeholder:document.querySelector('#research-query').placeholder})`);
  assert.deepEqual(leftControls, { materialsRefresh: false, evidenceRefresh: false, leftImport: false, fileInComposer: true, buttonCount: 3, placeholder: '输入原文关键词，回车检索' });
  result.checks.leftControls = leftControls;
  const uploadEntry = await evaluate(`(()=>{window.__uploadEntry=0;const input=document.querySelector('#research-file');input.click=()=>{window.__uploadEntry++};document.querySelector('#assistant-tools').click();document.querySelector('#assistant-upload').click();return{calls:window.__uploadEntry,menuHidden:document.querySelector('#assistant-tools-menu').hidden}})()`);
  assert.deepEqual(uploadEntry, { calls: 1, menuHidden: true });
  result.checks.uploadEntry = uploadEntry;
  assert.equal(await evaluate(`(()=>{document.querySelector('#research-upload').click();return window.__uploadEntry})()`), 2, 'Left upload must reuse the same file picker once');
  await evaluate(`window.wordcraftHost.open()`);
  const workspaceShot = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
  await writeFile(path.join(artifacts, 'workspace.png'), Buffer.from(workspaceShot.data, 'base64'));

  const beforeViews = await evaluate(`(async()=>{window.__updfCanvas=document.querySelector('#wordcraft_canvas');return window.wordcraftHost.engine({op:'status'});})()`);
  await evaluate(`document.querySelector('#research-close').click()`);
  assert(await evaluate(`getComputedStyle(document.querySelector('#research-materials')).display==='none' && document.querySelector('#research-ai').getBoundingClientRect().width===340 && document.querySelector('#editor').getBoundingClientRect().left===0`));
  await evaluate(`document.querySelector('#workspace-materials').click();document.querySelector('#research-ai-close').click()`);
  assert(await evaluate(`getComputedStyle(document.querySelector('#research-ai')).display==='none' && document.querySelector('#research-materials').getBoundingClientRect().width===260 && document.querySelector('#editor').getBoundingClientRect().right===innerWidth`));
  await evaluate(`document.querySelector('#workspace-focus').click()`);
  assert(await evaluate(`document.querySelector('#research-panel').hidden && document.querySelector('#editor').getBoundingClientRect().top===52`));
  await evaluate(`document.querySelector('#workspace-materials').click();document.querySelector('#workspace-ai').click()`);
  result.checks.independentPanels = true;
  await evaluate(`document.querySelector('#research-ai-question').value='保留当前问题'`);
  await evaluate(`document.querySelector('#research-view-evidence').click()`);
  await waitFor(`document.querySelector('#research-view-evidence').getAttribute('aria-selected')==='true' && !document.querySelector('#research-panel').hasAttribute('aria-busy')`);
  const evidence = await evaluate(`({materials:document.querySelector('#research-materials').hidden,
    evidence:document.querySelector('#research-evidence').hidden,
    question:document.querySelector('#research-ai-question').value,
    sameCanvas:window.__updfCanvas===document.querySelector('#wordcraft_canvas')})`);
  result.checks.evidence = evidence;
  assert.equal(evidence.materials, true);
  assert.equal(evidence.evidence, false);
  assert.equal(evidence.question, '保留当前问题');
  assert.equal(evidence.sameCanvas, true);
  await evaluate(`document.querySelector('#research-view-materials').click()`);
  assert.equal(await evaluate(`document.querySelector('#research-materials').hidden`), false);
  const afterViews = await evaluate(`window.wordcraftHost.engine({op:'status'})`);
  assert.equal(afterViews.revision, beforeViews.revision);
  assert.equal(afterViews.dirty, beforeViews.dirty);

  let selected = await evaluate(`(async()=>{const state=await window.wordcraftHost.engine({op:'status'});const inspect=await window.wordcraftHost.engine({op:'inspect'});const target=inspect.blocks.find(block=>block.type==='paragraph'&&block.text?.includes('2026')&&block.len>0);const position=inspect.selection.anchor;const range={anchor:{...position,path:[target.index],off:0},focus:{...position,path:[target.index],off:target.len}};await window.wordcraftHost.engine({op:'select',range,expectedDocumentId:state.documentId,expectedRevision:state.revision});return window.wordcraftHost.engine({op:'selection'});})()`);
  assert(selected.selection.text.trim().length > 0);
  const cacheState = await evaluate(`JSON.stringify({has:'lastNonemptySelection' in window.wordcraftHost,value:window.wordcraftHost.lastNonemptySelection})`);
  assert(await evaluate(`window.wordcraftHost.lastNonemptySelection?.selection?.text?.length > 0`), 'Host did not retain the last nonempty selection: ' + cacheState);

  // Task 3: use the real upload/retrieval/saved-citation path, then prove the
  // central source page returns to this exact DOCX selection without reloading.
  const sourceText = 'Task 3 source opening.\n第二段包含出处定位标记 TASK3-MARK，供浏览器验收。';
  await evaluate(`(()=>{const input=document.querySelector('#research-file');const transfer=new DataTransfer();transfer.items.add(new File([${JSON.stringify(sourceText)}], 'task3-source.txt', {type:'text/plain'}));input.files=transfer.files;input.dispatchEvent(new Event('change',{bubbles:true}));})()`);
  await waitFor(`document.querySelector('#research-status').textContent.includes('已入库') && !document.querySelector('#research-panel').hasAttribute('aria-busy')`);

  // Task 7: both chats complete right-side search/select/ask while the left
  // excerpt and legacy handoff stay closed and unused.
  const selectRightExcerpt = async mode => {
    if (mode === 'workspace') {
      await evaluate(`document.querySelector('#research-chat-workspace').click();document.querySelector('#assistant-tools').click();document.querySelector('#assistant-add-source').click()`);
    } else {
      await evaluate(`document.querySelector('#research-chat-source').click();document.querySelector('#research-ai-add-source').click()`);
    }
    const prefix = mode === 'workspace' ? 'assistant' : 'research-ai';
    await evaluate(`document.querySelector('#${prefix}-source-query').value='TASK3-MARK';document.querySelector('#${prefix}-source-search').click()`);
    await waitFor(`document.querySelector('#${prefix}-source-hits button') && !document.querySelector('#research-panel').hasAttribute('aria-busy')`);
    await evaluate(`document.querySelector('#${prefix}-source-hits button').click()`);
    await waitFor(`document.querySelector('#source-document-select-text')?.value.includes('TASK3-MARK')`);
    await evaluate(`(()=>{const text=document.querySelector('#source-document-select-text');const start=text.value.indexOf('TASK3-MARK');text.focus();text.setSelectionRange(start,start+'TASK3-MARK'.length);text.dispatchEvent(new Event('select',{bubbles:true}));document.querySelector('#source-document-add-excerpt').click()})()`);
    const attachment = mode === 'workspace' ? '#assistant-context' : '#research-ai-sources';
    await waitFor(`!document.querySelector('#source-document-page') && document.querySelector('${attachment}').textContent.includes('TASK3-MARK') && !document.querySelector('#research-panel').hasAttribute('aria-busy')`);
    const independence = await evaluate(`({side:document.body.dataset.side,leftExcerptHidden:document.querySelector('#research-excerpt').hidden,leftSource:document.querySelector('#research-source').value,legacyEntry:!!document.querySelector('#research-ai-add')})`);
    assert.deepEqual(independence, { side: 'right', leftExcerptHidden: true, leftSource: '', legacyEntry: false });
  };
  await selectRightExcerpt('workspace');
  await evaluate(`(()=>{const q=document.querySelector('#assistant-question');q.value='只根据附件，用一句话说明 TASK3-MARK 的用途。';q.dispatchEvent(new Event('input',{bubbles:true}));document.querySelector('#assistant-composer').requestSubmit()})()`);
  await waitFor(`document.querySelectorAll('#assistant-messages .assistant-turn').length===1 && document.querySelector('#assistant-messages .assistant-answer')?.textContent.trim() && !document.querySelector('#research-panel').hasAttribute('aria-busy')`);
  await selectRightExcerpt('source');
  await evaluate(`document.querySelector('#research-ai-question').value='只根据附件，用一句话说明 TASK3-MARK 的用途。';document.querySelector('#research-ai-consent').click();document.querySelector('#research-ai-ask').click()`);
  await waitFor(`document.querySelectorAll('#research-ai-answers article').length===1 && document.querySelector('#research-ai-answers article').dataset.status==='completed' && !document.querySelector('#research-panel').hasAttribute('aria-busy')`);
  assert.equal(result.modelRequests.length, 2);
  for (const request of result.modelRequests) {
    assert.deepEqual(request.source_ids, []);
    assert.equal(request.source_excerpts.length, 1);
    assert.equal(request.source_excerpts[0].quoted_text, 'TASK3-MARK');
  }
  assert(await evaluate(`document.querySelector('#research-excerpt').hidden && !document.querySelector('#research-source').value`));
  result.checks.independentRightChats = { modes: ['workspace', 'source'], exactExcerpt: 'TASK3-MARK', leftExcerptRemainedClosed: true };
  const independentChatsShot = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
  await writeFile(path.join(artifacts, 'independent-right-chats.png'), Buffer.from(independentChatsShot.data, 'base64'));

  const researchBoot = await backend('/api/local/research/bootstrap');
  const projectPrefix = '/api/v1/lingdoc/projects/' + encodeURIComponent(researchBoot.projectId);
  const projectAssets = await backend(projectPrefix + '/assets');
  const sourceHits = await backend(projectPrefix + '/retrieval', { query: 'TASK3-MARK', asset_ids: projectAssets.map(asset => asset.id) });
  assert.equal(sourceHits.length, 1);
  assert(sourceHits[0].quoted_text.includes('TASK3-MARK'));
  const sourceDocument = await backend(projectPrefix + '/sources/' + encodeURIComponent(sourceHits[0].id) + '/document');
  assert(sourceDocument.segments.some(segment => segment.source_id === sourceHits[0].id));
  await evaluate(`document.querySelector('#research-query').value='TASK3-MARK';document.querySelector('#research-search').click()`);
  await waitFor(`document.querySelector('#research-hits button') && !document.querySelector('#research-panel').hasAttribute('aria-busy')`);
  await evaluate(`window.wordcraftHost.research.restore([{sectionId:${JSON.stringify(researchBoot.chapters[0].sectionId)},sourceId:${JSON.stringify(sourceHits[0].id)},evidenceCardId:'task3-card',evidenceCardVersion:1}])`);
  await waitFor(`document.querySelector('#research-citations button') && !document.querySelector('#research-panel').hasAttribute('aria-busy')`);
  selected = await evaluate(`(async()=>{const state=await window.wordcraftHost.engine({op:'status'});await window.wordcraftHost.engine({op:'select',range:${JSON.stringify({ anchor: selected.selection.anchor, focus: selected.selection.focus })},expectedDocumentId:state.documentId,expectedRevision:state.revision});return window.wordcraftHost.engine({op:'selection'});})()`);
  await evaluate(`document.querySelector('#research-citations button').click()`);
  await waitFor(`!document.querySelector('#research-panel').hasAttribute('aria-busy')`);
  const sourceAcceptance = await evaluate(`(()=>{const page=document.querySelector('#source-document-page');const tooSmall=[...document.querySelectorAll('#research-panel *, #source-document-page *')].filter(el=>el.getClientRects().length&&parseFloat(getComputedStyle(el).fontSize)<13).map(el=>({id:el.id,className:el.className,size:getComputedStyle(el).fontSize}));const tooSmallControls=[...document.querySelectorAll('#research-panel button, #research-panel input, #research-panel select, #research-panel textarea, #source-document-page button, #source-document-page input, #source-document-page select, #source-document-page textarea, #citation-mode-bar button')].filter(el=>el.getClientRects().length&&parseFloat(getComputedStyle(el).fontSize)<14).map(el=>({id:el.id,className:el.className,size:getComputedStyle(el).fontSize}));return{pageExists:!!page,tooSmall,tooSmallControls}})()`);
  result.checks.sourceAcceptance = sourceAcceptance;
  assert(sourceAcceptance.pageExists, 'Central source page is missing; visible controls below 13px: ' + JSON.stringify(sourceAcceptance.tooSmall));
  assert.deepEqual(sourceAcceptance.tooSmall, []);
  assert.deepEqual(sourceAcceptance.tooSmallControls, []);
  await waitFor(`document.querySelector('#source-document-target')`);
  const located = await evaluate(`(()=>{const page=document.querySelector('#source-document-page');const mark=document.querySelector('#source-document-target');return{target:mark.textContent,parent:page.parentElement.id,sameCanvas:window.__updfCanvas===document.querySelector('#wordcraft_canvas'),pulse:mark.classList.contains('source-location-pulse'),iterations:getComputedStyle(mark).animationIterationCount}})()`);
  assert.equal(located.target, sourceHits[0].quoted_text);
  assert.equal(located.parent, 'editor');
  assert(located.sameCanvas && located.pulse);
  assert.equal(located.iterations, '1');
  const sourceShot = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
  await writeFile(path.join(artifacts, 'source-document.png'), Buffer.from(sourceShot.data, 'base64'));
  await sleep(1650);
  assert.equal(await evaluate(`document.querySelector('#source-document-target').classList.contains('source-location-pulse')`), false);
  const collapsed = await evaluate(`(async()=>{const state=await window.wordcraftHost.engine({op:'status'});const anchor=${JSON.stringify(selected.selection.anchor)};await window.wordcraftHost.engine({op:'select',range:{anchor,focus:anchor},expectedDocumentId:state.documentId,expectedRevision:state.revision});return window.wordcraftHost.engine({op:'selection'});})()`);
  assert.deepEqual(collapsed.selection.focus, collapsed.selection.anchor);
  await evaluate(`document.querySelector('#source-document-close').click()`);
  await waitFor(`!document.querySelector('#source-document-page')`);
  const returned = await evaluate(`(async()=>{const current=await window.wordcraftHost.engine({op:'selection'});return{current,sameCanvas:window.__updfCanvas===document.querySelector('#wordcraft_canvas')}})()`);
  assert(returned.sameCanvas);
  assert.deepEqual(returned.current.selection.anchor, selected.selection.anchor);
  assert.deepEqual(returned.current.selection.focus, selected.selection.focus);
  assert.equal(returned.current.state.documentId, selected.state.documentId);
  assert.equal(returned.current.state.revision, selected.state.revision);
  assert.equal(returned.current.state.dirty, selected.state.dirty);
  result.checks.sourceReturn = { sameCanvas: true, exactRange: true, revision: returned.current.state.revision, dirty: returned.current.state.dirty, noEditOrReload: true };
  await evaluate(`document.querySelector('#research-view-materials').click()`);
  const remainingSmallText = await evaluate(`[...document.querySelectorAll('#research-panel *, #source-document-page *')].filter(el=>el.getClientRects().length&&parseFloat(getComputedStyle(el).fontSize)<13).map(el=>({id:el.id,className:el.className,size:getComputedStyle(el).fontSize}))`);
  assert.deepEqual(remainingSmallText, []);
  result.checks.webFontMinimum = { sourceAndEvidence: [], materialsAndAssistant: [] };
  await sleep(450);
  await evaluate(`document.querySelector('#assistant-tools').click();document.querySelector('#assistant-selection').click()`);
  await waitFor(`document.body.classList.contains('citation-selection-mode')`);
  const canvasPoint = await evaluate(`(()=>{const r=document.querySelector('#wordcraft_canvas').getBoundingClientRect();return{x:r.x+r.width/2,y:r.y+r.height/2}})()`);
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', clickCount: 1, ...canvasPoint });
  await waitFor(`document.querySelector('#assistant-context').textContent.includes('正文圈选')`);
  const afterSelection = await evaluate(`window.wordcraftHost.engine({op:'status'})`);
  assert.equal(afterSelection.revision, selected.state.revision);
  assert.equal(afterSelection.dirty, selected.state.dirty);
  result.checks.selectionContext = { characters: selected.selection.text.length, unchanged: true };
  await evaluate(`document.querySelector('#assistant-cite').click()`);
  await waitFor(`document.body.classList.contains('citation-selection-mode')`);
  const visibleSmallControls = await evaluate(`[...document.querySelectorAll('#research-panel button, #research-panel input, #research-panel select, #research-panel textarea, #source-document-page button, #source-document-page input, #source-document-page select, #source-document-page textarea')].filter(el=>el.getClientRects().length&&parseFloat(getComputedStyle(el).fontSize)<14).map(el=>({id:el.id,className:el.className,size:getComputedStyle(el).fontSize}))`);
  const citationModeSmallControls = await evaluate(`[...document.querySelectorAll('#citation-mode-bar button')].filter(el=>el.getClientRects().length&&parseFloat(getComputedStyle(el).fontSize)<14).map(el=>({id:el.id,className:el.className,size:getComputedStyle(el).fontSize}))`);
  result.checks.controlFontMinimum = { sourceAndRail: visibleSmallControls, citationMode: citationModeSmallControls };
  assert.deepEqual([...visibleSmallControls, ...citationModeSmallControls], [], 'Visible controls below 14px');
  await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
  await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
  assert.equal(await evaluate(`document.body.classList.contains('citation-selection-mode')`), false);
  assert((await evaluate(`document.querySelector('#assistant-context').textContent`)).includes('正文圈选'));
  result.checks.citationCancel = { escape: true, existingSelectionPreserved: true };
  await evaluate(`document.querySelector('#assistant-cite').click()`);
  await waitFor(`document.body.classList.contains('citation-selection-mode')`);
  await sleep(1000);
  const marginPoint = await evaluate(`(async()=>{const canvas=document.querySelector('#wordcraft_canvas');const rect=canvas.getBoundingClientRect();const layout=await window.wordcraftHost.engine({op:'layout'});const candidates=[{x:2,y:2},{x:canvas.width-2,y:2},{x:2,y:canvas.height-2},{x:canvas.width-2,y:canvas.height-2}];const point=candidates.find(candidate=>!(layout.pages??[]).some(page=>{const left=page.x??0,top=page.y??0,width=page.w??page.width,height=page.h??page.height;return Number.isFinite(width)&&Number.isFinite(height)&&candidate.x>=left&&candidate.x<=left+width&&candidate.y>=top&&candidate.y<=top+height}));if(!point)throw Error('no canvas margin available');return{x:rect.left+point.x*rect.width/canvas.width,y:rect.top+point.y*rect.height/canvas.height}})()`);
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', button: 'left', clickCount: 1, ...marginPoint });
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', clickCount: 1, ...marginPoint });
  assert.equal(await evaluate(`document.body.classList.contains('citation-selection-mode')`), false);
  assert((await evaluate(`document.querySelector('#assistant-context').textContent`)).includes('正文圈选'));
  result.checks.citationMarginCancel = { outsidePage: true, existingSelectionPreserved: true };

  await evaluate(`document.querySelector('#research-chat-workspace').click()`);
  const turnsBeforeKeyboard = await evaluate(`document.querySelectorAll('.assistant-turn').length`);
  await evaluate(`(()=>{window.__submitCalls=0;document.querySelector('#assistant-composer').requestSubmit=()=>{window.__submitCalls++};const q=document.querySelector('#assistant-question');q.value='第一行';q.focus();q.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',shiftKey:true,cancelable:true}));})()`);
  assert.equal(await evaluate(`window.__submitCalls`), 0);
  await send('Input.insertText', { text: '\n' });
  assert(await evaluate(`document.querySelector('#assistant-question').value.includes(String.fromCharCode(10))`));
  await evaluate(`(()=>{const q=document.querySelector('#assistant-question');q.dispatchEvent(new CompositionEvent('compositionstart',{data:'中'}));q.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',isComposing:true,cancelable:true}));q.dispatchEvent(new CompositionEvent('compositionend',{data:'中'}));q.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',cancelable:true}));})()`);
  assert.equal(await evaluate(`window.__submitCalls`), 1);
  assert.equal(await evaluate(`document.querySelectorAll('.assistant-turn').length`), turnsBeforeKeyboard);
  await evaluate(`document.querySelector('#assistant-question').value='保留多轮草稿';document.querySelector('#research-chat-source').click();document.querySelector('#research-chat-workspace').click()`);
  assert.equal(await evaluate(`document.querySelector('#assistant-question').value`), '保留多轮草稿');
  result.checks.keyboard = { shiftEnterNewline: true, imeNoSend: true, draftPreserved: true };

  await evaluate(`(()=>{window.__selectionReads=0;const original=window.wasmBindings.host_request;window.__originalHostRequest=original;window.wasmBindings.host_request=request=>{if(JSON.parse(request).op==='selection')window.__selectionReads++;return original(request)};document.querySelector('#research-chat-source').click();document.querySelector('#research-chat-workspace').click();document.querySelector('#assistant-tools').click();})()`);
  await sleep(500);
  const selectionReads = await evaluate('window.__selectionReads');
  await evaluate(`window.wasmBindings.host_request=window.__originalHostRequest`);
  assert.equal(selectionReads, 0, 'Sidebar interactions must not poll the editor selection');

  await send('Emulation.setDeviceMetricsOverride', { width: 1024, height: 768, deviceScaleFactor: 1, mobile: false });
  await evaluate(`document.querySelector('#research-view-evidence').click()`);
  await waitFor(`!document.querySelector('#research-panel').hasAttribute('aria-busy')`);
  const narrow = await evaluate(`({left:getComputedStyle(document.querySelector('#research-evidence')).display,
    right:getComputedStyle(document.querySelector('#research-ai')).display,
    bounds:document.querySelector('#research-evidence').getBoundingClientRect().right})`);
  result.checks.narrow = narrow;
  assert.notEqual(narrow.left, 'none');
  assert.equal(narrow.right, 'none');
  assert(narrow.bounds <= 1024, 'Narrow panel escaped viewport');
  await evaluate(`document.querySelector('#research-view-materials').click()`);
  await send('Emulation.setDeviceMetricsOverride', { width: 900, height: 950, deviceScaleFactor: 1, mobile: false });
  await evaluate(`document.querySelector('#workspace-ai').click();document.querySelector('#assistant-tools').click()`);
  assert(await evaluate(`getComputedStyle(document.querySelector('#research-ai')).display!=='none' && getComputedStyle(document.querySelector('#research-materials')).display==='none'`));
  const menuBounds = await evaluate(`({right:document.querySelector('#assistant-tools-menu').getBoundingClientRect().right,sendRight:document.querySelector('#assistant-send').getBoundingClientRect().right,viewport:innerWidth})`);
  assert(menuBounds.right > 0 && menuBounds.right <= menuBounds.viewport && menuBounds.sendRight > 0 && menuBounds.sendRight <= menuBounds.viewport);
  result.checks.narrowComposer = menuBounds;
  await evaluate(`document.querySelector('#assistant-cite').click()`);
  await waitFor(`document.body.classList.contains('citation-selection-mode')`);
  await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
  await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
  assert(await evaluate(`!document.body.classList.contains('citation-selection-mode') && getComputedStyle(document.querySelector('#research-ai')).display!=='none' && document.querySelector('#workspace-ai').getAttribute('aria-pressed')==='true'`));
  result.checks.narrowCitationReturn = true;
  assert.equal(await evaluate(`document.querySelector('#editor').getBoundingClientRect().left`), 56, 'Narrow rail must not cover native editor controls');
  const narrowShot = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
  await writeFile(path.join(artifacts, 'narrow-workspace.png'), Buffer.from(narrowShot.data, 'base64'));
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });

  await evaluate(`document.querySelector('#workspace-focus').click();document.querySelector('#workspace-save').click()`);
  await waitFor(`!document.querySelector('#workspace-save').disabled`);
  const navigationSave = await evaluate(`({text:document.querySelector('#workspace-save-status').textContent,sources:window.lingdocDiagnostics().saveSources,receipt:!!window.wordcraftHost.lastSave?.receipt,focused:document.querySelector('#research-panel').hidden})`);
  assert.equal(navigationSave.text, '文稿与引用已保存');
  assert(navigationSave.sources.includes('workspace-navigation') && navigationSave.receipt && navigationSave.focused);
  result.checks.navigationSave = navigationSave;
  const focusShot = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
  await writeFile(path.join(artifacts, 'focus-writing.png'), Buffer.from(focusShot.data, 'base64'));
  await waitFor(`document.querySelector('#workspace-save-status').textContent===''`);
  result.checks.saveReceiptExpires = true;
  await send('Emulation.setDeviceMetricsOverride', { width: 375, height: 812, deviceScaleFactor: 1, mobile: false });
  assert(await evaluate(`document.querySelector('#workspace-save').getBoundingClientRect().right<=innerWidth && document.querySelector('.workspace-views').clientWidth>0 && document.querySelector('.workspace-views').scrollWidth>=document.querySelector('.workspace-views').clientWidth`));
  result.checks.smallNavigation = true;
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
  await evaluate(`document.querySelector('#workspace-materials').click();document.querySelector('#workspace-ai').click()`);

  await evaluate(`window.wordcraftHost.prepareDelivery()`);
  // Accept either the fixture's valid saved version or its structural rejection;
  // both default presentations must stay user-facing instead of exposing internals.
  await waitFor(`document.querySelector('#delivery-version')?.textContent === '当前保存版本已校验' || document.querySelector('#delivery-status')?.textContent.includes('STRUCTURE_INVALID')`);
  const versionText = await evaluate(`document.querySelector('#delivery-version').textContent`);
  result.checks.versionText = versionText;
  assert(!/SHA-256|saveToken|Go|DS|host-document|[0-9a-f]{8}-[0-9a-f-]{20,}/i.test(versionText));
  await evaluate(`document.querySelector('#delivery-close').click()`);

  await send('Page.navigate', { url: runtime.origin + '/?debug=1' });
  await waitFor(`window.wordcraftHost && !!document.querySelector('.host-debug:not([hidden])')`);
  result.checks.debugMode = await evaluate(`({visible:!!document.querySelector('.host-debug:not([hidden])'),body:document.body.classList.contains('debug-mode')})`);
  assert(result.checks.debugMode.visible && result.checks.debugMode.body, 'Debug mode did not activate');
  await waitFor(`window.wordcraftHost.research && document.querySelector('#research-citations') && (()=>{try{return !!JSON.parse(document.querySelector('#host-log').textContent).opened}catch{return false}})()`);

  // Make a real revision after opening the source. The stale return must retain
  // the old text snapshot and leave the post-edit selection exactly where it is.
  await evaluate(`window.wordcraftHost.research.restore([{sectionId:${JSON.stringify(researchBoot.chapters[0].sectionId)},sourceId:${JSON.stringify(sourceHits[0].id)},evidenceCardId:'task3-card',evidenceCardVersion:1}])`);
  await evaluate(`document.querySelector('#research-view-evidence').click()`);
  await waitFor(`document.querySelector('#research-citations button') && !document.querySelector('#research-panel').hasAttribute('aria-busy')`);
  await evaluate(`(async()=>{const state=await window.wordcraftHost.engine({op:'status'});await window.wordcraftHost.engine({op:'select',range:${JSON.stringify({ anchor: selected.selection.anchor, focus: selected.selection.focus })},expectedDocumentId:state.documentId,expectedRevision:state.revision});document.querySelector('#research-citations button').click();})()`);
  await waitFor(`document.querySelector('#source-document-target') && !document.querySelector('#research-panel').hasAttribute('aria-busy')`);
  const postEdit = await evaluate(`(async()=>{const current=await window.wordcraftHost.engine({op:'selection'});await window.wordcraftHost.engine({op:'insert',text:'TASK3-STALE',bookmark:current.selection,expectedDocumentId:current.state.documentId,expectedRevision:current.state.revision});return window.wordcraftHost.engine({op:'selection'});})()`);
  await evaluate(`document.querySelector('#source-document-close').click()`);
  await waitFor(`document.querySelector('#source-document-status')?.textContent.includes('正文已变化，无法恢复原位置')`);
  const stale = await evaluate(`(async()=>{const current=await window.wordcraftHost.engine({op:'selection'});return{snapshot:document.querySelector('#source-document-return-snapshot').textContent,message:document.querySelector('#source-document-status').textContent,visible:!!document.querySelector('#source-document-page'),current}})()`);
  assert(stale.visible);
  assert.equal(stale.snapshot, selected.selection.text);
  assert(stale.message.includes('正文已变化，无法恢复原位置'));
  assert.deepEqual(stale.current.selection.anchor, postEdit.selection.anchor);
  assert.deepEqual(stale.current.selection.focus, postEdit.selection.focus);
  assert.equal(stale.current.state.revision, postEdit.state.revision);
  result.checks.staleReturn = { snapshotRetained: true, message: stale.message, noApproximateSelect: true, revision: stale.current.state.revision };
  await evaluate(`document.querySelector('#source-document-dismiss').click()`);
  await waitFor(`!document.querySelector('#source-document-page')`);
  const editorPoint = await evaluate(`(()=>{const r=document.querySelector('#wordcraft_canvas').getBoundingClientRect();return{x:r.x+r.width/2,y:r.y+Math.max(250,r.height*.7)}})()`);
  await send('Input.dispatchMouseEvent', { type: 'mouseMoved', ...editorPoint });
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', button: 'left', clickCount: 1, ...editorPoint });
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', clickCount: 1, ...editorPoint });
  await sleep(100);
  await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Control', code: 'ControlLeft', modifiers: 2 });
  await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'z', code: 'KeyZ', windowsVirtualKeyCode: 90, modifiers: 2 });
  await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'z', code: 'KeyZ', windowsVirtualKeyCode: 90, modifiers: 2 });
  await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Control', code: 'ControlLeft', modifiers: 0 });
  await sleep(500);
  const staleCleanup = await evaluate(`(async()=>{const inspect=await window.wordcraftHost.engine({op:'inspect'});const state=await window.wordcraftHost.engine({op:'status'});return{undoRestoredText:inspect.blocks.every(block=>!block.text?.includes('TASK3-STALE')),state,texts:inspect.blocks.filter(block=>block.text?.includes('TASK3-STALE')).map(block=>block.text)}})()`);
  result.checks.staleCleanup = staleCleanup;
  assert(staleCleanup.undoRestoredText, 'Stale edit cleanup failed: ' + JSON.stringify(staleCleanup));

  // Task 7: an already-dirty DOCX survives source open/return with the same
  // canvas and revision, and its existing undo history still removes the edit.
  const unsavedBeforeOpen = await evaluate(`(async()=>{window.__task7Canvas=document.querySelector('#wordcraft_canvas');const current=await window.wordcraftHost.engine({op:'selection'});await window.wordcraftHost.engine({op:'insert',text:'TASK7-UNSAVED',bookmark:current.selection,expectedDocumentId:current.state.documentId,expectedRevision:current.state.revision});return window.wordcraftHost.engine({op:'status'})})()`);
  assert.equal(unsavedBeforeOpen.dirty, true);
  await evaluate(`document.querySelector('#workspace-focus').click();document.querySelector('#workspace-materials').click();document.querySelector('#workspace-ai').click();document.querySelector('#workspace-evidence').click()`);
  await waitFor(`!document.querySelector('#research-panel').hasAttribute('aria-busy')`);
  assert.deepEqual(await evaluate(`window.wordcraftHost.engine({op:'status'})`), unsavedBeforeOpen, 'Navigation must preserve dirty document state');
  result.checks.unsavedNavigation = true;
  await evaluate(`document.querySelector('#research-citations button').click()`);
  await waitFor(`document.querySelector('#source-document-target') && !document.querySelector('#research-panel').hasAttribute('aria-busy')`);
  await evaluate(`document.querySelector('#source-document-close').click()`);
  await waitFor(`!document.querySelector('#source-document-page')`);
  const unsavedReturned = await evaluate(`(async()=>{const state=await window.wordcraftHost.engine({op:'status'});const inspect=await window.wordcraftHost.engine({op:'inspect'});return{state,sameCanvas:window.__task7Canvas===document.querySelector('#wordcraft_canvas'),marker:inspect.blocks.some(block=>block.text?.includes('TASK7-UNSAVED'))}})()`);
  assert.equal(unsavedReturned.state.revision, unsavedBeforeOpen.revision);
  assert.equal(unsavedReturned.state.dirty, true);
  assert(unsavedReturned.sameCanvas && unsavedReturned.marker);
  const unsavedShot = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
  await writeFile(path.join(artifacts, 'unsaved-source-return.png'), Buffer.from(unsavedShot.data, 'base64'));
  const unsavedEditorPoint = await evaluate(`(()=>{const r=document.querySelector('#wordcraft_canvas').getBoundingClientRect();return{x:r.x+r.width/2,y:r.y+Math.max(250,r.height*.7)}})()`);
  await send('Input.dispatchMouseEvent', { type: 'mouseMoved', ...unsavedEditorPoint });
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', button: 'left', clickCount: 1, ...unsavedEditorPoint });
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', clickCount: 1, ...unsavedEditorPoint });
  await sleep(100);
  await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Control', code: 'ControlLeft', modifiers: 2 });
  await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'z', code: 'KeyZ', windowsVirtualKeyCode: 90, modifiers: 2 });
  await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'z', code: 'KeyZ', windowsVirtualKeyCode: 90, modifiers: 2 });
  await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Control', code: 'ControlLeft', modifiers: 0 });
  await sleep(500);
  assert(await evaluate(`window.wordcraftHost.engine({op:'inspect'}).then(inspect=>inspect.blocks.every(block=>!block.text?.includes('TASK7-UNSAVED')))`));
  result.checks.unsavedSourceReturn = { revision: unsavedReturned.state.revision, dirty: true, sameCanvas: true, undoPreserved: true };

  // Repeated engine-status failures must still offer a safe way off the source
  // page, without selecting or claiming that the dirty DOCX is stale.
  const closeFailureBefore = await evaluate(`(async()=>{const current=await window.wordcraftHost.engine({op:'selection'});await window.wordcraftHost.engine({op:'insert',text:'TASK7-CLOSE-UNSAVED',bookmark:current.selection,expectedDocumentId:current.state.documentId,expectedRevision:current.state.revision});return window.wordcraftHost.engine({op:'status'})})()`);
  assert.equal(closeFailureBefore.dirty, true);
  const failedClose = await evaluate(`(async()=>{const {createSourceDocumentPage}=await import('/source-document-page.js');const realEngine=window.wordcraftHost.engine;let statusFailures=0,selectCalls=0;const engine=async request=>{if(request.op==='status'){statusFailures++;throw new Error('simulated repeated status failure')}if(request.op==='select')selectCalls++;return realEngine(request)};const document=${JSON.stringify(sourceDocument)};const page=createSourceDocumentPage({request:async()=>document,projectPath:suffix=>suffix,engine});await page.open({sourceId:${JSON.stringify(sourceHits[0].id)},mode:'read'});await page.close();await page.close();const dismiss=window.document.querySelector('#source-document-dismiss');return{pageVisible:!!window.document.querySelector('#source-document-page'),dismissVisible:!!dismiss?.getClientRects().length,message:window.document.querySelector('#source-document-status').textContent,statusFailures,selectCalls}})()`);
  assert(failedClose.pageVisible && failedClose.dismissVisible, 'No-restore dismiss is not available after repeated status failures');
  assert.equal(failedClose.statusFailures, 2);
  assert(!failedClose.message.includes('正文已变化'));
  assert.equal(failedClose.selectCalls, 0);
  const failureShot = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
  await writeFile(path.join(artifacts, 'source-close-status-failure.png'), Buffer.from(failureShot.data, 'base64'));
  await evaluate(`document.querySelector('#source-document-dismiss').click()`);
  await waitFor(`!document.querySelector('#source-document-page')`);
  const failedCloseReturned = await evaluate(`(async()=>{const state=await window.wordcraftHost.engine({op:'status'});const inspect=await window.wordcraftHost.engine({op:'inspect'});return{state,sameCanvas:window.__task7Canvas===document.querySelector('#wordcraft_canvas'),marker:inspect.blocks.some(block=>block.text?.includes('TASK7-CLOSE-UNSAVED'))}})()`);
  result.checks.repeatedCloseStatusFailureDebug = { failedClose, failedCloseReturned, expectedRevision: closeFailureBefore.revision };
  assert.equal(failedCloseReturned.state.revision, closeFailureBefore.revision);
  assert.equal(failedCloseReturned.state.dirty, true);
  assert(failedCloseReturned.sameCanvas && failedCloseReturned.marker);
  result.checks.repeatedCloseStatusFailure = { dismissVisible: true, noSelect: true, unsavedContentPreserved: true, revision: failedCloseReturned.state.revision };

  // Deterministically hold A's status read until B is fully visible. A's stale
  // close continuation must neither select over nor dismiss the newer B page.
  const closeRace = await evaluate(`(async()=>{
    const {createSourceDocumentPage}=await import('/source-document-page.js');
    const state={documentId:91,revision:7,dirty:false};
    const anchor={story:'body',path:[0],off:0},focus={story:'body',path:[0],off:1};
    const selection={state,selection:{text:'saved',anchor,focus}};
    let hold=false,releaseStatus=null,selectCalls=0;
    const engine=async request=>{
      if(request.op==='selection')return selection;
      if(request.op==='status'){if(hold)return new Promise(resolve=>{releaseStatus=()=>resolve(state)});return state;}
      if(request.op==='select'){selectCalls++;return state;}
      throw new Error('unexpected engine operation '+request.op);
    };
    const documents={A:{source:{quoted_text:'Alpha',locator:'A'},text:'Alpha',segments:[{source_id:'A',chunk_index:0,start_at:0,end_at:5}]},B:{source:{quoted_text:'Beta',locator:'B'},text:'Beta',segments:[{source_id:'B',chunk_index:0,start_at:0,end_at:4}]}};
    const page=createSourceDocumentPage({request:async url=>url.includes('/A/')?documents.A:documents.B,projectPath:suffix=>'/project'+suffix,engine});
    await page.open({sourceId:'A',mode:'read'});
    hold=true;
    const closingA=page.close();
    await page.open({sourceId:'B',mode:'read'});
    releaseStatus();
    await closingA;
    const after={exists:!!document.querySelector('#source-document-page'),title:document.querySelector('#source-document-title')?.textContent||null,selectCalls};
    hold=false;
    await page.close();
    return{after,cleaned:!document.querySelector('#source-document-page')};
  })()`);
  result.checks.closeRace = closeRace;
  assert.deepEqual(closeRace.after, { exists: true, title: 'B', selectCalls: 0 }, 'Closing A altered newer source B');
  assert(closeRace.cleaned, 'Source race fixture did not clean up B');
  assert(result.pageErrors.length === 0, 'Browser exceptions occurred');
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
