// Local first-version runtime. Reuses the existing Go/DS interfaces and WASM.
// CLI: node scripts/delivery-runtime.mjs [--runtime-dir PATH] [--port PORT] [--go-port PORT]
// Verification may import startDeliveryRuntime({runtimeDir}) and await stop().
import { spawn } from 'node:child_process';
import { createWriteStream } from 'node:fs';
import { mkdir, open, readFile, rm, writeFile } from 'node:fs/promises';
import { createServer } from 'node:net';
import path from 'node:path';
import { createHash, randomUUID } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { root, distribution, goEnvironment, goRoot, runtimeDirectory, shellQuote, wslPath } from './local-environment.mjs';
import { checkEnvironment, printEnvironment } from './check-environment.mjs';
import { startDocumentReader } from './docreader-runtime.mjs';

const delay = ms => new Promise(resolve => setTimeout(resolve, ms));

async function availablePort(requested = 0) {
  const port = Number(requested);
  if (!Number.isInteger(port) || port < 0 || port > 65535 || (port > 0 && port < 1024) || port === 5255) {
    throw new Error(`Unsupported local port: ${requested}`);
  }
  const server = createServer();
  return new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(port, '127.0.0.1', () => {
      const selected = server.address().port;
      server.close(error => error ? reject(error) : resolve(selected));
    });
  });
}

function launch(command, args, options, logFile) {
  const log = createWriteStream(logFile, { flags: 'a' });
  const child = spawn(command, args, { ...options, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] });
  const state = { child, error: null, result: null, tail: '' };
  for (const stream of [child.stdout, child.stderr]) stream.on('data', chunk => {
    log.write(chunk);
    state.tail = (state.tail + chunk.toString()).slice(-16000);
  });
  log.on('error', error => { state.error = error; });
  child.once('error', error => { state.error = error; });
  state.done = new Promise(resolve => child.once('close', (code, signal) => {
    state.result = { code, signal };
    log.end(() => resolve(state.result));
  }));
  return state;
}

function checkRunning(service) {
  if (service.error) throw service.error;
  if (service.result) throw new Error(`Service exited before ready (${service.result.code}): ${service.tail}`);
}

async function waitUntil(check, services, deadline, signal) {
  while (Date.now() < deadline) {
    signal?.throwIfAborted();
    services.forEach(checkRunning);
    const result = await check();
    if (result) return result;
    await delay(100);
  }
  throw new Error('Local runtime startup timed out; retained logs contain the first failure');
}

async function signalGoGroup(pidFile, signal) {
  const pid = (await readFile(pidFile, 'utf8').catch(() => '')).trim();
  if (!/^\d+$/.test(pid) || Number(pid) <= 1) return;
  // The PID file is unique to this invocation. setsid makes this our process
  // group, including go test/compiler children; never kill by name or by port.
  const child = spawn('wsl.exe', ['-d', distribution, '--', '/bin/kill', `-${signal}`, '--', `-${pid}`], {
    windowsHide: true, stdio: 'ignore',
  });
  await new Promise(resolve => { child.once('error', resolve); child.once('close', resolve); });
}

const activeRuntimeError = lockPath => new Error(`Runtime already active or not cleanly stopped: ${lockPath}; do not reuse its data until its processes are closed`);
const validPid = pid => Number.isSafeInteger(pid) && pid > 1;
const validStartedAt = value => typeof value === 'string' && Number.isFinite(Date.parse(value));
const canonicalRuntimeId = value => typeof value === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value);

function windowsProcessAlive(pid) {
  try { process.kill(pid, 0); return true; }
  catch (error) {
    if (error.code === 'ESRCH') return false;
    if (error.code === 'EPERM') return true;
    throw error;
  }
}

