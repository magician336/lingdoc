import { readFile } from 'node:fs/promises';
import { createDrawingStore } from './drawing-store.mjs';
import { readDrawing } from './drawing-png.mjs';

const base = '/native-reading/drawing';
const assets = new Map([
  [base, ['drawing.html', 'text/html; charset=utf-8']],
  [base + '.js', ['drawing.js', 'text/javascript; charset=utf-8']],
  [base + '-host.js', ['drawing-host.js', 'text/javascript; charset=utf-8']],
  [base + '.css', ['drawing.css', 'text/css; charset=utf-8']],
]);

export async function createDrawingHTTP({ directory, token, drawingOrigin, notice = '' }) {
  const store = await createDrawingStore({ directory });
  return async function handle(req, res, url) {
    if (!url.pathname.startsWith(base)) return false;
    const origin = url.origin;
    const headers = { 'Cache-Control': 'no-store', 'X-Content-Type-Options': 'nosniff',
      'Cross-Origin-Resource-Policy': 'same-origin',
      'Content-Security-Policy': `default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; frame-src ${drawingOrigin || "'none'"}; frame-ancestors 'none'` };
    const json = (status, value) => { res.writeHead(status, { ...headers, 'Content-Type': 'application/json; charset=utf-8' }); res.end(JSON.stringify(value)); };
    try {
      const asset = assets.get(url.pathname);
      if (req.method === 'GET' && asset) {
        res.writeHead(200, { ...headers, 'Content-Type': asset[1] });
        res.end(await readFile(new URL(asset[0], import.meta.url)));
        return true;
      }
      if (req.method === 'GET' && url.pathname === base + '-config.js') {
        res.writeHead(200, { ...headers, 'Content-Type': 'text/javascript; charset=utf-8' });
        res.end('window.LINGDOC_DRAWING=' + JSON.stringify({ token, origin: drawingOrigin, notice }) + ';');
        return true;
      }
      if (req.headers['x-workspace-token'] !== token) { json(403, { error: '绘图会话未授权，请重新打开窗口。' }); return true; }
      if (req.method === 'GET' && url.pathname === base + '/list') { json(200, await store.list()); return true; }
      if (req.method !== 'POST' || req.headers.origin !== origin) { json(403, { error: '请求来源不匹配。' }); return true; }
      const chunks = []; let size = 0;
      for await (const chunk of req) {
        size += chunk.length;
        if (size > 10 * 1024 * 1024) { json(413, { error: '图稿过大，请减少图片或对话内容。' }); return true; }
        chunks.push(chunk);
      }
      const input = JSON.parse(Buffer.concat(chunks).toString('utf8'));
      if (url.pathname === base + '/save') {
        const record = await store.save(input);
        json(200, record);
      } else if (url.pathname === base + '/delete') {
        json(200, await store.delete(input));
      } else if (url.pathname === base + '/history') {
        json(200, await store.history(input.id));
      } else if (url.pathname === base + '/version') {
        json(200, await store.version(input.id,input.revision));
      } else if (url.pathname === base + '/delete-version') {
        json(200, await store.deleteVersion(input));
      } else if (url.pathname === base + '/read-image') {
        json(200, { record: readDrawing(input.png) });
      } else if (url.pathname === base + '/get') {
        json(200, { record: await store.get(input.id) });
      } else json(404, { error: '绘图操作不存在。' });
    } catch (error) {
      const status = error.code === 'CONFLICT' ? 409 : error.code === 'NOT_FOUND' ? 404 : error.code === 'VALIDATION' || error instanceof SyntaxError ? 400 : 500;
      json(status, { error: status === 409 ? '图稿版本已变化，请重新打开图稿后保存。' : status === 400 ? '图稿数据无效或超过限制。' : status === 404 ? '图稿不存在，可能已被删除。' : '绘图操作未完成，请保留窗口后重试。' });
    }
    return true;
  };
}
