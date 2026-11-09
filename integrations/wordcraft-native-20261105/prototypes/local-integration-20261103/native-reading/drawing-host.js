// Same-origin document bridge. Every mutation is checked again by the WASM host.
const channel = new BroadcastChannel('lingdoc-drawing');
const hostId = crypto.randomUUID();
window.LINGDOC_DRAWING_HOST_ID = hostId;
const token = window.NATIVE_WORKSPACE_TOKEN;
const allowed = new Set(['diagram.selection', 'diagram.insert', 'diagram.replace']);
const validId = value => typeof value === 'string' && /^[a-zA-Z0-9_-]{1,128}$/.test(value);
const ttl = 5 * 60 * 1000;
const tickets = new Map();
let picker = null;

function clearPicker() {
  if (!picker) return;
  clearTimeout(picker.timer);
  picker.banner.remove();
  picker = null;
}
function pruneTickets() {
  const now = Date.now();
  for (const [key, ticket] of tickets) if (ticket.expires <= now) tickets.delete(key);
}
async function beginPicker(request) {
  if (!validId(request.pickId) || !(request.draftId == null || validId(request.draftId))) throw Error('无效的选点请求。');
  await window.wordcraftHost.engine({op:'workspace.editor'});
  clearPicker();
  pruneTickets();
  const banner = document.createElement('div');
  banner.setAttribute('role', 'status');
  banner.style.cssText = 'position:fixed;z-index:2147483647;bottom:40px;left:50%;transform:translateX(-50%);display:flex;flex-wrap:wrap;align-items:center;gap:10px;box-sizing:border-box;max-width:calc(100% - 32px);padding:12px 16px;background:#fff;border:1px solid #7d94bd;border-radius:8px;box-shadow:0 3px 16px #0003;color:#18243a;font:14px sans-serif';
  const text = document.createElement('span'); text.textContent = '在正文单击插入位置，确认后返回绘图。';
  text.style.cssText = 'flex:1 1 220px;line-height:1.6';
  const confirm = document.createElement('button'); confirm.type = 'button'; confirm.textContent = '确认位置并返回绘图';
  const cancel = document.createElement('button'); cancel.type = 'button'; cancel.textContent = '取消';
  const buttonStyle = 'flex:0 0 auto;width:auto;min-height:36px;margin:0;padding:8px 12px;border:1px solid #dce6df;border-radius:7px;font:13px sans-serif;line-height:1.4;cursor:pointer;';
  confirm.style.cssText = buttonStyle + 'background:#2e7660;color:white;border-color:#2e7660';
  cancel.style.cssText = buttonStyle + 'background:white;color:#456353';
  banner.append(text, confirm, cancel);
  const current = { id:request.pickId, draftId:request.draftId ?? null, banner, timer:null, busy:false };
  picker = current;
  current.timer = setTimeout(() => { if (picker === current) clearPicker(); }, ttl);
  cancel.addEventListener('click', () => { if (picker === current) clearPicker(); });
  confirm.addEventListener('click', async () => {
    if (picker !== current || current.busy) return;
    // The popup is opened synchronously while the click still has browser activation.
    const popup = window.open('about:blank', '_blank');
    if (!popup) { text.textContent = '浏览器阻止了绘图窗口，请允许弹窗后重试。'; return; }
    current.busy = true; confirm.disabled = true;
    let key;
    try {
      const source = await window.wordcraftHost.engine({op:'source.status'});
      const workspace = await window.wordcraftHost.engine({op:'workspace.status'});
      const selected = await window.wordcraftHost.engine({op:'diagram.selection'});
      if (picker !== current) { popup.close(); return; }
      // These fields come from the native status methods, including the active
      // pane in split view. Missing fields are not evidence that body is active.
      if (source?.sourceActive !== false || workspace?.auxiliaryActive !== false) throw Error('请先切回正文，再选择位置。');
      const selection = selected?.selection;
      if (!selection?.anchor || !selection?.focus || (!selected.image && JSON.stringify(selection.anchor) !== JSON.stringify(selection.focus))) {
        throw Error('请在正文单击插入光标，或选中一张图片。');
      }
      key = crypto.randomUUID();
      pruneTickets();
      tickets.set(key, { value:selected, expires:Date.now() + ttl });
      const params = new URLSearchParams({resume:key, host:hostId});
      if (current.draftId) params.set('draft', current.draftId);
      popup.location.href = '/native-reading/drawing?' + params.toString();
      clearPicker();
    } catch (error) {
      if (key) tickets.delete(key);
      popup.close();
      if (picker === current) text.textContent = error instanceof Error ? error.message : '无法获取正文位置，请重试。';
    } finally {
      current.busy = false;
      confirm.disabled = false;
    }
  });
  document.body.append(banner);
  return {picking:true};
}

channel.onmessage = async ({ data }) => {
  if (!data || data.token !== token) return;
  if (data.type === 'discover') {
    if (window.wordcraftHost?.engine) channel.postMessage({ type:'host', token, hostId, title:document.title, standalone:window.WORDCRAFT_STANDALONE === true, picker:true });
    return;
  }
  if (data.type !== 'request' || data.hostId !== hostId) return;
  const request = data.request;
  if (!allowed.has(request?.op) && !['diagram.pick', 'diagram.cancel-pick', 'diagram.resume'].includes(request?.op)) return;
  try {
    let result;
    if (request.op === 'diagram.pick') result = await beginPicker(request);
    else if (request.op === 'diagram.cancel-pick') {
      const canceled = !!picker && picker.id === request.pickId;
      if (canceled) clearPicker();
      result = {canceled};
    } else if (request.op === 'diagram.resume') {
      pruneTickets();
      const ticket = tickets.get(request.ticket);
      if (!ticket) throw Error('选点凭据已失效，请重新定位。');
      tickets.delete(request.ticket);
      result = ticket.value;
    } else result = await window.wordcraftHost.engine(request);
    channel.postMessage({type:'result', token, hostId, requestId:data.requestId, result});
  } catch (error) {
    const message = ['diagram.pick', 'diagram.resume'].includes(request?.op) && error instanceof Error
      ? error.message : '文档或选区已变化，或图片写入失败。请返回正文重新选择位置后再试。';
    channel.postMessage({type:'result', token, hostId, requestId:data.requestId, error:message});
  }
};
window.addEventListener('pagehide', () => { clearPicker(); tickets.clear(); channel.close(); }, { once:true });
