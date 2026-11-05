// Own only this Python transport process; parsing stays in pinned upstream classes.
import { spawn, spawnSync } from 'node:child_process';
import { createWriteStream } from 'node:fs';
import { mkdir, readFile } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import path from 'node:path';
import { root, goRoot, distribution, shellQuote, wslPath } from './local-environment.mjs';

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
export async function startDocumentReader({ directory, signal }) {
  const temp = path.join(directory, 'parser-tmp');
  await mkdir(temp, { recursive: true });
  const output = createWriteStream(path.join(directory, 'docreader.log'), { flags: 'a' });
  const python = process.env.LINGDOC_DOCREADER_PYTHON || wslPath(path.join(root, 'build', 'docreader-venv', 'bin', 'python'));
  if (!python.startsWith('/')) throw new Error('LINGDOC_DOCREADER_PYTHON must be an absolute WSL path');
  const runId = randomUUID();
  const pidFile = path.join(directory, 'docreader.pid');
  const command = 'cd ' + shellQuote(wslPath(goRoot)) + ' && exec env PYTHONDONTWRITEBYTECODE=1 TMPDIR=' + shellQuote(wslPath(temp))
    + ' LINGDOC_READER_RUN_ID=' + shellQuote(runId) + ' LINGDOC_READER_PID_FILE=' + shellQuote(wslPath(pidFile))
    + ' ' + shellQuote(python) + ' -X utf8 -u -m docreader.local_server';
  const child = spawn('wsl.exe', ['-d', distribution, '--', 'setsid', '--wait', 'sh', '-c', command], {
    cwd: goRoot, windowsHide: true, stdio: ['pipe', 'pipe', 'pipe'],
    env: process.env,
  });
  let startupError, ended = false, tail = '', stdoutPending = '', info;
  const done = new Promise(resolve => {
    child.once('error', error => { startupError = error; });
    child.once('close', (code, termination) => { ended = true; output.end(); resolve({ code, termination }); });
  });
  child.stdout.on('data', data => {
    tail = (tail + data.toString()).slice(-16000);
    output.write(data);
    stdoutPending += data.toString();
    const lines = stdoutPending.split(/\r?\n/);
    stdoutPending = lines.pop();
    for (const line of lines) {
      try { const row = JSON.parse(line); if (row.ready && row.runId === runId && Number.isInteger(row.pid) && row.pid > 1 && row.port > 0 && row.port < 65536) info = row; } catch {}
    }
  });
  child.stderr.on('data', data => { output.write(data); tail = (tail + data.toString()).slice(-16000); });
  output.on('error', error => { startupError = error; });
  child.stdin.on('error', () => {});
  let stopping;
  const stop = () => stopping ??= (async () => {
    if (!ended) child.stdin.end('stop\n');
    await Promise.race([done, sleep(4000)]);
    const owner = JSON.parse(await readFile(pidFile, 'utf8').catch(() => '{}'));
    const pid = String(owner.pid ?? '');
    const validPid = owner.runId === runId && /^\d+$/.test(pid) && Number(pid) > 1;
    const groupSignal = signal => spawnSync('wsl.exe', ['-d', distribution, '--', '/bin/kill', '-' + signal, '--', '-' + pid], { windowsHide: true, stdio: 'ignore', timeout: 5000 });
    if (validPid && groupSignal('0').status === 0) groupSignal('KILL');
    if (!ended && child.pid) spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { windowsHide: true, stdio: 'ignore', timeout: 5000 });
    await Promise.race([done, sleep(2000)]);
    const probe = validPid ? groupSignal('0') : null;
    if (!ended || (!validPid && !startupError) || probe?.status === 0 || probe?.error) throw new Error('Owned document parser did not exit or could not be verified; retain its runtime directory');
  })();
  try {
    for (let i = 0; i < 300 && !info; i++) {
      signal?.throwIfAborted();
      if (startupError) throw startupError;
      if (ended) throw new Error('Existing Python document reader could not start: ' + tail);
      await sleep(100);
    }
    if (!info) throw new Error('Python document reader startup timed out: ' + tail);
    return { address: '127.0.0.1:' + info.port, pid: info.pid, launcherPid: child.pid, upstream: info.upstream, done, stop };
  } catch (error) { await stop(); throw error; }
}
