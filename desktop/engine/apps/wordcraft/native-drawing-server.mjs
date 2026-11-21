// LingDoc native drawing workspace: old drawing UI + store + pinned next.ai proxy.
// This is not the legacy document host. Document commands go only to the running egui app.
import http from 'node:http';
import net from 'node:net';
import { readFile } from 'node:fs/promises';
import { createDrawingHTTP } from '../../../../../prototypes/local-integration-20261103/native-reading/drawing-http.mjs';
import { createDrawingProxy } from '../../../../../prototypes/local-integration-20261103/native-reading/drawing-proxy.mjs';

const required = name => {
  const value = process.env[name];
  if (!value) throw Error(`${name} is required`);
  return value;
};
const port = Number(required('LINGDOC_NATIVE_DRAWING_PORT'));
const token = required('LINGDOC_NATIVE_DRAWING_TOKEN');
const controlPort = Number(required('LINGDOC_CONTROL_PORT'));
const controlKey = required('LINGDOC_CONTROL_KEY');
const directory = required('LINGDOC_DRAWING_DIRECTORY');
const drawingOrigin = required('LINGDOC_NEXT_AI_ORIGIN');
const applicationDirectory = required('LINGDOC_NEXT_AI_APPLICATION');
const hostId = 'native';
const origin = `http://127.0.0.1:${port}`;
const drawingHTTP = await createDrawingHTTP({ directory, token, drawingOrigin, sameOrigin: true });
const drawingProxy = await createDrawingProxy({ origin: drawingOrigin, applicationDirectory });
const channelAsset = await readFile(new URL('native-drawing-channel.js', import.meta.url));
const operations = new Set(['diagram.selection', 'diagram.insert', 'diagram.replace', 'diagram.pick',
  'diagram.cancel-pick', 'diagram.edit', 'diagram.edit-apply', 'diagram.return', 'diagram.default-target']);

function callNative(request) {
  return new Promise((resolve, reject) => {
    const socket = net.createConnection({ host: '127.0.0.1', port: controlPort });
    let input = '';
    const timer = setTimeout(() => socket.destroy(), 60000);
    socket.setEncoding('utf8');
    socket.on('connect', () => socket.end(JSON.stringify({ id: 1, key: controlKey,
      method: request.op, params: request }) + '\n'));
    socket.on('data', chunk => {
      input += chunk;
      const end = input.indexOf('\n');
      if (end < 0) return;
      clearTimeout(timer);
      socket.destroy();
      try {
        const reply = JSON.parse(input.slice(0, end));
        if (reply.ok) resolve(reply.result);
        else reject(Error(reply.error || '正文操作未完成。'));
      } catch { reject(Error('正文返回了无法识别的结果。')); }
    });
    socket.on('error', error => { clearTimeout(timer); reject(error); });
    socket.on('timeout', () => { clearTimeout(timer); socket.destroy(); reject(Error('正文操作超时。')); });
  });
}

async function body(req, limit = 10 * 1024 * 1024) {
  const chunks = [];
  let size = 0;
  for await (const chunk of req) {
    size += chunk.length;
    if (size > limit) throw Object.assign(Error('请求过大。'), { status: 413 });
    chunks.push(chunk);
  }
  return JSON.parse(Buffer.concat(chunks).toString('utf8'));
}

const server = http.createServer(async (req, res) => {
  const json = (status, value) => {
    res.writeHead(status, { 'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store',
      'X-Content-Type-Options': 'nosniff' });
    res.end(JSON.stringify(value));
  };
  try {
    if (req.headers.host !== `127.0.0.1:${port}` || req.headers['sec-fetch-site'] === 'cross-site' ||
        (req.headers.origin && req.headers.origin !== origin)) {
      res.writeHead(403); res.end('Origin refused'); return;
    }
    const url = new URL(req.url, origin);
    if (req.method === 'GET' && url.pathname === '/native-reading/native-drawing-channel.js') {
      res.writeHead(200, { 'Content-Type': 'text/javascript; charset=utf-8', 'Cache-Control': 'no-store',
        'X-Content-Type-Options': 'nosniff', 'Content-Security-Policy': "default-src 'self'" });
      res.end(channelAsset); return;
    }
    if (url.pathname === '/native-reading/drawing-host') {
      if (req.method !== 'POST' || req.headers.origin !== origin || req.headers['x-workspace-token'] !== token) {
        json(403, { error: '绘图会话未授权。' }); return;
      }
      const input = await body(req);
      if (input.type === 'discover') {
        json(200, { type: 'host', token, hostId, title: '当前正文', standalone: false,
          picker: true, directInsert: true, insertOnly: true, editableDiagrams: true }); return;
      }
      if (input.type !== 'request' || input.hostId !== hostId || !operations.has(input.request?.op)) {
        json(400, { error: '不支持的正文操作。' }); return;
      }
      try {
        const result = await callNative(input.request);
        json(200, { type: 'result', token, hostId, requestId: input.requestId, result });
      } catch (error) {
        json(200, { type: 'result', token, hostId, requestId: input.requestId, error: error.message });
      }
      return;
    }
    // The native page gets a loopback-only session token and a same-origin next.ai frame.
    if (req.method === 'GET' && url.pathname === '/native-reading/drawing-config.js') {
      res.writeHead(200, { 'Content-Type': 'text/javascript; charset=utf-8', 'Cache-Control': 'no-store',
        'X-Content-Type-Options': 'nosniff' });
      res.end('window.LINGDOC_DRAWING=' + JSON.stringify({ token, origin, native: true,
        aiConfigured: process.env.LINGDOC_DRAWING_AI_CONFIGURED === '1' }) + ';'); return;
    }
    if (await drawingHTTP(req, res, url)) return;
    if (drawingProxy(req, res, url)) return;
    res.writeHead(404); res.end('Not found');
  } catch (error) {
    if (!res.headersSent) json(error.status || 500, { error: error.message || '绘图服务未完成请求。' });
    else res.destroy();
  }
});
server.listen(port, '127.0.0.1');
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => server.close(() => process.exit(0)));
