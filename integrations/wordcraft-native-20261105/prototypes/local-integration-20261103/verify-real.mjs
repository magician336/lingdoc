// Focused Save As regression: real WASM/Go/SQLite/DS/Edge, isolated user data.
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { createServer } from 'node:net';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { startDeliveryRuntime } from '../../candidates/lingdoc-native-workspace-20261027/scripts/delivery-runtime.mjs';
import { root, edge } from '../../candidates/lingdoc-native-workspace-20261027/scripts/local-environment.mjs';

const runId = `${new Date().toISOString().replaceAll(':', '-')}-${process.pid}`;
const localRoot = new URL('.', import.meta.url);
const artifacts = new URL('artifacts/' + runId + '/', localRoot).pathname.replace(/^\/([A-Za-z]:)/,'$1');
const runtimeDir = new URL('runtime/' + runId + '/', localRoot).pathname.replace(/^\/([A-Za-z]:)/,'$1');
const result = { runId, kind: 'real Go/SQLite/DS/WASM/Edge', baselineId:'LD-V1-20261103-R1', passed: false, checks:[], pageErrors: [] };
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
  browser = spawn(edge, ['--headless=new', '--disable-extensions', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
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
    throw new Error(`Timed out: ${expression}; ${await evaluate(`document.querySelector('#host-log')?.textContent`)}`);
  };
  const clickAt = async (x, y) => {
    await send('Input.dispatchMouseEvent', { type: 'mouseMoved', x, y });
    await send('Input.dispatchMouseEvent', { type: 'mousePressed', button: 'left', clickCount: 1, x, y });
    await send('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', clickCount: 1, x, y });
  };
  const click = async selector => {
    const point = await evaluate(`(()=>{const b=document.querySelector(${JSON.stringify(selector)});if(!b||b.disabled)throw Error('button unavailable');const r=b.getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()`);
    await clickAt(point.x, point.y);
  };
  const key = async (key, code, modifiers = 0) => {
    await send('Input.dispatchKeyEvent', { type: 'keyDown', key, code, modifiers });
    await send('Input.dispatchKeyEvent', { type: 'keyUp', key, code, modifiers: 0 });
  };
  const screenshot = async name => {
    const shot = await send('Page.captureScreenshot', { format: 'png' });
    await writeFile(path.join(artifacts, name), Buffer.from(shot.data, 'base64'));
  };
  const state = () => evaluate(`(async()=>({engine:await window.wordcraftHost.engine({op:'status'}),diskVersion:window.wordcraftHost.diskVersion,goDiskVersion:window.wordcraftHost.goDiskVersion,calls:[...window.__saveAsCalls]}))()`);
  await send('Page.enable'); await send('Runtime.enable');
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
  await send('Browser.setDownloadBehavior', { behavior: 'allow', downloadPath: artifacts });
  await send('Page.navigate', { url: runtime.origin });
  await waitFor(`(()=>{try{return !!JSON.parse(document.querySelector('#host-log').textContent).opened}catch{return false}})()`);

  const moduleSource = (await readFile(new URL('native-adapter.js',localRoot),'utf8')).replaceAll('export const ','const ').replaceAll('export function ','function ');
  const install = async()=>evaluate(`(()=>{${moduleSource}\nwindow.integrationAdapter=createNativeAdapter({hostWindow:window});return integrationAdapter.refresh()})()`);
  const addText = text=>evaluate(`(async()=>{const s=await wordcraftHost.engine({op:'selection'});await wordcraftHost.engine({op:'insert',text:${JSON.stringify(text)},bookmark:s.selection,expectedDocumentId:s.state.documentId,expectedRevision:s.state.revision});return integrationAdapter.refresh()})()`);
  const verify = async (expression,label)=>{assert(await evaluate(expression),label);result.checks.push(label)};
  await install();
  await verify(`integrationAdapter.getState().phase==='not_registered'`,'initial clean fixture is not falsely called a registered save');
  await addText('LOCAL-INTEGRATION-FIRST-20261103');
  await evaluate(`(()=>{window.originalFetch=window.fetch;window.held=false;window.fetch=(input,options)=>{if(String(input)==='/api/save'&&!window.held){window.held=true;return new Promise((resolve,reject)=>{window.releaseDS=()=>originalFetch(input,options).then(resolve,reject)})}return originalFetch(input,options)};window.savePromise=integrationAdapter.save();return true})()`);
  await waitFor(`Boolean(window.releaseDS)`);
  await addText('LOCAL-INTEGRATION-LATE-20261103');
  await evaluate(`(async()=>{releaseDS();await savePromise;window.fetch=originalFetch;return integrationAdapter.refresh()})()`);
  await verify(`integrationAdapter.getState().phase==='unsaved'&&integrationAdapter.getState().saved.ackPending&&integrationAdapter.getState().engine.dirty`,'real save completed for old revision while new edits remain dirty');
  result.lateEditState=await evaluate(`integrationAdapter.getState()`);
  await evaluate(`integrationAdapter.save()`);
  await verify(`integrationAdapter.getState().phase==='saved'&&!integrationAdapter.getState().engine.dirty`,'next explicit save persists new revision through original coordinator');
  await addText('LOCAL-INTEGRATION-RETRY-20261103');
  await evaluate(`(()=>{window.fetch=(input,options)=>String(input)==='/api/save'?Promise.reject(Error('controlled test network outage')):originalFetch(input,options);window.expectedFailure=integrationAdapter.save().then(()=>false,()=>true);return true})()`);
  await verify(`expectedFailure`,'controlled network failure reaches caller');
  await verify(`integrationAdapter.getState().phase==='save_failed'&&integrationAdapter.getState().engine.dirty`,'controlled failure preserves dirty document and failure state');
  await evaluate(`(async()=>{window.fetch=originalFetch;await integrationAdapter.save();return integrationAdapter.getState()})()`);
  await verify(`integrationAdapter.getState().phase==='saved'`,'explicit retry succeeds using original host transaction');
  result.saved=await evaluate(`integrationAdapter.getState()`);
  await verify(`(async()=>{const h=wordcraftHost;const ds=await h.api('version',{diskVersion:h.diskVersion});const go=await h.goApi('versions/latest');return ds.sha256===go.docSha256&&ds.sha256===integrationAdapter.getState().saved.sha256})()`,'real DS and Go persisted bytes agree');
  await screenshot('saved-native.png');
  await runtime.stop();
  runtime = await startDeliveryRuntime({runtimeDir});
  result.reopenedRuntime = {origin:runtime.origin,goOrigin:runtime.goOrigin};
  await send('Page.navigate',{url:runtime.origin});
  await waitFor(`(()=>{try{return !!JSON.parse(document.querySelector('#host-log').textContent).opened}catch{return false}})()`);
  await install();
  await verify(`(async()=>{const text=JSON.stringify(await wordcraftHost.engine({op:'inspect'}));return ['LOCAL-INTEGRATION-FIRST-20261103','LOCAL-INTEGRATION-LATE-20261103','LOCAL-INTEGRATION-RETRY-20261103'].every(x=>text.includes(x))})()`,'full Go and DS restart reopens all edits from persisted DOCX');
  await verify(`integrationAdapter.getState().phase==='saved'&&integrationAdapter.getState().saved.sha256===${JSON.stringify(result.saved.saved.sha256)}`,'reopened adapter reports verified saved bytes');
  result.reopened=await evaluate(`integrationAdapter.getState()`);
  await screenshot('reopened-native.png');
  assert.equal(result.pageErrors.length,0);
  result.passed=true;
} catch(error) {result.error=error.stack;process.exitCode=1}
finally {
  if(socket)socket.close();
  if(browser?.pid)spawnSync('taskkill',['/PID',String(browser.pid),'/T','/F'],{windowsHide:true,stdio:'ignore'});
  try {if(runtime)await runtime.stop();result.ownedRuntimeStopped=true} catch(error){result.cleanupError=error.message;result.passed=false;process.exitCode=1}
  result.completedAt=new Date().toISOString();
  await writeFile(path.join(artifacts,'verification.json'),JSON.stringify(result,null,2));
  console.log(JSON.stringify(result,null,2));
}
