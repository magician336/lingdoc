import { spawn, spawnSync } from 'node:child_process';
import http from 'node:http';
import net from 'node:net';
import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const artifacts = path.join(root, 'artifacts', 'preview');
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const result = {
  kind: 'native-workspace-preview-startup-regression',
  observedAt: new Date().toISOString(),
  cases: [],
  passed: false,
};

function previewProcess(args) {
  // When invoked through npm run verify:dev, invoke the real npm dev script without
  // shell:true (Node 24 on Windows rejects npm.cmd spawn and shell escaping is unsafe).
  if (process.env.npm_execpath) {
    return spawn(process.execPath, [process.env.npm_execpath, 'run', 'dev', '--', ...args], {
      cwd: root,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
  }
  return spawn(process.execPath, [path.join(root, 'scripts', 'dev.mjs'), ...args], {
    cwd: root,
    stdio: ['ignore', 'pipe', 'pipe'],
  });
}

async function stop(child) {
  if (!child) return true;
  if (child.exitCode === null && child.pid) {
    if (process.platform === 'win32') spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore' });
    else child.kill('SIGTERM');
  }
  for (let index = 0; index < 50 && child.exitCode === null; index++) await sleep(100);
  return child.exitCode !== null;
}

async function portFree(port) {
  const server = net.createServer();
  return new Promise(resolve => {
    server.once('error', () => resolve(false));
    server.listen(port, '127.0.0.1', () => server.close(() => resolve(true)));
  });
}

async function availablePort() {
  const server = net.createServer();
  await new Promise((resolve, reject) => server.once('error', reject).listen(0, '127.0.0.1', resolve));
  const port = server.address().port;
  await new Promise(resolve => server.close(resolve));
  return port;
}

async function portClosed(port) {
  return new Promise((resolve, reject) => {
    const socket = net.createConnection({ host: '127.0.0.1', port });
    const timer = setTimeout(() => { socket.destroy(); reject(new Error(`port ${port} probe timeout`)); }, 1500);
    socket.once('connect', () => { clearTimeout(timer); socket.end(); resolve(false); });
    socket.once('error', () => { clearTimeout(timer); resolve(true); });
  });
}

async function statusWithHost(port, host) {
  return new Promise((resolve, reject) => {
    const request = http.request({
      hostname: '127.0.0.1', port, path: '/', headers: { Host: host }, timeout: 5000,
    }, response => {
      response.resume();
      response.once('end', () => resolve(response.statusCode));
    });
    request.once('timeout', () => request.destroy(new Error('raw Host probe timeout')));
    request.once('error', reject);
    request.end();
  });
}

async function runCase(name, args, port) {
  const row = { name, args, port, passed: false };
  result.cases.push(row);
  if (!(await portFree(port))) {
    row.skipped = 'port already occupied; foreign process left untouched';
    return;
  }
  const child = previewProcess(args);
  let output = '';
  child.stdout.on('data', chunk => { output += String(chunk); });
  child.stderr.on('data', chunk => { output += String(chunk); });
  try {
    let ready;
    for (let index = 0; index < 600 && !ready; index++) {
      if (child.exitCode !== null) throw new Error(`preview exited early: ${output}`);
      for (const line of output.split(/\r?\n/)) {
        try {
          const value = JSON.parse(line);
          if (value.ready && value.origin === `http://127.0.0.1:${port}`) ready = value;
        } catch {}
      }
      if (!ready) await sleep(100);
    }
    if (!ready) throw new Error(`preview readiness timeout: ${output}`);
    if (ready.bind !== '127.0.0.1') throw new Error(`preview escaped loopback binding: ${ready.bind}`);
    row.goOrigin = ready.goOrigin;
    row.runtimeId = ready.runtimeId;
    row.goBinary = ready.goBinary;
    const response = await fetch(`${ready.origin}/`, { signal: AbortSignal.timeout(5000) });
    const html = await response.text();
    if (!response.ok || !html.includes('wordcraft_canvas') || !html.includes('host-config.js')) {
      throw new Error('preview did not serve the real workspace');
    }
    row.httpStatus = response.status;
    row.realWorkspace = true;
    row.loopbackOnly = true;
    row.reservedPortsRefused = ready.portIsolation?.reservedPortsRefused;

    // Kimi Work opens localhost even when the launcher receives a loopback bind address.
    // Exercise that exact browser path, including host-config and a real same-origin API call.
    const localhostUrl = new URL(ready.origin);
    localhostUrl.hostname = 'localhost';
    const localhostOrigin = localhostUrl.origin;
    const localhostResponse = await fetch(`${localhostOrigin}/`, { signal: AbortSignal.timeout(5000) });
    const localhostHtml = await localhostResponse.text();
    row.localhostPage = localhostResponse.ok && localhostHtml.includes('wordcraft_canvas');
    if (!row.localhostPage) throw new Error(`localhost preview failed: ${localhostResponse.status} ${localhostHtml}`);

    const configResponse = await fetch(`${localhostOrigin}/host-config.js`, { signal: AbortSignal.timeout(5000) });
    const configText = await configResponse.text();
    const configPrefix = 'window.WORDCRAFT_HOST_CONFIG=';
    if (!configResponse.ok || !configText.startsWith(configPrefix) || !configText.endsWith(';')) {
      throw new Error('localhost host-config was not served correctly');
    }
    const config = JSON.parse(configText.slice(configPrefix.length, -1));
    row.localhostConfigOrigin = config.origin === localhostOrigin;
    if (!row.localhostConfigOrigin) throw new Error(`host-config origin mismatch: ${config.origin}`);
    row.goVersionChain = typeof config.goOrigin === 'string' && /^http:\/\/(127\.0\.0\.1|localhost):\d+$/.test(config.goOrigin);
    row.goOrigin = config.goOrigin;
    if (!row.goVersionChain) throw new Error(`preview did not provide a loopback Go origin: ${config.goOrigin}`);

    const openResponse = await fetch(`${localhostOrigin}/api/open`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Host-Token': config.token, Origin: localhostOrigin },
      body: '{}',
      signal: AbortSignal.timeout(5000),
    });
    const openResult = await openResponse.json();
    row.localhostApi = openResponse.ok && typeof openResult.data === 'string' && openResult.data.length > 0;
    if (!row.localhostApi) throw new Error(`localhost API failed: ${openResponse.status} ${JSON.stringify(openResult)}`);

    const foreignHostStatus = await statusWithHost(port, 'external.invalid');
    row.foreignHostRejected = foreignHostStatus === 403;
    if (!row.foreignHostRejected) throw new Error(`foreign Host was not rejected: ${foreignHostStatus}`);

    const foreignOriginResponse = await fetch(`${localhostOrigin}/api/open`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Host-Token': config.token, Origin: 'http://external.invalid' },
      body: '{}',
      signal: AbortSignal.timeout(5000),
    });
    row.foreignOriginRejected = foreignOriginResponse.status === 403;
    await foreignOriginResponse.arrayBuffer();
    if (!row.foreignOriginRejected) throw new Error(`foreign Origin was not rejected: ${foreignOriginResponse.status}`);
  } catch (error) {
    row.error = String(error);
  } finally {
    if (row.goOrigin) {
      // Stop the owned Go process first so delivery-runtime can release DS and its lock.
      await fetch(`${row.goOrigin}/shutdown`, { method: 'POST',
        headers: { 'X-LingDoc-Runtime-ID': row.runtimeId }, signal: AbortSignal.timeout(2000) }).catch(() => {});
      for (let index = 0; index < 150 && child.exitCode === null; index++) await sleep(100);
    }
    row.processExited = await stop(child);
    if (row.processExited) row.portClosed = await portClosed(port);
  }
  row.passed = row.httpStatus === 200 && row.realWorkspace === true && row.loopbackOnly === true
    && row.localhostPage === true && row.localhostConfigOrigin === true && row.localhostApi === true && row.goVersionChain === true
    && row.foreignHostRejected === true && row.foreignOriginRejected === true
    && row.processExited === true && row.portClosed === true;
}