async function wslProcessGroupAlive(pgid, { signal, timeoutMs }) {
  signal?.throwIfAborted();
  const child = spawn('wsl.exe', ['-d', distribution, '--', '/bin/ps', '-eo', 'pgid='], {
    windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'],
  });
  let stdout = '', stderr = '', settled = false;
  child.stdout.on('data', chunk => { stdout += chunk; });
  child.stderr.on('data', chunk => { stderr += chunk; });
  return new Promise((resolve, reject) => {
    const finish = (error, result) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      signal?.removeEventListener('abort', onAbort);
      // Only this owned probe is terminated on timeout/abort. Recovery never
      // signals a recorded runtime PID or process group.
      if (error && child.exitCode === null && child.signalCode === null) child.kill();
      if (error) reject(error); else resolve(result);
    };
    const onAbort = () => finish(signal.reason instanceof Error ? signal.reason : new Error('Runtime startup aborted'));
    const timer = setTimeout(() => finish(new Error('WSL process probe timed out')), Math.min(5000, timeoutMs));
    signal?.addEventListener('abort', onAbort, { once: true });
    child.once('error', error => finish(error));
    child.once('close', code => {
      if (code !== 0) { finish(new Error(`WSL process probe failed (${code}): ${stderr}`)); return; }
      finish(null, stdout.split(/\r?\n/).some(line => line.trim() === String(pgid)));
    });
  });
}

async function recoverableRuntimeLock(directory, lockPath, { signal, timeoutMs }) {
  try {
    const raw = await readFile(lockPath, 'utf8');
    const owner = JSON.parse(raw);
    if (!owner || !canonicalRuntimeId(owner.runtimeId) || !validPid(owner.pid) || !validStartedAt(owner.startedAt)) return null;
    const logs = path.join(directory, 'logs', owner.runtimeId);
    const metadata = JSON.parse(await readFile(path.join(logs, 'runtime.json'), 'utf8'));
    const parserPresent = metadata.parserPid != null;
    if (metadata.runtimeId !== owner.runtimeId || metadata.pid !== owner.pid || !validStartedAt(metadata.startedAt)
      || owner.startedAt !== metadata.startedAt || path.resolve(metadata.directory) !== directory
      || typeof metadata.processesExited !== 'boolean' || !validPid(metadata.goLauncherPid) || !validPid(metadata.dsPid)
      || (parserPresent && (!validPid(metadata.parserPid) || !validPid(metadata.parserLauncherPid)))
      || (!parserPresent && metadata.parserLauncherPid != null)) return null;
    const goPidText = (await readFile(path.join(logs, 'go.pid'), 'utf8')).trim();
    if (!/^[1-9]\d*$/.test(goPidText)) return null;
    const goPid = Number(goPidText);
    if (!validPid(goPid)) return null;

    // A reused live PID also blocks recovery. This can refuse a harmless stale
    // lock, but can never mistake a live process for the recorded dead owner.
    const windowsPids = [owner.pid, metadata.goLauncherPid, metadata.dsPid, metadata.parserLauncherPid].filter(validPid);
    for (const pid of windowsPids) if (windowsProcessAlive(pid)) return null;
    // Go and docreader run in their own WSL process groups. Checking only the
    // group leader could miss orphaned compiler or parser children.
    for (const pgid of [goPid, metadata.parserPid].filter(validPid)) {
      if (await wslProcessGroupAlive(pgid, { signal, timeoutMs })) return null;
    }
    return raw;
  } catch (error) {
    signal?.throwIfAborted();
    return null;
  }
}

async function acquireRecoveryMutex(lockPath) {
  // A named pipe is kernel-owned and disappears if this process crashes. Unlike
  // a guard file, it cannot become a permanent stale lock or be deleted by a
  // competing recovery. Legacy runtime.lock.recovery files are inert and kept.
  const pipe = `\\\\.\\pipe\\lingdoc-delivery-runtime-${createHash('sha256').update(lockPath.toLowerCase()).digest('hex')}`;
  const mutex = createServer();
  try {
    await new Promise((resolve, reject) => {
      mutex.once('error', reject);
      mutex.listen(pipe, resolve);
    });
  } catch { throw activeRuntimeError(lockPath); }
  return () => new Promise(resolve => mutex.close(resolve));
}

