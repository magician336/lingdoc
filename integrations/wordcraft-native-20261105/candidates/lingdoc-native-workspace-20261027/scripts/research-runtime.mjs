// Local-only evidence slice. Reuses the first-version process and data lifecycle.
import path from 'node:path';
import { startDeliveryRuntime } from './delivery-runtime.mjs';
import { checkEnvironment, printEnvironment } from './check-environment.mjs';
import { root } from './local-environment.mjs';

const options = { research: true, runtimeDir: path.join(root, 'runtime', 'research-local') };
let runtime;
const controller = new AbortController();
const shutdown = () => { controller.abort(); runtime?.stop().catch(error => { console.error(error.message); process.exitCode = 1; }); };
try {
  const names = { '--runtime-dir': 'runtimeDir', '--port': 'port', '--go-port': 'goPort', '--timeout-ms': 'timeoutMs' };
  const args = process.argv.slice(2);
  for (let i = 0; i < args.length; i++) {
    const key = names[args[i]], value = args[++i];
    if (!key || !value || value.startsWith('--')) throw new Error('Usage: research-runtime.mjs [--runtime-dir PATH] [--port PORT] [--go-port PORT] [--timeout-ms MS]');
    options[key] = key === 'runtimeDir' ? value : Number(value);
  }
  const environment = await checkEnvironment({ profile: 'run', runtimeDir: options.runtimeDir, signal: controller.signal });
  printEnvironment(environment);
  if (!environment.ok) throw new Error('环境检查未通过，请按已有路径修复；没有启动研究服务。');
  process.on('SIGINT', shutdown); process.on('SIGTERM', shutdown);
  runtime = await startDeliveryRuntime({ ...options, signal: controller.signal });
  console.log(JSON.stringify({ event: 'research-runtime-ready', ...runtime, stop: undefined, finished: undefined }));
  console.log('LingDoc 资料与引用试用：' + runtime.origin + '/');
  try { await runtime.finished; }
  finally { await runtime.stop(); }
} catch (error) {
  console.error(error.stack || error.message);
  process.exitCode = 1;
} finally {
  process.off('SIGINT', shutdown); process.off('SIGTERM', shutdown);
}
