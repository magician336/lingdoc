// Same-origin document bridge. Every mutation is checked again by the WASM host.
const channel = new BroadcastChannel('lingdoc-drawing');
const hostId = crypto.randomUUID();
window.LINGDOC_DRAWING_HOST_ID = hostId;
const token = window.NATIVE_WORKSPACE_TOKEN;
const allowed = new Set(['diagram.selection', 'diagram.insert', 'diagram.replace']);
const validId = value => typeof value === 'string' && /^[a-zA-Z0-9_-]{1,128}$/.test(value);
const ttl = 5 * 60 * 1000;
let picker = null;
let startingPicker = false;

function clearPicker() {
  if (!picker) return;
  clearTimeout(picker.timer);
  picker.banner.remove();
  picker = null;
}
function showNotice(banner, message) {
  clearPicker();
  const notice = document.createElement('div');
  notice.setAttribute('role', 'status');
  notice.style.cssText = banner.style.cssText;
  notice.textContent = message;
  document.body.append(notice);
  setTimeout(() => notice.remove(), 5000);
}
async function beginPicker(request) {
  const drawing = request.drawing;
  if (!validId(request.pickId) || !validId(request.draftId) || drawing?.id !== request.draftId ||
      !Number.isSafeInteger(drawing.revision) || drawing.revision < 1 ||
      typeof drawing.title !== 'string' || drawing.title.length > 120 ||
      typeof drawing.embeddedPng !== 'string' || !drawing.embeddedPng || drawing.embeddedPng.length > 8 * 1024 * 1024) {
    throw Error('图稿尚未准备好，请先保存图稿再定位。');
  }
  if (startingPicker || picker?.busy || (picker?.insertRequest && !picker.completed)) throw Error('已有插图正在处理，请先完成当前文稿的保存。');
  startingPicker = true;
  let state;
  try {
    installStandaloneSave(window.wordcraftHost);
    if (window.wordcraftHost.saveIntent?.supportsExpectedIdentity !== true) throw Error('文稿窗口需更新，请先保存文稿并刷新后重试。');
    state = await window.wordcraftHost.engine({op:'status'});
    await window.wordcraftHost.engine({op:'workspace.editor'});
  } finally { startingPicker = false; }
  clearPicker();
  const banner = document.createElement('div');
  banner.setAttribute('role', 'status');
  banner.style.cssText = 'position:fixed;z-index:2147483647;bottom:40px;left:50%;transform:translateX(-50%);display:flex;flex-wrap:wrap;align-items:center;gap:10px;box-sizing:border-box;max-width:calc(100% - 32px);padding:12px 16px;background:#fff;border:1px solid #7d94bd;border-radius:8px;box-shadow:0 3px 16px #0003;color:#18243a;font:14px sans-serif';
  const text = document.createElement('span'); text.textContent = '在正文单击插入位置，确认后直接插入并保存。';
  text.style.cssText = 'flex:1 1 220px;line-height:1.6';
  const confirm = document.createElement('button'); confirm.type = 'button'; confirm.textContent = '确认并插入';
  const cancel = document.createElement('button'); cancel.type = 'button'; cancel.textContent = '取消';
  const buttonStyle = 'flex:0 0 auto;width:auto;min-height:36px;margin:0;padding:8px 12px;border:1px solid #dce6df;border-radius:7px;font:13px sans-serif;line-height:1.4;cursor:pointer;';
  confirm.style.cssText = buttonStyle + 'background:#2e7660;color:white;border-color:#2e7660';
  cancel.style.cssText = buttonStyle + 'background:white;color:#456353';
  banner.append(text, confirm, cancel);
  // Keep the exact saved image: another drawing window may edit its library entry.
  const current = { id:request.pickId, documentId:state.documentId,
    drawing:{...drawing}, banner, timer:null, expires:Date.now() + ttl, busy:false, inserted:false, insertRequest:null };
  picker = current;
  current.timer = setTimeout(() => {
    if (picker === current && !current.busy && !current.insertRequest) clearPicker();
  }, ttl);
  cancel.addEventListener('click', () => {
    if (picker !== current || current.busy) return;
    if (current.inserted && !current.completed) {
      showNotice(banner, '图片已保留在正文中，未保存的修改请在文稿中保存。');
    } else if (current.insertRequest && !current.completed) {
      showNotice(banner, '插图结果尚未确认，请先检查正文中的图片并保存，再开始新的插图。');
    } else clearPicker();
  });
  confirm.addEventListener('click', async () => {
    if (picker !== current || current.busy || current.completed) return;
    current.busy = true; confirm.disabled = true; cancel.disabled = true;
    try {
      const host = window.wordcraftHost;
      let expected = await host.engine({op:'status'});
      if (expected.documentId !== current.documentId) throw Error('目标文稿已变化，请回到原文稿保存或重新开始插图。');
      if (!current.inserted) {
        if (!current.insertRequest) {
          if (Date.now() >= current.expires) throw Error('定位已过期，请取消后重新定位。');
          const source = await host.engine({op:'source.status'});
          const workspace = await host.engine({op:'workspace.status'});
          const selected = await host.engine({op:'diagram.selection'});
          if (source?.sourceActive !== false || workspace?.auxiliaryActive !== false) throw Error('请先切回正文，再选择位置。');
          if (selected?.state?.documentId !== current.documentId) throw Error('目标文稿已变化，请重新定位。');
          const selection = selected?.selection;
          if (!selection?.anchor || !selection?.focus || JSON.stringify(selection.anchor) !== JSON.stringify(selection.focus)) {
            throw Error('请在正文单击插入光标。');
          }
          // Reuse this operation if its acknowledgement is lost; never insert twice.
          current.insertRequest = {
            op:'diagram.insert', operationId:crypto.randomUUID(),
            expectedDocumentId:current.documentId, expectedRevision:selected.state.revision,
            bookmark:selection, data:current.drawing.embeddedPng, alt:current.drawing.title,
          };
        }
        text.textContent = '正在插入图片…';
        const receipt = await host.engine(current.insertRequest);
        if (receipt?.status !== 'applied') throw Error('图片写入未确认，请检查正文后重试。');
        current.inserted = true;
        clearTimeout(current.timer);
        expected = receipt.state;
      }
      text.textContent = '图片已插入，正在保存文稿…';
      if (expected?.documentId !== current.documentId) throw Error('目标文稿已变化，请回到原文稿保存。');
      const saved = await host.saveIntent('drawing', expected);
      const live = await host.engine({op:'status'});
      const completed = saved?.status === 'saved' || saved?.status === 'downloaded' || saved?.ackPending === false;
      if (!completed || saved.live?.documentId !== current.documentId || saved.live?.revision !== expected.revision ||
          saved.live?.dirty !== false || live.documentId !== current.documentId || live.revision !== expected.revision || live.dirty !== false) {
        throw Error('保存尚未完成或正文有新修改，请重试保存。');
      }
      text.textContent = saved.status === 'downloaded'
        ? '图片已插入并导出文稿，请保存浏览器下载的文件。'
        : '图片已插入，文稿修改已保存。';
      current.completed = true;
      if (saved.status === 'downloaded') {
        // Downloads confirm an export, not an overwrite of the original file.
        confirm.style.display = 'none'; confirm.disabled = true;
        cancel.textContent = '完成';
      } else {
        showNotice(banner, text.textContent);
      }
    } catch (error) {
      text.textContent = (current.inserted ? '图片已保留在正文，但保存未完成：' : '') +
        (error instanceof Error ? error.message : '操作失败，请重试。');
      confirm.textContent = current.inserted ? '重试保存' : current.insertRequest ? '重试确认插图' : '确认并插入';
      if (current.inserted) cancel.textContent = '稍后保存';
    } finally {
      current.busy = false;
      confirm.disabled = confirm.style.display === 'none'; cancel.disabled = false;
    }
  });
  document.body.append(banner);
  return {picking:true};
}

