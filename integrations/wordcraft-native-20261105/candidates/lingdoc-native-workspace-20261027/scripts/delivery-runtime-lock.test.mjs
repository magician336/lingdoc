import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { cp, mkdir, readFile, rename, rm, writeFile } from 'node:fs/promises';
import { createServer } from 'node:net';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { startDeliveryRuntime } from './delivery-runtime.mjs';
import { distribution, shellQuote, wslPath } from './local-environment.mjs';

const candidateRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const testRoot = path.join(candidateRoot, 'runtime', 'stale-lock-tests');
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));

async function availablePort() {
  const server = createServer();
  await new Promise((resolve, reject) => server.once('error', reject).listen(0, '127.0.0.1', resolve));
  const port = server.address().port;
  await new Promise(resolve => server.close(resolve));
  return port;
}

function windowsProcessAlive(pid) {
  try { process.kill(pid, 0); return true; }
  catch (error) { if (error.code === 'ESRCH') return false; return true; }
}

function wslGroupAlive(pgid) {
  const result = spawnSync('wsl.exe', ['-d', distribution, '--', '/bin/ps', '-eo', 'pgid='], {
    windowsHide: true, encoding: 'utf8', timeout: 10000,
  });
  if (result.error || result.status !== 0) throw result.error || new Error(`WSL process probe failed: ${result.status}`);
  return result.stdout.split(/\r?\n/).some(line => line.trim() === String(pgid));
}

async function waitForChild(child) {
  for (let index = 0; index < 100 && child.exitCode === null; index++) await sleep(100);
  return child.exitCode !== null;
}

async function startOwner(runtimeDir, { research = false } = {}) {
  const port = await availablePort();
  const moduleUrl = pathToFileURL(path.join(candidateRoot, 'scripts', 'delivery-runtime.mjs')).href;
  const source = `
    import { startDeliveryRuntime } from ${JSON.stringify(moduleUrl)};
    const runtime = await startDeliveryRuntime({ runtimeDir: process.argv[1], port: Number(process.argv[2]), timeoutMs: 240000, research: ${research} });
    console.log(JSON.stringify({ ready: true, origin: runtime.origin, goOrigin: runtime.goOrigin, parserLauncherPid: runtime.parserLauncherPid }));
    await runtime.finished;
  `;
  const child = spawn(process.execPath, ['--input-type=module', '-e', source, runtimeDir, String(port)], {
    cwd: candidateRoot, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'],
  });
  let output = '';
  child.stdout.on('data', chunk => { output += String(chunk); });
  child.stderr.on('data', chunk => { output += String(chunk); });
  let ready;
  for (let index = 0; index < 2400 && !ready; index++) {
    if (child.exitCode !== null) throw new Error(`Managed runtime exited before ready: ${output}`);
    for (const line of output.split(/\r?\n/)) {
      try { const value = JSON.parse(line); if (value.ready) ready = value; } catch {}
    }
    if (!ready) await sleep(100);
  }
  if (!ready) throw new Error(`Managed runtime readiness timeout: ${output}`);
  const lock = await readFile(path.join(runtimeDir, 'runtime.lock'), 'utf8');
  const owner = JSON.parse(lock);
  const metadata = JSON.parse(await readFile(path.join(runtimeDir, 'logs', owner.runtimeId, 'runtime.json'), 'utf8'));
  const goPid = Number((await readFile(path.join(runtimeDir, 'logs', owner.runtimeId, 'go.pid'), 'utf8')).trim());
  return { child, ready, output: () => output, lock, metadata, goPid };
}

function taskkill(pid, tree = false) {
  if (!pid || !windowsProcessAlive(pid)) return;
  const args = ['/PID', String(pid), '/F'];
  if (tree) args.push('/T');
  const result = spawnSync('taskkill', args, { stdio: 'ignore', timeout: 10000 });
  if (result.error) throw result.error;
}

async function stopWslGroup(pgid) {
  if (!pgid || !(await wslGroupAlive(pgid))) return;
  spawnSync('wsl.exe', ['-d', distribution, '--', '/bin/kill', '-TERM', '--', `-${pgid}`], { stdio: 'ignore', timeout: 10000 });
  for (let index = 0; index < 50 && (await wslGroupAlive(pgid)); index++) await sleep(100);
  if (await wslGroupAlive(pgid)) {
    spawnSync('wsl.exe', ['-d', distribution, '--', '/bin/kill', '-KILL', '--', `-${pgid}`], { stdio: 'ignore', timeout: 10000 });
  }
  for (let index = 0; index < 50 && (await wslGroupAlive(pgid)); index++) await sleep(100);
  assert.equal(await wslGroupAlive(pgid), false, `managed WSL process group ${pgid} must exit`);
}

