import { spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { createServer } from 'node:net';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const runId = `${new Date().toISOString().replaceAll(':', '-').replaceAll('.', '-')}-${process.pid}`;
const artifacts = path.join(root, 'artifacts', 's2', runId);
const runtime = path.join(root, 'runtime', 's2', runId);
const profile = path.join(runtime, 'profile');
const dataDirectory = path.join(runtime, 'data');
const edge = 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe';
const fixtureSha256 = 'e6ef31b4b284f509b478071f8be09c76efceab9f32ca8803afdf905eee136181';
const expectedSections = ['topic-summary', 'topic-basis', 'research-content', 'innovation', 'expected-results', 'references'];
const expectedTitles = ['[选题说明]', '[选题依据]', '[研究内容]', '[创新之处]', '[预期成果]', '[参考文献]'];
const sizes = [[1440, 900], [1366, 768], [1280, 800], [1920, 1080]];
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const assert = (condition, message) => { if (!condition) throw new Error(message); };
const sha256 = bytes => createHash('sha256').update(bytes).digest('hex');
async function availablePort() {
  const server = createServer();
  await new Promise((resolve, reject) => server.once('error', reject).listen(0, '127.0.0.1', resolve));
  const port = server.address().port;
  await new Promise(resolve => server.close(resolve));
  return port;
}
async function stopChild(child) {
  if (!child) return true;
  if (child.exitCode === null && child.pid) spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore' });
  for (let i = 0; i < 30 && child.exitCode === null; i++) await sleep(100);
  return child.exitCode !== null;
}

await mkdir(artifacts, { recursive: true });
await mkdir(profile, { recursive: true });
await mkdir(dataDirectory, { recursive: true });
const results = {
  kind: 'S2 native six-section projection with real DOCX focus and undo verification',
  runId,
  probeSha256: sha256(await readFile(fileURLToPath(import.meta.url))),
  actual: {},
  runtime: { dataDirectory, profile },
  viewports: [],
  sectionFocus: [],
  pageErrors: [],
  passed: false,
};
let host;
let browser;
let socket;
let hostOutput = '';
let browserOutput = '';
try {
  const hostPort = await availablePort();
  const debugPort = await availablePort();
  const origin = `http://127.0.0.1:${hostPort}`;
  host = spawn(process.execPath, [path.join(root, 'ds', 'r11-bridge-combination-20261025', 'host', 'server.mjs')], {
    env: {
      ...process.env,
      WORDCRAFT_HOST_PORT: String(hostPort),
      WORDCRAFT_WEB_DIR: path.join(root, 'build', 'out'),
      WORDCRAFT_DATA_DIR: dataDirectory,
      WORDCRAFT_AUDIT_PATH: path.join(artifacts, 'host-audit.jsonl'),
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  host.stdout.on('data', chunk => { hostOutput += String(chunk); });
  host.stderr.on('data', chunk => { hostOutput += String(chunk); });
  let ready;
  for (let i = 0; i < 100 && !ready; i++) {
    if (host.exitCode !== null) throw new Error(`宿主提前退出：${hostOutput}`);
    for (const line of hostOutput.split(/\r?\n/)) {
      try { const value = JSON.parse(line); if (value.ready && value.pid === host.pid) ready = value; } catch {}
    }
    if (!ready) await sleep(100);
  }
  assert(ready, `宿主启动超时：${hostOutput}`);
  results.runtime.hostPort = hostPort;
  results.runtime.edgeCdpPort = debugPort;
  results.hostReady = ready;

  for (const name of ['wordcraft.js', 'wordcraft_bg.wasm']) {
    const bytes = await readFile(path.join(root, 'build', 'out', name));
    results.actual[name] = { bytes: bytes.length, sha256: sha256(bytes) };
  }

  browser = spawn(edge, [
    '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    '--disable-background-timer-throttling', '--disable-backgrounding-occluded-windows',
    '--disable-renderer-backgrounding', `--remote-debugging-port=${debugPort}`,
    `--user-data-dir=${profile}`, 'about:blank',
  ], { stdio: ['ignore', 'ignore', 'pipe'] });
  browser.stderr.on('data', chunk => { browserOutput += String(chunk); });
  let pages;
  for (let i = 0; i < 100; i++) {
    if (browser.exitCode !== null) throw new Error(`Edge 提前退出：${browserOutput}`);
    try {
      pages = await (await fetch(`http://127.0.0.1:${debugPort}/json/list`, { signal: AbortSignal.timeout(1000) })).json();
      if (pages.some(page => page.type === 'page')) break;
    } catch {}
    await sleep(150);
  }
  const page = pages?.find(item => item.type === 'page');
  assert(page, '未找到 S2 浏览器页面');
  socket = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  let sequence = 0;
  const pending = new Map();
  socket.onmessage = event => {
    const message = JSON.parse(event.data);
    if (message.method === 'Runtime.exceptionThrown') results.pageErrors.push(message.params.exceptionDetails);
    if (!message.id || !pending.has(message.id)) return;
    const handlers = pending.get(message.id);
    clearTimeout(handlers.timer);
    pending.delete(message.id);
    if (message.error) handlers.reject(new Error(message.error.message)); else handlers.resolve(message.result);
  };
  const send = (method, params = {}) => new Promise((resolve, reject) => {
    const id = ++sequence;
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`${method} timeout`)); }, 30000);
    pending.set(id, { resolve, reject, timer });
    socket.send(JSON.stringify({ id, method, params }));
  });
  const evaluate = async expression => {
    const response = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
    if (response.exceptionDetails) throw new Error(response.exceptionDetails.exception?.description || response.exceptionDetails.text);
    return response.result.value;
  };
  const engine = request => evaluate(`window.wordcraftHost.engine(${JSON.stringify(request)})`);
  const click = async (x, y) => {
    await send('Input.dispatchMouseEvent', { type: 'mouseMoved', x, y });
    await send('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', clickCount: 1 });
    await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', clickCount: 1 });
    await sleep(450);
  };
  const undo = async () => {
    await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'z', code: 'KeyZ', windowsVirtualKeyCode: 90, modifiers: 2 });
    await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'z', code: 'KeyZ', windowsVirtualKeyCode: 90, modifiers: 2 });
    await sleep(450);
  };
  const screenshot = async name => {
    const shot = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
    const bytes = Buffer.from(shot.data, 'base64');
    await writeFile(path.join(artifacts, name), bytes);
    return { file: name, sha256: sha256(bytes), bytes: bytes.length };
  };
  const waitRevision = async (previous, label) => {
    let status;
    for (let i = 0; i < 40; i++) {
      status = await engine({ op: 'status' });
      if (status.revision > previous) return status;
      await sleep(100);
    }
    throw new Error(`${label} 未增加文档修订号：${previous} -> ${status?.revision}`);
  };

  await send('Page.enable');
  await send('Runtime.enable');
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
  await send('Page.navigate', { url: origin });
  let opened;
  for (let i = 0; i < 300; i++) {
    const value = await evaluate(`(()=>{try{const text=document.querySelector('#host-log')?.textContent||'';const parsed=JSON.parse(text);return parsed.opened?parsed:null}catch{return null}})()`);
    if (value) { opened = value; break; }
    await sleep(200);
  }
  assert(opened, `真实 DOCX 打开超时：${await evaluate(`document.querySelector('#host-log')?.textContent`)}`);
  assert(opened.sourceSha256 === fixtureSha256, '打开的 DOCX 不是固定输入字节');
  const initialStatus = await engine({ op: 'status' });
  const initialManifest = await engine({ op: 'manifest' });
  assert(initialManifest.structure?.ok === true, '固定 DOCX 的六章结构无效');
  assert(initialManifest.sections?.length === 6, '六章数量不是 6');
  assert(initialManifest.sections.map(section => section.sectionId).join(',') === expectedSections.join(','), '六章 ID 或顺序不匹配');
  assert(initialManifest.sections.map(section => section.anchorToken).join(',') === expectedTitles.join(','), '六章标题不是 DOCX 模板锚点');
  for (const section of initialManifest.sections) {
    assert(section.bodyCharCount === Array.from(section.bodyText).length, `${section.sectionId} 字符数与实时正文不符`);
    assert(section.bodyRange?.anchor && section.anchorPath, `${section.sectionId} 缺少真实定位范围`);
  }
  results.document = { sourceSha256: opened.sourceSha256, diskVersion: opened.diskVersion, initialStatus, manifestHash: initialManifest.manifestHash };
  results.initialSections = initialManifest.sections.map(section => ({
    sectionId: section.sectionId,
    order: section.order,
    anchorToken: section.anchorToken,
    bodyCharCount: section.bodyCharCount,
    bodyTextSha256: section.bodyTextSha256,
    bodyRange: section.bodyRange,
  }));

  for (let index = 0; index < sizes.length; index++) {
    const [width, height] = sizes[index];
    await send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: false });
    await sleep(500);
    if (index === 0) {
      // S2's navigation toggle is right-aligned; at 1440×900 its center is
      // (1235, 18), as shown by the retained S2 failure screenshot.
      const navigationToggle = { x: width - 205, y: 18 };
      results.navigationToggle = { viewport: { width, height }, ...navigationToggle };
      await click(navigationToggle.x, navigationToggle.y);
    }
    const canvas = await evaluate(`(()=>{const c=document.querySelector('#wordcraft_canvas');const r=c.getBoundingClientRect();return {width:r.width,height:r.height,loadingPresent:!!document.querySelector('#wordcraft_loading')}})()`);
    assert(canvas.width > 500 && canvas.height === height && !canvas.loadingPresent, `${width}x${height} Canvas 未完成真实加载`);
    const shot = await screenshot(`s2-six-sections-${width}x${height}.png`);
    results.viewports.push({ width, height, deviceScaleFactor: 1, canvas, screenshot: shot });
  }

  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
  await sleep(400);
  for (let index = 0; index < initialManifest.sections.length; index++) {
    const section = initialManifest.sections[index];
    const before = await engine({ op: 'status' });
    // Native card centers measured from the 1440×900 screenshot: 265, 325, …, 565.
    await click(130, 265 + index * 60);
    const after = await engine({ op: 'status' });
    const selected = await engine({ op: 'selection' });
    const currentManifest = await engine({ op: 'manifest' });
    assert(after.documentId === before.documentId && after.revision === before.revision && after.dirty === before.dirty, `${section.sectionId} 定位改变了文档状态`);
    assert(JSON.stringify(selected.selection.anchor) === JSON.stringify(section.bodyRange.anchor), `${section.sectionId} 未定位到实时 bodyRange.anchor`);
    assert(JSON.stringify(selected.selection.focus) === JSON.stringify(section.bodyRange.anchor), `${section.sectionId} 定位不是折叠选区`);
    assert(currentManifest.manifestHash === initialManifest.manifestHash, `${section.sectionId} 定位改变了结构哈希`);
    const shot = await screenshot(`s2-focus-${section.sectionId}-1440x900.png`);
    results.sectionFocus.push({ sectionId: section.sectionId, before, after, selection: selected.selection, screenshot: shot });
  }

  // A real insertion followed by chapter focus and undo proves that focus adds no undo step.
  await click(130, 265);
  const insertionSelection = await engine({ op: 'selection' });
  const beforeInsert = await engine({ op: 'status' });
  const probe = '〔S2定位撤销探针〕';
  await engine({
    op: 'insert',
    bookmark: insertionSelection.selection,
    text: probe,
    expectedDocumentId: beforeInsert.documentId,
    expectedRevision: beforeInsert.revision,
  });
  const insertedStatus = await waitRevision(beforeInsert.revision, '真实插入');
  const insertedManifest = await engine({ op: 'manifest' });
  assert(insertedManifest.sections[0].bodyText.includes(probe), '实时六章投影未刷新真实插入');
  await click(130, 325);
  const focusedAfterInsert = await engine({ op: 'status' });
  assert(focusedAfterInsert.revision === insertedStatus.revision, '编辑后的切章额外改变了修订号');
  await undo();
  const undoStatus = await waitRevision(insertedStatus.revision, '撤销');
  const restoredManifest = await engine({ op: 'manifest' });
  assert(!restoredManifest.sections[0].bodyText.includes(probe), '切章后的撤销没有撤销真实插入');
  const initialHashes = initialManifest.sections.map(section => section.bodyTextSha256);
  const restoredHashes = restoredManifest.sections.map(section => section.bodyTextSha256);
  assert(JSON.stringify(restoredHashes) === JSON.stringify(initialHashes), '撤销后六章正文哈希未全部恢复');
  results.undoCheck = {
    probe,
    beforeInsert,
    insertedStatus,
    focusedAfterInsert,
    undoStatus,
    insertedBodyTextSha256: insertedManifest.sections[0].bodyTextSha256,
    restoredBodyTextSha256: restoredHashes,
    conclusion: '编辑后的章节定位未新增文档修订；随后一次撤销移除了真实插入。',
  };

  const finalStatus = await engine({ op: 'status' });
  assert(results.pageErrors.length === 0, '浏览器存在未捕获脚本错误');
  results.finalStatus = finalStatus;
  results.finalNote = '六章均通过真实鼠标点击定位，文档身份、revision 与 dirty 在定位前后不变。真实插入已撤销，六章正文哈希恢复；未执行保存。';
  results.passed = true;
} catch (error) {
  results.error = String(error);
  results.passed = false;
  process.exitCode = 1;
} finally {
  if (socket) {
    try { await new Promise((resolve, reject) => {
      const timer = setTimeout(resolve, 2000);
      socket.addEventListener('message', event => {
        const message = JSON.parse(event.data);
        if (message.method === 'Browser.close' || message.id === -1) { clearTimeout(timer); resolve(); }
      }, { once: true });
      socket.send(JSON.stringify({ id: -1, method: 'Browser.close', params: {} }), error => error ? reject(error) : resolve());
    }); } catch {}
    socket.close();
  }
  results.browserExited = await stopChild(browser);
  results.hostExited = await stopChild(host);
  results.hostLog = hostOutput.slice(-6000);
  results.browserLog = browserOutput.slice(-3000);
  if (!results.browserExited || !results.hostExited) {
    results.passed = false;
    process.exitCode = 1;
    results.processError = '本次浏览器或宿主进程未确认退出';
  }
  await writeFile(path.join(artifacts, 'verification.json'), JSON.stringify(results, null, 2));
  console.log(JSON.stringify({ artifacts, passed: results.passed, actual: results.actual, document: results.document, viewports: results.viewports.length, focused: results.sectionFocus.length, undoCheck: results.undoCheck, browserExited: results.browserExited, hostExited: results.hostExited, error: results.error, processError: results.processError }, null, 2));
}
