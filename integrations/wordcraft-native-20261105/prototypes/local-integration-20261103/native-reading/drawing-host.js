// Same-origin document bridge. Every mutation is checked again by the WASM host.
const channel = new BroadcastChannel('lingdoc-drawing');
const hostId = crypto.randomUUID();
const token = window.NATIVE_WORKSPACE_TOKEN;
const allowed = new Set(['diagram.selection', 'diagram.insert', 'diagram.replace']);
channel.onmessage = async ({ data }) => {
  if (!data || data.token !== token) return;
  if (data.type === 'discover') {
    if (window.wordcraftHost?.engine) channel.postMessage({ type: 'host', token, hostId, title: document.title, standalone: window.WORDCRAFT_STANDALONE === true });
    return;
  }
  if (data.type !== 'request' || data.hostId !== hostId || !allowed.has(data.request?.op)) return;
  try {
    const result = await window.wordcraftHost.engine(data.request);
    channel.postMessage({ type: 'result', token, hostId, requestId: data.requestId, result });
  } catch {
    channel.postMessage({ type: 'result', token, hostId, requestId: data.requestId, error: '文档或选区已变化，或图片写入失败。请返回正文重新选择位置后再试。' });
  }
};
window.addEventListener('pagehide', () => channel.close(), { once: true });