async function stopOwnedProcesses(owner) {
  if (!owner) return;
  const { metadata, goPid } = owner;
  await stopWslGroup(goPid);
  if (metadata.parserPid) await stopWslGroup(metadata.parserPid);
  taskkill(metadata.dsPid, true);
  taskkill(metadata.goLauncherPid, true);
  taskkill(metadata.parserLauncherPid, true);
  taskkill(owner.child.pid, true);
  await waitForChild(owner.child);
  for (const pid of [metadata.pid, metadata.dsPid, metadata.goLauncherPid, metadata.parserLauncherPid].filter(Boolean)) {
    assert.equal(windowsProcessAlive(pid), false, `managed Windows process ${pid} must exit`);
  }
}

async function stopOwnerCleanly(owner) {
  if (!owner) return;
  if (owner.child.exitCode === null) {
    await fetch(`${owner.ready.goOrigin}/shutdown`, { method: 'POST', signal: AbortSignal.timeout(2000) }).catch(() => {});
    await waitForChild(owner.child);
  }
  await stopOwnedProcesses(owner);
}

async function removeIsolatedDirectory(runtimeDir) {
  await rm(runtimeDir, { recursive: true, force: true });
}

test('recovers a stale lock only after all owned runtime processes are dead', { timeout: 300000 }, async () => {
  const runtimeDir = path.join(testRoot, `dead-${randomUUID()}`);
  let owner, runtime;
  try {
    owner = await startOwner(runtimeDir);
    taskkill(owner.child.pid);
    await waitForChild(owner.child);
    await stopOwnedProcesses(owner);
    assert.equal(await readFile(path.join(runtimeDir, 'runtime.lock'), 'utf8'), owner.lock);

    runtime = await startDeliveryRuntime({ runtimeDir, port: await availablePort(), timeoutMs: 240000 });
    const response = await fetch(runtime.origin, { signal: AbortSignal.timeout(5000) });
    assert.equal(response.status, 200);
    await response.arrayBuffer();
    const replacement = JSON.parse(await readFile(path.join(runtimeDir, 'runtime.lock'), 'utf8'));
    assert.notEqual(replacement.runtimeId, owner.metadata.runtimeId);

    const oldGoPid = owner.goPid;
    await runtime.stop();
    runtime = null;
    await assert.rejects(readFile(path.join(runtimeDir, 'runtime.lock'), 'utf8'), { code: 'ENOENT' });
    assert.equal(await wslGroupAlive(oldGoPid), false);
  } finally {
    if (runtime) await runtime.stop().catch(() => {});
    await stopOwnedProcesses(owner);
    await removeIsolatedDirectory(runtimeDir);
  }
});

test('rejects startup and preserves the lock while its owner is alive', { timeout: 300000 }, async () => {
  const runtimeDir = path.join(testRoot, `live-owner-${randomUUID()}`);
  let owner, duplicate;
  try {
    owner = await startOwner(runtimeDir);
    let rejection;
    try { duplicate = await startDeliveryRuntime({ runtimeDir, port: await availablePort(), timeoutMs: 1000 }); }
    catch (error) { rejection = error; }
    assert.match(rejection?.message || '', /Runtime already active or not cleanly stopped/);
    assert.equal(await readFile(path.join(runtimeDir, 'runtime.lock'), 'utf8'), owner.lock);
    const response = await fetch(owner.ready.origin, { signal: AbortSignal.timeout(5000) });
    assert.equal(response.status, 200);
    await response.arrayBuffer();
  } finally {
    if (duplicate) await duplicate.stop().catch(() => {});
    await stopOwnerCleanly(owner);
    await removeIsolatedDirectory(runtimeDir);
  }
});