async function acquireRuntimeLock(directory, runtimeId, startedAt, { signal, timeoutMs }) {
  const lockPath = path.join(directory, 'runtime.lock');
  let lock;
  try {
    lock = await open(lockPath, 'wx');
  } catch (error) {
    if (error.code !== 'EEXIST') throw error;
    const release = await acquireRecoveryMutex(lockPath);
    try {
      const stale = await recoverableRuntimeLock(directory, lockPath, { signal, timeoutMs });
      if (!stale || await readFile(lockPath, 'utf8').catch(() => null) !== stale) throw activeRuntimeError(lockPath);
      await rm(lockPath);
      lock = await open(lockPath, 'wx').catch(error => {
        if (error.code === 'EEXIST') throw activeRuntimeError(lockPath);
        throw error;
      });
    } finally {
      await release();
    }
  }
  await lock.writeFile(JSON.stringify({ runtimeId, pid: process.pid, startedAt }));
  await lock.close();
  return lockPath;
}

export async function startDeliveryRuntime({ runtimeDir = runtimeDirectory, port = 0, goPort = 0, timeoutMs = 300000, signal, research = false } = {}) {
  if (process.platform !== 'win32') throw new Error('This local candidate uses Windows DS fonts and the existing WSL Go toolchain');
  if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) throw new Error('timeoutMs must be positive');
  signal?.throwIfAborted();
  const directory = path.resolve(runtimeDir);
  const runtimeId = randomUUID();
  const startedAt = new Date().toISOString();
  await mkdir(directory, { recursive: true });
  const lockPath = await acquireRuntimeLock(directory, runtimeId, startedAt, { signal, timeoutMs });
  const logs = path.join(directory, 'logs', runtimeId);
  const pidFile = path.join(logs, 'go.pid');
  let go, ds, parser, origin, goOrigin, metadata, stopping;
  let goVerified = false;
  const stop = () => stopping ??= (async () => {
    const failures = [];
    try {
    if (ds && !ds.result) ds.child.kill('SIGTERM');
    if (go && !go.result) {
      if (goVerified) {
        try {
          const health = await fetch(`${goOrigin}/healthz`, { signal: AbortSignal.timeout(1000) }).then(r => r.json());
          if (health.runtimeId === runtimeId) {
            const response = await fetch(`${goOrigin}/shutdown`, { method: 'POST',
              headers: research ? { 'X-LingDoc-Runtime-ID': runtimeId } : {}, signal: AbortSignal.timeout(1000) });
            await response.arrayBuffer();
          }
        } catch { /* The child may already be exiting. */ }
      }
      await Promise.race([go.done, delay(goVerified ? 5000 : 100)]);
      if (!go.result) await signalGoGroup(pidFile, 'TERM');
      await Promise.race([go.done, delay(2000)]);
      if (!go.result) await signalGoGroup(pidFile, 'KILL');
    }
    for (const service of [ds, go].filter(Boolean)) {
      await Promise.race([service.done, delay(3000)]);
      if (!service.result) failures.push(new Error(`Owned process ${service.child.pid} has not exited; retain ${lockPath}`));
    }
    } catch (error) { failures.push(error); }
    finally { try { if (parser) await parser.stop(); } catch (error) { failures.push(error); } }
    if (failures.length) throw new AggregateError(failures, 'Runtime cleanup incomplete; retain ' + lockPath);
    if (metadata) await writeFile(path.join(logs, 'runtime.json'), JSON.stringify({ ...metadata, stoppedAt: new Date().toISOString(), processesExited: true }, null, 2));
    const owner = JSON.parse(await readFile(lockPath, 'utf8'));
    if (owner.runtimeId !== runtimeId) throw new Error('Runtime lock changed; refusing to remove another owner’s lock');
    await rm(lockPath);
  })();
  try {
    await mkdir(logs, { recursive: true });
    await mkdir(path.join(directory, 'ds'), { recursive: true });
    const dsPort = await availablePort(port);
    const selectedGoPort = await availablePort(goPort);
    if (selectedGoPort === dsPort) throw new Error('Go and DS require different ports');
    origin = `http://127.0.0.1:${dsPort}`;
    goOrigin = `http://127.0.0.1:${selectedGoPort}`;
    const env = {
      ...goEnvironment,
      LINGDOC_ISOLATED_HTTP_LISTEN: '1', LINGDOC_ISOLATED_HTTP_ADDR: `127.0.0.1:${selectedGoPort}`,
      LINGDOC_SQLITE_PATH: wslPath(path.join(directory, 'lingdoc.db')),
      LINGDOC_DS_ORIGIN: origin, LINGDOC_ISOLATED_HTTP_RUNTIME_ID: runtimeId,
    };
    if (research) Object.assign(env, {
      LINGDOC_LOCAL_ADDR: '127.0.0.1:' + selectedGoPort, LINGDOC_LOCAL_ORIGIN: origin,
      LINGDOC_LOCAL_RUNTIME_ID: runtimeId, DB_DRIVER: 'sqlite', DB_PATH: wslPath(path.join(directory, 'lingdoc.db')),
      RETRIEVE_DRIVER: 'sqlite', STORAGE_TYPE: 'local', LOCAL_STORAGE_BASE_DIR: wslPath(path.join(directory, 'files')),
      REDIS_ADDR: '', DUCKDB_SKIP_EXTENSION_LOAD: '1',
    });
    if (research) {
      parser = await startDocumentReader({ directory: logs, signal });
      env.DOCREADER_ADDR = parser.address;
      env.DOCREADER_TRANSPORT = 'grpc';
      // Pass a file path only. Credentials are read inside Go, never placed
      // in the shell command, process arguments, runtime metadata or logs.
      const aiConfig = process.env.LINGDOC_LOCAL_AI_CONFIG;
      if (aiConfig) Object.assign(env, {
        LINGDOC_LOCAL_AI_CONFIG: aiConfig.startsWith('/') ? aiConfig : wslPath(path.resolve(aiConfig)),
        LLM_DEBUG_LOG: 'false', LANGFUSE_ENABLED: 'false',
      });
    }
    const researchBinary = process.env.LINGDOC_RESEARCH_GO_BINARY;
    if (researchBinary && !researchBinary.startsWith('/')) throw new Error('LINGDOC_RESEARCH_GO_BINARY must be an absolute WSL path');
    const goCommand = research ? (researchBinary ? shellQuote(researchBinary) : 'go run -tags sqlite_fts5 ./cmd/lingdoc-local') : "go test ./internal/lingdoc/workspace -run '^TestServeIsolatedHTTP$' -count=1 -v -timeout 0";
    const command = `printf '%s\\n' "$$" > ${shellQuote(wslPath(pidFile))}; cd ${shellQuote(wslPath(goRoot))} && exec env ${Object.entries(env).map(([key, value]) => `${key}=${shellQuote(value)}`).join(' ')} ${goCommand}`;
    go = launch('wsl.exe', ['-d', distribution, '--', 'setsid', '--wait', 'sh', '-c', command], {}, path.join(logs, 'go.log'));
    const deadline = Date.now() + timeoutMs;
    await waitUntil(async () => {
      try {
        const response = await fetch(`${goOrigin}/healthz`, { signal: AbortSignal.timeout(500) });
        if (!response.ok) return false;
        const health = await response.json();
        return health.runtimeId === runtimeId && health.persistent === true;
      } catch { return false; }
    }, [go], deadline, signal);
    goVerified = true;
    ds = launch(process.execPath, [path.join(root, 'ds', 'r11-bridge-combination-20261025', 'host', 'server.mjs')], {
      env: { ...process.env, WORDCRAFT_HOST_PORT: String(dsPort), WORDCRAFT_GO_ORIGIN: goOrigin,
        WORDCRAFT_WEB_DIR: path.join(root, 'build', 'out'), WORDCRAFT_DATA_DIR: path.join(directory, 'ds'),
        WORDCRAFT_RESEARCH_ENABLED: research ? '1' : '0',
        WORDCRAFT_AUDIT_PATH: path.join(logs, 'host-audit.jsonl') },
    }, path.join(logs, 'ds.log'));
    await waitUntil(async () => {
      // A foreign process could occupy the port between reservation and bind.
      // Require the ready record from our own DS child before probing its URL.
      const ownedReady = ds.tail.split(/\r?\n/).some(line => {
        try { const event = JSON.parse(line); return event.ready === true && event.pid === ds.child.pid && path.resolve(event.data) === path.join(directory, 'ds'); }
        catch { return false; }
      });
      if (!ownedReady) return false;
      try { const response = await fetch(`${origin}/`, { signal: AbortSignal.timeout(500) }); await response.arrayBuffer(); return response.ok; }
      catch { return false; }
    }, [go, ds], deadline, signal);
    metadata = { runtimeId, origin, goOrigin, directory, research, database: path.join(directory, 'lingdoc.db'),
      dsData: path.join(directory, 'ds'), logs, pid: process.pid, goLauncherPid: go.child.pid, dsPid: ds.child.pid,
      parserPid: parser?.pid, parserLauncherPid: parser?.launcherPid, parserAddress: parser?.address, startedAt, processesExited: false };
    await writeFile(path.join(logs, 'runtime.json'), JSON.stringify(metadata, null, 2));
    const finished = Promise.race([go.done, ds.done, ...(parser ? [parser.done] : [])]).then(async result => {
      const unexpected = !stopping;
      await stop();
      if (unexpected) throw new Error(`A runtime child exited unexpectedly (${result.code}); see ${logs}`);
    });
    finished.catch(() => {}); // callers may use stop() instead of awaiting finished
    return { ...metadata, stop, finished };
  } catch (error) {
    await stop().catch(cleanupError => { error.message += `; cleanup: ${cleanupError.message}`; });
    throw error;
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const options = {};
  try {
    const names = { '--runtime-dir': 'runtimeDir', '--port': 'port', '--go-port': 'goPort' };
    const args = process.argv.slice(2);
    for (let index = 0; index < args.length; index++) {
      const key = names[args[index]];
      const value = args[++index];
      if (!key || !value || value.startsWith('--')) throw new Error('Usage: delivery-runtime.mjs [--runtime-dir PATH] [--port PORT] [--go-port PORT]');
      options[key] = key === 'runtimeDir' ? value : Number(value);
    }
    const controller = new AbortController();
    let runtime;
    const shutdown = () => { controller.abort(); runtime?.stop().catch(error => { console.error(error.message); process.exitCode = 1; }); };
    process.on('SIGINT', shutdown);
    process.on('SIGTERM', shutdown);
    const environment = await checkEnvironment({ profile: 'run', runtimeDir: options.runtimeDir, signal: controller.signal });
    printEnvironment(environment);
    if (!environment.ok) throw new Error('环境缺项，服务未启动。按上方提示修复后再运行 npm start。');
    runtime = await startDeliveryRuntime({ ...options, signal: controller.signal });
    console.log(JSON.stringify({ event: 'delivery-runtime-ready', ...runtime, stop: undefined, finished: undefined }));
    console.log(`LingDoc 试用：${runtime.origin}/`);
    try { await runtime.finished; }
    finally { await runtime.stop(); process.off('SIGINT', shutdown); process.off('SIGTERM', shutdown); }
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
