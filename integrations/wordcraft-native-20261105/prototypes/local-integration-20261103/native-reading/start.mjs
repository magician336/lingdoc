// Isolated native-only trial. No React workbench and no replacement save coordinator.
import http from 'node:http';
import { createServer } from 'node:net';
import { readFile, mkdir, writeFile, access } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { randomUUID } from 'node:crypto';
import { startIntegration } from '../server.mjs';
import { createGoSources } from '../go-sources.mjs';
import { createWorkspaceService } from './workspace-service.mjs';
import { createImportStore } from './import-store.mjs';
import { createDrawingHTTP } from './drawing-http.mjs';
import { startDrawingUpstream } from '../../../drawing-upstream/launch.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
async function freePort() {
  const probe = createServer();
  await new Promise((resolve, reject) => { probe.once('error', reject); probe.listen(0, '127.0.0.1', resolve); });
  const port = probe.address().port;
  await new Promise(resolve => probe.close(resolve));
  return port;
}
const csp = "default-src 'self' blob: data:; script-src 'self' 'wasm-unsafe-eval'; connect-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' blob: data:; frame-ancestors 'self'";
export async function startNativeReading({ port = 7212, directory = path.join(here, 'runtime', 'trial') } = {}) {
  if (!Number.isInteger(port) || port < 1024 || port > 65535 || [7100,7106,7210,7211].includes(port)) throw Error('Invalid or protected trial port');
  directory = path.resolve(directory);
  const hosts = new Set([`localhost:${port}`, `127.0.0.1:${port}`]);
  const runId = randomUUID();
  const workspaceToken = randomUUID();
  const server = http.createServer((_req, res) => { res.writeHead(503); res.end('Native reading trial starting'); });
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(port, '127.0.0.1', resolve); });
  let app, drawing, closed = false;
  const stop = async () => { if (closed) return; closed = true; await new Promise(resolve => server.close(resolve)); try { await drawing?.stop(); } finally { await app?.stop(); } };
  try {
    const fixture = await readFile(path.join(here, 'fixtures', 'source.json'));
    const script = Buffer.from(`window.NATIVE_WORKSPACE_TOKEN=${JSON.stringify(workspaceToken)};\n` + await readFile(path.join(here, 'load-source.js'), 'utf8'));
    const standaloneScript = await readFile(path.join(here, 'standalone.js'));
    // Reuse the already-authorized local provider configuration. Only its path
    // crosses this launcher; Go reads credentials without exposing them here.
    if (!process.env.LINGDOC_LOCAL_AI_CONFIG) {
      const existingConfig = path.resolve(here, '../../../candidates/lingdoc-native-workspace-20261027/runtime/ai-private/local-ai.json');
      try { await access(existingConfig); process.env.LINGDOC_LOCAL_AI_CONFIG = existingConfig; }
      catch { /* Reading and editing remain available without a configured model. */ }
    }
    app = await startIntegration({ port: await freePort(), directory: path.join(directory, 'integration') });
    const sources = createGoSources({goOrigin:app.runtime.goOrigin, origin:app.runtime.origin, runtimeId:app.runtime.runtimeId});
    const imports = await createImportStore({directory:path.join(directory,'imports'),sources});
    const workspace = await createWorkspaceService({directory:path.join(directory,'workspace'),goOrigin:app.runtime.goOrigin,origin:app.runtime.origin,projectId:app.projectId,sources,imports});
    let drawingNotice = '';
    try {
      drawing = await startDrawingUpstream({ parentOrigin: `http://127.0.0.1:${port}`, configPath: process.env.LINGDOC_LOCAL_AI_CONFIG, directory: path.join(directory, 'drawing-service') });
    } catch {
      drawingNotice = '绘图服务暂不可用，请联系管理员完成绘图组件配置后重新打开。';
    }
    const drawingHTTP = await createDrawingHTTP({ directory: path.join(directory, 'drawings'), token: workspaceToken, drawingOrigin: drawing?.origin, notice: drawingNotice });
    server.removeAllListeners('request');
    server.on('request', async (req, res) => {
      try {
        if (!hosts.has(req.headers.host) || req.headers['sec-fetch-site'] === 'cross-site') { res.writeHead(403); return res.end('Host refused'); }
        const origin = `http://${req.headers.host}`;
        if (req.headers.origin && req.headers.origin !== origin) { res.writeHead(403); return res.end('Origin refused'); }
        const url = new URL(req.url, origin);
        if (await drawingHTTP(req, res, url)) return;
        if (url.pathname.startsWith('/native-reading/workspace/')) {
          if (req.headers['x-workspace-token'] !== workspaceToken) { res.writeHead(403); return res.end('Workspace token required'); }
          let result;
          if (url.pathname === '/native-reading/workspace/state' && req.method === 'GET') result = await workspace.state();
          else if (req.method === 'POST' && ['/native-reading/workspace/action','/native-reading/workspace/source','/native-reading/workspace/import'].includes(url.pathname)) {
            if (req.headers.origin !== origin) { res.writeHead(403); return res.end('Origin required'); }
            const chunks=[]; let size=0;
            const limit=url.pathname.endsWith('/import')?15*1024*1024:128*1024;
            for await(const chunk of req) { size+=chunk.length; if(size>limit) {res.writeHead(413);return res.end('Request too large');} chunks.push(chunk); }
            let input; try { input=JSON.parse(Buffer.concat(chunks).toString('utf8')); } catch {res.writeHead(400);return res.end('Invalid JSON');}
            result = url.pathname.endsWith('/import') ? {record:await workspace.importFile(input),state:await workspace.state()} : url.pathname.endsWith('/source') ? await workspace.openSource(input.sourceId,input.page,input.version) : await workspace.act(input);
          } else {res.writeHead(404);return res.end('Not found');}
          res.writeHead(200,{'Content-Type':'application/json; charset=utf-8','Cache-Control':'no-store','X-Content-Type-Options':'nosniff'});return res.end(JSON.stringify(result));
        }
        if(req.method==='GET'&&url.pathname==='/native-reading/standalone.js') {
          res.writeHead(200,{'Content-Type':'text/javascript','Cache-Control':'no-store'});return res.end(standaloneScript);
        }
        if(req.method==='GET'&&url.pathname==='/native-reading/standalone-mode.js') {
          res.writeHead(200,{'Content-Type':'text/javascript','Cache-Control':'no-store'});return res.end('window.WORDCRAFT_STANDALONE=true;');
        }
        if (req.method === 'GET' && ['/native-reading/source.json', '/native-reading/load-source.js'].includes(url.pathname)) {
          res.writeHead(200, {'Content-Type': url.pathname.endsWith('.json') ? 'application/json' : 'text/javascript', 'Cache-Control': 'no-store', 'Cross-Origin-Resource-Policy': 'same-origin', 'X-Content-Type-Options': 'nosniff'});
          return res.end(url.pathname.endsWith('.json') ? fixture : script);
        }
        const editorEntry=['/','/project'].includes(url.pathname);
        const route = editorEntry ? '/native.html' : url.pathname + url.search;
        const headers = { 'Content-Type': req.headers['content-type'] || 'application/json' };
        for (const name of ['x-host-token', 'x-integration-token', 'idempotency-key']) if (req.headers[name]) headers[name] = req.headers[name];
        if (req.headers.origin) headers.Origin = app.origin;
        let body;
        if (!['GET', 'HEAD'].includes(req.method)) {
          const chunks = []; let size = 0;
          for await (const chunk of req) { size += chunk.length; if (size > 20 * 1024 * 1024) { res.writeHead(413); return res.end('Request too large'); } chunks.push(chunk); }
          body = Buffer.concat(chunks);
        }
        const upstream = await fetch(app.origin + route, { method: req.method, headers, body, redirect: 'error', signal: AbortSignal.timeout(90000) });
        let bytes = Buffer.from(await upstream.arrayBuffer());
        if (upstream.ok && editorEntry) {
          let html=bytes.toString('utf8');
          if(url.pathname==='/') {
            for(const tag of ['<script src="/host-config.js">','<script type="module" src="/save-coordinator.js"></script>','<script type="module" src="/host.js"></script>']) {
              if(!html.includes(tag))throw Error('Standalone entry cannot verify native host separation');
            }
            html=html.replace('<script src="/host-config.js">','<script src="/native-reading/standalone-mode.js"></script><script src="/host-config.js">')
            .replace('<script type="module" src="/save-coordinator.js"></script>','')
            .replace('<script type="module" src="/host.js"></script>','<script type="module" src="/native-reading/standalone.js"></script>');
            if(/src=["']\/(?:host|save-coordinator)\.js["']/.test(html))throw Error('Standalone entry still contains a project save script');
          }
          bytes = Buffer.from(html.replace('</body>', '<script type="module" src="/native-reading/load-source.js"></script><script type="module" src="/native-reading/drawing-host.js"></script></body>'));
        }
        if (upstream.ok && url.pathname === '/host-config.js') bytes = Buffer.from(bytes.toString('utf8').replaceAll(app.origin, origin));
        res.writeHead(upstream.status, {'Content-Type': upstream.headers.get('content-type') || 'application/octet-stream', 'Cache-Control': 'no-store', 'X-Content-Type-Options': 'nosniff', 'Cross-Origin-Resource-Policy': 'same-origin', 'Content-Security-Policy': csp});
        res.end(bytes);
      } catch (error) { if (!res.headersSent) res.writeHead(error.status || 502, {'Content-Type':'application/json; charset=utf-8'}); res.end(JSON.stringify({error:error.message})); }
    });
    await mkdir(directory, { recursive: true });
    const info = { origin: `http://127.0.0.1:${port}`, port, runId, pid: process.pid, directory, upstream: app.origin, startedAt: new Date().toISOString(), scope: 'native source reading trial' };
    await writeFile(path.join(directory, 'trial.json'), JSON.stringify(info, null, 2));
    return { ...info, stop };
  } catch (error) { await stop(); throw error; }
}
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2); const options = {};
  for (let i = 0; i < args.length; i += 2) {
    if (args[i] === '--port') options.port = Number(args[i + 1]);
    else if (args[i] === '--directory') options.directory = args[i + 1];
    else throw Error('Unknown argument');
  }
  const app = await startNativeReading(options);
  console.log(JSON.stringify({ ready: true, origin: app.origin, directory: app.directory, pid: app.pid }));
  process.on('SIGINT', () => app.stop()); process.on('SIGTERM', () => app.stop());
  const timer = setInterval(async () => { try { if ((await readFile(path.join(app.directory, 'stop.request'), 'utf8')).trim() === app.runId) { clearInterval(timer); await app.stop(); } } catch {} }, 1000);
  timer.unref();
}