test('rejects startup when the owner is dead but an owned runtime process is alive', { timeout: 300000 }, async () => {
  const runtimeDir = path.join(testRoot, `live-child-${randomUUID()}`);
  let owner, duplicate;
  try {
    owner = await startOwner(runtimeDir);
    taskkill(owner.child.pid);
    await waitForChild(owner.child);
    // Keep one explicitly managed child alive in the recorded runtime slot.
    // This avoids depending on console-job behavior for the other real children.
    const survivor = spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], { windowsHide: true, stdio: 'ignore' });
    owner.metadata.dsPid = survivor.pid;
    const metadataPath = path.join(runtimeDir, 'logs', owner.metadata.runtimeId, 'runtime.json');
    await writeFile(metadataPath, JSON.stringify(owner.metadata, null, 2));
    assert.equal(windowsProcessAlive(survivor.pid), true);

    let rejection;
    try { duplicate = await startDeliveryRuntime({ runtimeDir, port: await availablePort(), timeoutMs: 1000 }); }
    catch (error) { rejection = error; }
    assert.match(rejection?.message || '', /Runtime already active or not cleanly stopped/);
    assert.equal(await readFile(path.join(runtimeDir, 'runtime.lock'), 'utf8'), owner.lock);
    assert.equal(windowsProcessAlive(survivor.pid), true);
  } finally {
    if (duplicate) await duplicate.stop().catch(() => {});
    await stopOwnedProcesses(owner);
    await removeIsolatedDirectory(runtimeDir);
  }
});

test('preserves corrupt or unverifiable lock metadata', async () => {
  for (const [name, contents] of [
    ['corrupt', '{not-json'],
    ['invalid', JSON.stringify({ runtimeId: 'bad', pid: 0, startedAt: 'not-a-date' })],
    ['missing-runtime-metadata', JSON.stringify({ runtimeId: randomUUID(), pid: 99999999, startedAt: new Date().toISOString() })],
  ]) {
    const runtimeDir = path.join(testRoot, `${name}-${randomUUID()}`);
    await mkdir(runtimeDir, { recursive: true });
    const lockPath = path.join(runtimeDir, 'runtime.lock');
    await writeFile(lockPath, contents);
    try {
      await assert.rejects(startDeliveryRuntime({ runtimeDir, port: await availablePort(), timeoutMs: 1000 }), /Runtime already active or not cleanly stopped/);
      assert.equal(await readFile(lockPath, 'utf8'), contents);
    } finally {
      await removeIsolatedDirectory(runtimeDir);
    }
  }
});

async function startDeadOwner(runtimeDir, options) {
  const owner = await startOwner(runtimeDir, options);
  taskkill(owner.child.pid);
  await waitForChild(owner.child);
  await stopOwnedProcesses(owner);
  assert.equal(await readFile(path.join(runtimeDir, 'runtime.lock'), 'utf8'), owner.lock);
  return owner;
}

async function captureStart(runtimeDir, timeoutMs = 1000) {
  let runtime, rejection;
  try { runtime = await startDeliveryRuntime({ runtimeDir, port: await availablePort(), timeoutMs }); }
  catch (error) { rejection = error; }
  return { runtime, rejection };
}

async function writeOwnerMetadata(runtimeDir, owner) {
  owner.lock = JSON.stringify({ runtimeId: owner.metadata.runtimeId, pid: owner.metadata.pid, startedAt: owner.metadata.startedAt });
  await writeFile(path.join(runtimeDir, 'runtime.lock'), owner.lock);
  await writeFile(path.join(runtimeDir, 'logs', owner.metadata.runtimeId, 'runtime.json'), JSON.stringify(owner.metadata, null, 2));
}

function managedSurvivor() {
  return spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], { windowsHide: true, stdio: 'ignore' });
}

function wslProcessAlive(pid) {
  const result = spawnSync('wsl.exe', ['-d', distribution, '--', 'test', '-d', `/proc/${pid}`], { stdio: 'ignore', timeout: 10000 });
  if (result.error) throw result.error;
  return result.status === 0;
}

test('records and enforces the parser launcher with a research runtime', { timeout: 300000 }, async () => {
  const runtimeDir = path.join(testRoot, `parser-launcher-${randomUUID()}`);
  let owner, attempt, survivor;
  try {
    owner = await startDeadOwner(runtimeDir, { research: true });
    assert.equal(Number.isSafeInteger(owner.metadata.parserPid), true);
    assert.equal(owner.metadata.parserLauncherPid, owner.ready.parserLauncherPid);
    assert.equal(Number.isSafeInteger(owner.metadata.parserLauncherPid), true);

    survivor = managedSurvivor();
    owner.metadata.parserLauncherPid = survivor.pid;
    await writeOwnerMetadata(runtimeDir, owner);
    attempt = await captureStart(runtimeDir);
    assert.match(attempt.rejection?.message || '', /Runtime already active or not cleanly stopped/);
    assert.equal(attempt.runtime, undefined);
    assert.equal(await readFile(path.join(runtimeDir, 'runtime.lock'), 'utf8'), owner.lock);
    assert.equal(windowsProcessAlive(survivor.pid), true);
  } finally {
    if (attempt?.runtime) await attempt.runtime.stop().catch(() => {});
    if (survivor) taskkill(survivor.pid, true);
    await stopOwnedProcesses(owner);
    await removeIsolatedDirectory(runtimeDir);
  }
});