channel.onmessage = async ({ data }) => {
  if (!data || data.token !== token) return;
  if (data.type === 'discover') {
    installStandaloneSave(window.wordcraftHost);
    if (window.wordcraftHost?.engine) channel.postMessage({ type:'host', token, hostId, title:document.title, standalone:window.WORDCRAFT_STANDALONE === true, picker:true,
      directInsert:window.wordcraftHost.saveIntent?.supportsExpectedIdentity === true });
    return;
  }
  if (data.type !== 'request' || data.hostId !== hostId) return;
  const request = data.request;
  if (!allowed.has(request?.op) && !['diagram.pick', 'diagram.cancel-pick', 'diagram.resume'].includes(request?.op)) return;
  try {
    let result;
    if (request.op === 'diagram.pick') result = await beginPicker(request);
    else if (request.op === 'diagram.cancel-pick') {
      const canceled = !!picker && picker.id === request.pickId && !picker.busy && !picker.insertRequest;
      if (canceled) clearPicker();
      result = {canceled};
    } else if (request.op === 'diagram.resume') {
      throw Error('旧版本定位凭据已失效，请重新定位并直接插入。');
    } else result = await window.wordcraftHost.engine(request);
    channel.postMessage({type:'result', token, hostId, requestId:data.requestId, result});
  } catch (error) {
    const message = ['diagram.pick', 'diagram.resume'].includes(request?.op) && error instanceof Error
      ? error.message : '文档或选区已变化，或图片写入失败。请返回正文重新选择位置后再试。';
    channel.postMessage({type:'result', token, hostId, requestId:data.requestId, error:message});
  }
};
window.addEventListener('pagehide', () => { clearPicker(); channel.close(); }, { once:true });

function installStandaloneSave(host) {
  if (!host?.standalone || typeof host.saveIntent === 'function') return;
  const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
  host.saveIntent = async (_source, expected) => {
    const before = await host.engine({op:'status'});
    if (expected && (before.documentId !== expected.documentId || before.revision !== expected.revision)) {
      throw Object.assign(Error('正文已变化，请检查后重新保存。'), {code:'stale'});
    }
    const started = await host.engine({op:'save.begin',expectedDocumentId:before.documentId,expectedRevision:before.revision});
    const deadline = Date.now() + 120000;
    while (Date.now() < deadline) {
      const result = await host.engine({op:'save.status',ticket:started.ticket});
      if (result.status === 'saved' || result.status === 'downloaded') return {status:result.status,live:result.state};
      if (result.status !== 'pending') {
        const text = result.status === 'cancelled' ? '已取消保存，文稿仍需保存。'
          : result.status === 'stale' ? '保存期间文稿发生变化，请重新保存。'
          : '文稿保存失败，请重试。';
        throw Object.assign(Error(text), {code:result.status});
      }
      await sleep(150);
    }
    throw Object.assign(Error('保存结果尚未确认，请在文稿中检查后重试。'), {code:'timeout'});
  };
  host.saveIntent.supportsExpectedIdentity = true;
}