const dynamicPort = await availablePort();
await runCase('default npm run dev', [], 7102);
await runCase('Kimi wildcard host on 7100', ['--host', '0.0.0.0', '--port', '7100'], 7100);
await runCase('IPv6 wildcard host on 7101', ['--host', '::', '--port', '7101'], 7101);
await runCase('equals-form localhost and dynamic port', [`--host=localhost`, `--port=${dynamicPort}`], dynamicPort);

let blocker;
try {
  blocker = http.createServer((request, response) => response.end('occupied'));
  await new Promise((resolve, reject) => blocker.once('error', reject).listen(0, '127.0.0.1', resolve));
  const port = blocker.address().port;
  const child = previewProcess(['--host', '0.0.0.0', '--port', String(port)]);
  let output = '';
  child.stdout.on('data', chunk => { output += String(chunk); });
  child.stderr.on('data', chunk => { output += String(chunk); });
  for (let index = 0; index < 100 && child.exitCode === null; index++) await sleep(100);
  await stop(child);
  const rejected = child.exitCode !== 0 && output.includes('EADDRINUSE');
  result.occupiedPort = {
    port,
    rejectedWithAddressInUse: rejected,
    foreignServerStillListening: blocker.listening,
    passed: rejected && blocker.listening,
  };
} catch (error) {
  result.occupiedPort = { passed: false, error: String(error) };
} finally {
  if (blocker?.listening) await new Promise(resolve => blocker.close(resolve));
}

result.passed = result.cases.every(row => row.passed || row.skipped) && result.occupiedPort?.passed === true;
await mkdir(artifacts, { recursive: true });
await writeFile(path.join(artifacts, 'startup-regression.json'), JSON.stringify(result, null, 2));
console.log(JSON.stringify({
  artifact: path.join(artifacts, 'startup-regression.json'),
  passed: result.passed,
  cases: result.cases.map(row => ({ name: row.name, port: row.port, passed: row.passed, skipped: row.skipped, error: row.error })),
  occupiedPort: result.occupiedPort,
}, null, 2));
if (!result.passed) process.exitCode = 1;