test('recovers despite an inert leftover recovery guard without deleting it', { timeout: 300000 }, async () => {
  const runtimeDir = path.join(testRoot, `leftover-guard-${randomUUID()}`);
  const guardPath = path.join(runtimeDir, 'runtime.lock.recovery');
  const guard = JSON.stringify({ pid: 99999999, runtimeId: randomUUID(), startedAt: new Date().toISOString() });
  let owner, attempt;
  try {
    owner = await startDeadOwner(runtimeDir);
    await writeFile(guardPath, guard);
    attempt = await captureStart(runtimeDir, 120000);
    assert.equal(attempt.rejection, undefined);
    assert.notEqual(attempt.runtime, undefined);
    assert.equal(await readFile(guardPath, 'utf8'), guard);
    await attempt.runtime.stop();
    attempt.runtime = null;
  } finally {
    if (attempt?.runtime) await attempt.runtime.stop().catch(() => {});
    await stopOwnedProcesses(owner);
    await removeIsolatedDirectory(runtimeDir);
  }
});

test('fails closed and preserves the lock when a WSL probe exceeds the startup budget', { timeout: 300000 }, async () => {
  const runtimeDir = path.join(testRoot, `probe-timeout-${randomUUID()}`);
  let owner, attempt;
  try {
    owner = await startDeadOwner(runtimeDir);
    attempt = await captureStart(runtimeDir, 1);
    assert.match(attempt.rejection?.message || '', /Runtime already active or not cleanly stopped/);
    assert.equal(attempt.runtime, undefined);
    assert.equal(await readFile(path.join(runtimeDir, 'runtime.lock'), 'utf8'), owner.lock);
  } finally {
    if (attempt?.runtime) await attempt.runtime.stop().catch(() => {});
    await stopOwnedProcesses(owner);
    await removeIsolatedDirectory(runtimeDir);
  }
});

test('rejects a surviving WSL group member after its leader exits', { timeout: 300000 }, async () => {
  const runtimeDir = path.join(testRoot, `wsl-group-member-${randomUUID()}`);
  let owner, attempt, launcher;
  try {
    owner = await startDeadOwner(runtimeDir);
    const pidFile = path.join(runtimeDir, 'logs', owner.metadata.runtimeId, 'go.pid');
    const previousGoPid = owner.goPid;
    const command = String.raw`ps -o pgid= -p \$\$ | tr -d ' ' > ${shellQuote(wslPath(pidFile))}; /bin/sleep 300 </dev/null >/dev/null 2>&1 & wait`;
    launcher = spawn('wsl.exe', ['-d', distribution, '--', 'setsid', '--wait', 'sh', '-c', command], { windowsHide: true, stdio: 'ignore' });
    let currentGoPid;
    for (let index = 0; index < 100 && !currentGoPid; index++) {
      const candidate = Number(await readFile(pidFile, 'utf8').catch(() => '')) || null;
      if (candidate && candidate !== previousGoPid) currentGoPid = candidate;
      if (!currentGoPid) await sleep(100);
    }
    owner.goPid = currentGoPid;
    assert.equal(Number.isSafeInteger(owner.goPid), true);
    spawnSync('wsl.exe', ['-d', distribution, '--', '/bin/kill', '-KILL', '--', String(owner.goPid)], { stdio: 'ignore', timeout: 10000 });
    await waitForChild(launcher);
    assert.notEqual(launcher.exitCode, null);
    assert.equal(wslProcessAlive(owner.goPid), false);
    assert.equal(await wslGroupAlive(owner.goPid), true);

    attempt = await captureStart(runtimeDir);
    assert.match(attempt.rejection?.message || '', /Runtime already active or not cleanly stopped/);
    assert.equal(attempt.runtime, undefined);
    assert.equal(await readFile(path.join(runtimeDir, 'runtime.lock'), 'utf8'), owner.lock);
    assert.equal(await wslGroupAlive(owner.goPid), true);
  } finally {
    if (attempt?.runtime) await attempt.runtime.stop().catch(() => {});
    await stopOwnedProcesses(owner);
    await removeIsolatedDirectory(runtimeDir);
  }
});

