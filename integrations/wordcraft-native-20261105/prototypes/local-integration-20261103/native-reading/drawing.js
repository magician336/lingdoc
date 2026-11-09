const config = window.LINGDOC_DRAWING;
const element = id => document.getElementById(id);
const frame = element('editor');
const channel = new BroadcastChannel('lingdoc-drawing');
const pending = new Map();
const hosts = new Map();
let ready = false, busy = false, current = null, target = null, connectionTimer, pendingSave;
let activeFeature = null;
const features = {
  paper: '论文转图表', animated: '动画图表', aws: 'AWS 架构',
  flowchart: '复杂流程图', creative: '创意绘画',
};
const message = text => { element('status').textContent = text; element('notice').textContent = text; };
function controls() {
  element('version').textContent = current ? `已存 v${current.revision}` : '未保存图稿';
  for (const id of ['save', 'new', 'edit', 'capture', 'context']) element(id).disabled = busy || !ready;
  element('insert').disabled = busy || !ready || !target;
  element('replace').disabled = busy || !ready || !target?.image;
  element('hosts').disabled = busy;
  element('saved').disabled = busy || !ready;
  element('title').disabled = busy;
  frame.inert = busy;
  element('refresh').disabled = busy;
  element('exit').disabled = busy;
  for (const feature of Object.keys(features)) {
    const button = element('feature-' + feature);
    button.disabled = busy || !ready;
    button.ariaPressed = String(activeFeature === feature);
  }
}
async function api(action, body) {
  const response = await fetch('/native-reading/drawing/' + action, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Workspace-Token': config.token },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const result = await response.json();
  if (!response.ok) throw Error(result.error || '图稿操作失败。');
  return result;
}
function request(destination, data) {
  const requestId = crypto.randomUUID();
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { pending.delete(requestId); reject(Error('操作超时，请检查窗口状态。正文操作结果需返回文档核对。')); }, 45000);
    pending.set(requestId, { resolve, reject, timer, destination });
    if (destination === 'frame') frame.contentWindow.postMessage({ channel: 'lingdoc-drawing', requestId, ...data }, config.origin);
    else channel.postMessage({ type: 'request', requestId, token: config.token, hostId: destination, request: data });
  });
}
function settle(data, destination) {
  const item = pending.get(data.requestId);
  if (!item || item.destination !== destination) return;
  clearTimeout(item.timer); pending.delete(data.requestId);
  if (data.error || data.type === 'error') item.reject(Error(data.error || '绘图操作失败。'));
  else item.resolve(data.result ?? data);
}
window.addEventListener('message', event => {
  if (event.origin !== config.origin || event.source !== frame.contentWindow || event.data?.channel !== 'lingdoc-drawing') return;
    if (event.data.type === 'ready') {
      clearTimeout(connectionTimer); ready = true; controls();
      message('可以开始绘图。图稿保存后可插入正文。');
    }
  else settle(event.data, 'frame');
});
channel.onmessage = ({ data }) => {
  if (data?.token !== config.token) return;
  if (data.type === 'host') {
    hosts.set(data.hostId, data);
    if (![...element('hosts').options].some(option => option.value === data.hostId)) {
      element('hosts').add(new Option(`${data.standalone ? '普通文档' : '项目文稿'} · ${data.title} · ${data.hostId.slice(0, 6)}`, data.hostId));
    }
  } else if (data.type === 'result') settle(data, data.hostId);
};
async function run(action) {
  if (busy) return;
  busy = true; controls();
  try { await action(); } catch (error) { message(error.message); }
  finally { busy = false; controls(); }
}
async function list() {
  const records = await api('list');
  element('drawing-count').textContent = String(records.length);
  element('saved').replaceChildren(new Option('选择图稿', ''));
  for (const record of records) element('saved').add(new Option(`${record.title} · 第 ${record.revision} 版`, record.id));
  if (current && records.some(record => record.id === current.id)) element('saved').value = current.id;
}
async function load(record, persisted = true) {
  await request('frame', { type: 'load', xml: record?.xml || '<mxfile><diagram name="Page-1"><mxGraphModel><root><mxCell id="0"/><mxCell id="1" parent="0"/></root></mxGraphModel></diagram></mxfile>', messages: record?.messages || [], title: record?.title || '新图稿' });
  current = persisted ? record : null;
  pendingSave = null;
  element('title').value = record?.title || '新图稿';
  element('saved').value = current?.id || '';
}
async function save(exported) {
  exported ??= await request('frame', { type: 'export' });
  const payload = { id: current?.id, expectedRevision: current?.revision || 0, title: element('title').value.trim() || '新图稿', xml: exported.xml, messages: exported.messages || [], png: exported.png?.replace(/^data:image\/png;base64,/, '') };
  const fingerprint = JSON.stringify(payload);
  // A lost HTTP response must not create another version when the user retries.
  if (pendingSave?.fingerprint !== fingerprint) pendingSave = { fingerprint, key: crypto.randomUUID() };
  const saved = await api('save', { ...payload, key: pendingSave.key });
  current = saved;
  pendingSave = null;
  await list();
  message(`图稿已保存，第 ${saved.revision} 版。`);
  return saved;
}
async function capture() {
  const hostId = element('hosts').value;
  if (!hostId) throw Error('请先选择目标文档窗口。');
  const selection = await request(hostId, { op: 'diagram.selection' });
  return { ...selection, hostId };
}
async function saveBeforeLeaving() {
  if (!ready) {
    if (config.origin) throw Error('画布尚未连接，无法确认图稿已保存。请等待连接完成后再返回。');
    return current;
  }
  const exported = await request('frame', { type: 'export', skipEmpty: !current });
  return exported.empty ? current : await save(exported);
}
function focusOpener() {
  try { if (window.opener && !window.opener.closed) window.opener.focus(); } catch { /* Browser may deny tab focus. */ }
}
function closeDrawing(exit) {
  focusOpener();
  window.close();
  setTimeout(() => {
    if (window.closed) return;
    if (exit) window.location.assign('/');
    else message('图稿已保存。请手动切回目标文稿标签，在正文单击位置后，点击“确认并插入”，即可插图并保存。浏览器未允许自动关闭此页。');
  }, 500);
}
async function pick() {
  target = null; controls();
  const hostId = element('hosts').value;
  if (!hostId) throw Error('请先打开并选择目标文稿，再定位插入位置。');
  if (!hosts.get(hostId)?.directInsert) throw Error('文稿窗口使用旧版本：请先保存文稿，再刷新文稿窗口并点击“查找文档”，然后重新定位。');
  message('正在保存图稿，随后返回文稿选择插入位置…');
  const saved = await saveBeforeLeaving();
  if (!saved?.embeddedPng) throw Error('画布为空，请先绘制图形再选择插入位置。');
  const result = await request(hostId, { op: 'diagram.pick', pickId: crypto.randomUUID(), draftId: saved.id,
    drawing: {id:saved.id, revision:saved.revision, title:saved.title, embeddedPng:saved.embeddedPng} });
  if (!result.picking) throw Error('文稿未能开启定位，请保留画布并重试。');
  message('请在目标文稿中单击插入位置，再点击“确认并插入”，图片将直接插入并保存。');
  closeDrawing(false);
}
async function writeImage(replace) {
  const destination = target;
  if (!destination) throw Error('请先定位正文位置。');
  const saved = await save();
  // Clear before sending. A timeout can mean the write completed; a new target must be captured.
  target = null;
  await request(destination.hostId, {
    op: replace ? 'diagram.replace' : 'diagram.insert', operationId: crypto.randomUUID(),
    expectedDocumentId: destination.state.documentId, expectedRevision: destination.state.revision,
    bookmark: destination.selection, expectedImageSha256: destination.image?.sha256,
    data: saved.embeddedPng, alt: saved.title,
  });
  message('图片已写入正文。请在文档窗口保存文稿。');
}
function discover() {
  target = null;
  hosts.clear();
  element('hosts').replaceChildren(new Option('选择文档窗口', ''));
  channel.postMessage({ type: 'discover', token: config.token }); controls();
}
element('refresh').onclick = discover;
for (const [feature, label] of Object.entries(features)) {
  element('feature-' + feature).onclick = () => run(async () => {
    await request('frame', { type: 'feature', feature });
    activeFeature = feature;
    message(feature === 'paper'
      ? '已选择论文转图表。在对话中上传论文或粘贴内容，补充要求后发送。'
      : `已选择${label}。在对话中补充内容和要求后发送。`);
  });
}
element('hosts').onchange = () => { target = null; controls(); };
element('save').onclick = () => run(save);
element('capture').onclick = () => run(pick);
element('exit').onclick = () => run(async () => {
  message('正在保存图稿并返回文稿…');
  await saveBeforeLeaving();
  closeDrawing(true);
});
element('context').onclick = () => run(async () => {
  target = null;
  const selection = await capture();
  const text = selection.selection.text;
  if (!text?.trim()) throw Error('请先在正文选中要用于绘图的文字。');
  if (text.length > 12000) throw Error('选中文字较长，请缩小到 12000 字以内。');
  await request('frame', { type: 'prompt', prompt: '请根据以下内容绘制科研图表：\n' + text });
  target = null;
  message('选中文字已带入绘图输入框，可修改后发送。插入图片前请重新定位正文光标。');
});
element('insert').onclick = () => run(() => writeImage(false));
element('replace').onclick = () => run(() => writeImage(true));
element('new').onclick = () => run(async () => {
  if (!confirm('新建图稿会切换当前画布，请先保存需要保留的修改。继续吗？')) return;
  await load(null); target = null; message('已新建图稿。');
});
element('saved').onchange = () => {
  const id = element('saved').value;
  if (busy || !ready || !id || id === current?.id) {
    element('saved').value = current?.id || '';
    return;
  }
  return run(async () => {
    try {
      // Capture the destination before saving refreshes the library selection.
      const { record } = await api('get', { id });
      if (!record) throw Error('图稿不存在，当前画布已保留。');
      message('正在保存当前图稿，完成后自动切换…');
      const exported = await request('frame', { type: 'export', skipEmpty: !current });
      if (!exported.empty) await save(exported);
      await load(record); target = null;
      message(exported.empty ? '已打开保存的图稿。' : '原图已自动保存，已打开选中的图稿。');
    } finally {
      // Reflect the last tracked drawing so a failed switch can be retried.
      element('saved').value = current?.id || '';
    }
  });
};
element('edit').onclick = () => run(async () => {
  if (!confirm('编辑原图会切换当前画布，请先保存需要保留的修改。继续吗？')) return;
  target = null;
  const selection = await capture();
  if (!selection.image) throw Error('请在正文中选中一张灵档生成的图片。');
  const { record } = await api('read-image', { png: selection.image.data });
  if (!record) throw Error('这张图片没有携带可编辑图稿。');
  // The document carries its exact source version. Saving it creates a fresh drawing,
  // so editing a copied or old image cannot overwrite a newer library version.
  await load(record, false); target = selection; message('已恢复图片中的图稿与对话，修改后可更新正文图片。');
});
controls();
if (config.origin) {
  frame.src = config.origin + '/zh'; discover(); list().catch(error => message(error.message));
  connectionTimer = setTimeout(() => { if (!ready) message('绘图窗口连接超时，请刷新重试或联系管理员检查绘图服务。'); }, 45000);
}
else message(config.notice || '绘图服务尚未准备完成，请按部署说明启动绘图组件。');
window.addEventListener('pagehide', () => { clearTimeout(connectionTimer); channel.close(); for (const item of pending.values()) clearTimeout(item.timer); }, { once: true });
