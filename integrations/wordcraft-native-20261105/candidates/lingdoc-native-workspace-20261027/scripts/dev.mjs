import path from 'node:path';
import { existsSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { startDeliveryRuntime } from './delivery-runtime.mjs';
import { distribution } from './local-environment.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const args = process.argv.slice(2);
let host = '127.0.0.1';
let port = 7102;

function optionValue(index, name) {
  const argument = args[index];
  if (argument === name) {
    const value = args[index + 1];
    if (!value || value.startsWith('--')) throw new Error(`${name} requires a value`);
    return { value, next: index + 1 };
  }
  if (argument.startsWith(`${name}=`)) return { value: argument.slice(name.length + 1), next: index };
  return null;
}

for (let index = 0; index < args.length; index++) {
  if (args[index] === '--') continue;
  const hostOption = optionValue(index, '--host');
  if (hostOption) {
    host = hostOption.value;
    index = hostOption.next;
    continue;
  }
  const portOption = optionValue(index, '--port');
  if (portOption) {
    if (!/^\d+$/.test(portOption.value)) throw new Error('--port must be an integer');
    port = Number(portOption.value);
    index = portOption.next;
    continue;
  }
  throw new Error(`unknown option: ${args[index]}`);
}

// Preview launchers commonly pass wildcard hosts. Accept those aliases, but keep
// both services on their intentional same-origin loopback bindings (never expose LAN).
if (!['127.0.0.1', 'localhost', '0.0.0.0', '::', '[::]'].includes(host)) {
  throw new Error(`--host ${host} is unsupported by this loopback-only preview`);
}
if (!Number.isInteger(port) || port < 1024 || port > 65535) throw new Error('invalid port');

const localAIConfig = path.join(root, 'runtime', 'ai-private', 'local-ai.json');
// This local preview uses the delivered executable. Check before taking a lock;
// a missing /tmp artifact must not silently turn preview into a Go build.
const goBinary = process.env.LINGDOC_RESEARCH_GO_BINARY || '/tmp/lingdoc-local-source-ai';
if (!goBinary.startsWith('/')) throw new Error('LINGDOC_RESEARCH_GO_BINARY must be an absolute WSL path');
try {
  execFileSync('wsl.exe', ['-d', distribution, '--', 'test', '-x', goBinary], {
    timeout: 15000, stdio: 'pipe', windowsHide: true,
  });
} catch {
  throw new Error(`Preview Go executable unavailable: ${goBinary} in ${distribution}. Restore the delivered executable or set LINGDOC_RESEARCH_GO_BINARY to an executable absolute WSL path. No runtime lock acquired.`);
}
process.env.LINGDOC_RESEARCH_GO_BINARY = goBinary;
if (!process.env.LINGDOC_LOCAL_AI_CONFIG && existsSync(localAIConfig)) {
  process.env.LINGDOC_LOCAL_AI_CONFIG = localAIConfig;
}
const runtime = await startDeliveryRuntime({ runtimeDir: path.join(root, 'runtime', 'workspace-preview', String(port)), port, research: true });
console.log(JSON.stringify({
  ready: true,
  pid: process.pid,
  origin: runtime.origin,
  goOrigin: runtime.goOrigin,
  runtimeId: runtime.runtimeId,
  goBinary,
  bind: '127.0.0.1',
  portIsolation: { reservedPortsRefused: [5255] },
}));

let stopping = false;
const stop = async () => {
  if (stopping) return;
  stopping = true;
  await runtime.stop();
};
for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => {
    stop().then(() => { process.exitCode = 0; }, error => { console.error(error); process.exitCode = 1; });
  });
}

try {
  await runtime.finished;
} catch (error) {
  console.error(error);
  process.exitCode = 1;
} finally {
  await stop();
}