test('rejects a live reused owner PID even when the original runtime is dead', { timeout: 300000 }, async () => {
  const runtimeDir = path.join(testRoot, `pid-reuse-${randomUUID()}`);
  let owner, attempt, survivor;
  try {
    owner = await startDeadOwner(runtimeDir);
    survivor = managedSurvivor();
    owner.metadata.pid = survivor.pid;
    await writeOwnerMetadata(runtimeDir, owner);
    attempt = await captureStart(runtimeDir);
    assert.match(attempt.rejection?.message || '', /Runtime already active or not cleanly stopped/);
    assert.equal(attempt.runtime, undefined);
    assert.equal(await readFile(path.join(runtimeDir, 'runtime.lock'), 'utf8'), owner.lock);
    assert.equal(windowsProcessAlive(survivor.pid), true);
  } finally {
    if (attempt?.runtime) await attempt.runtime.stop().catch(() => {});
    if (survivor) taskkill(survivor.pid, true);
    await stopOwnedProcesses(owner);
    await removeIsolatedDirectory(runtimeDir);
  }
});

test('allows only one concurrent recovery to replace a stale lock', { timeout: 300000 }, async () => {
  const runtimeDir = path.join(testRoot, `concurrent-recovery-${randomUUID()}`);
  let owner;
  const runtimes = [];
  try {
    owner = await startDeadOwner(runtimeDir);
    const results = await Promise.allSettled([
      startDeliveryRuntime({ runtimeDir, port: await availablePort(), timeoutMs: 240000 }),
      startDeliveryRuntime({ runtimeDir, port: await availablePort(), timeoutMs: 240000 }),
    ]);
    for (const result of results) if (result.status === 'fulfilled') runtimes.push(result.value);
    assert.equal(runtimes.length, 1);
    const rejected = results.filter(result => result.status === 'rejected');
    assert.equal(rejected.length, 1);
    assert.match(rejected[0].reason.message, /Runtime already active or not cleanly stopped/);
    const response = await fetch(runtimes[0].origin, { signal: AbortSignal.timeout(5000) });
    assert.equal(response.status, 200);
    await response.arrayBuffer();
    await runtimes[0].stop();
    runtimes.length = 0;
  } finally {
    for (const runtime of runtimes) await runtime.stop().catch(() => {});
    await stopOwnedProcesses(owner);
    await removeIsolatedDirectory(runtimeDir);
  }
});

test('rejects noncanonical identity and mismatched timestamp metadata', { timeout: 300000 }, async () => {
  const baseDir = path.join(testRoot, `identity-base-${randomUUID()}`);
  let owner;
  try {
    owner = await startDeadOwner(baseDir);
    for (const name of ['runtime-id', 'pid-string', 'started-at', 'go-pid-number']) {
      const runtimeDir = path.join(testRoot, `identity-${name}-${randomUUID()}`);
      await cp(baseDir, runtimeDir, { recursive: true });
      let attempt;
      try {
        const lock = JSON.parse(owner.lock);
        const metadata = structuredClone(owner.metadata);
        metadata.directory = runtimeDir;
        let runtimeId = metadata.runtimeId;
        if (name === 'runtime-id') {
          runtimeId = 'aaaaaaa-aaaaa-aaaa-aaaa-aaaaaaaaaaaa';
          lock.runtimeId = runtimeId;
          metadata.runtimeId = runtimeId;
          await rename(path.join(runtimeDir, 'logs', owner.metadata.runtimeId), path.join(runtimeDir, 'logs', runtimeId));
        } else if (name === 'pid-string') {
          lock.pid = String(lock.pid);
          metadata.pid = String(metadata.pid);
        } else if (name === 'started-at') {
          lock.startedAt = new Date(Date.parse(lock.startedAt) + 1000).toISOString();
        }
        await writeFile(path.join(runtimeDir, 'runtime.lock'), JSON.stringify(lock));
        await writeFile(path.join(runtimeDir, 'logs', runtimeId, 'runtime.json'), JSON.stringify(metadata, null, 2));
        if (name === 'go-pid-number') await writeFile(path.join(runtimeDir, 'logs', runtimeId, 'go.pid'), '1e3');
        const before = await readFile(path.join(runtimeDir, 'runtime.lock'), 'utf8');
        attempt = await captureStart(runtimeDir, 5000);
        assert.match(attempt.rejection?.message || '', /Runtime already active or not cleanly stopped/);
        assert.equal(attempt.runtime, undefined);
        assert.equal(await readFile(path.join(runtimeDir, 'runtime.lock'), 'utf8'), before);
      } finally {
        if (attempt?.runtime) await attempt.runtime.stop().catch(() => {});
        await removeIsolatedDirectory(runtimeDir);
      }
    }
  } finally {
    await stopOwnedProcesses(owner);
    await removeIsolatedDirectory(baseDir);
  }
});
