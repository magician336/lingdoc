import { spawn, execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { createServer } from 'node:net';
import { readFile, lstat, access } from 'node:fs/promises';
import path from 'node:path';
import { randomUUID } from 'node:crypto';
import { sourceDirectory } from './patch.mjs';

const execute = promisify(execFile);
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));

export function exactLoopbackOrigin(value) {
  const url = new URL(value);
  if (url.protocol !== 'http:' || !['127.0.0.1', 'localhost'].includes(url.hostname) ||
      url.origin !== value || !url.port) throw new Error('Exact loopback HTTP origin required');
  return url.origin;
}

export function modelEnvironment(config) {
  if (!config || typeof config !== 'object' || Array.isArray(config)) throw new Error('Existing LingDoc model configuration is invalid');
  const fields = Object.keys(config);
  if (fields.some(field => !['model', 'api_key', 'system_aes_key'].includes(field)) ||
      config.model !== 'deepseek-flash' || typeof config.api_key !== 'string' ||
      !config.api_key || config.api_key.length > 512 || /\s|\p{Cc}/u.test(config.api_key) ||
      config.api_key.startsWith('enc:v1:') || typeof config.system_aes_key !== 'string' ||
      Buffer.byteLength(config.system_aes_key) !== 32) throw new Error('Existing LingDoc model configuration is invalid');
  // These are child-process environment variables, never command arguments,
  // response fields, metadata, logs or browser storage.
  return {
    AI_PROVIDER: 'deepseek', AI_MODEL: config.model,
    DEEPSEEK_API_KEY: config.api_key, DEEPSEEK_BASE_URL: 'https://api.deepseek.com/v1',
    AI_MODELS_CONFIG: JSON.stringify({ providers: [{ name: 'LingDoc', provider: 'deepseek', models: [config.model], default: true }] }),
  };
}

async function loadModelEnvironment(configPath) {
  if (!configPath) return {};
  if (!path.isAbsolute(configPath)) throw new Error('Existing LingDoc model configuration requires an absolute path');
  try {
    const stat = await lstat(configPath);
    if (!stat.isFile() || stat.size <= 0 || stat.size > 16 * 1024) throw new Error();
    return modelEnvironment(JSON.parse(await readFile(configPath, 'utf8')));
  } catch { throw new Error('Existing LingDoc model configuration could not be loaded'); }
}

async function freePort() {
  const server = createServer();
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  const port = server.address().port;
  await new Promise(resolve => server.close(resolve));
  return port;
}

export async function startDrawingUpstream({ parentOrigin, configPath = process.env.LINGDOC_LOCAL_AI_CONFIG, directory } = {}) {
  exactLoopbackOrigin(parentOrigin);
  if (process.env.LINGDOC_DRAWING_ORIGIN) {
    return { origin: exactLoopbackOrigin(process.env.LINGDOC_DRAWING_ORIGIN), stop: async () => {}, external: true };
  }
  const serverFile = path.join(sourceDirectory, '.next', 'standalone', 'server.js');
  try { await access(serverFile); }
  catch { throw new Error('AI 绘图尚未构建。请运行 node integrations/wordcraft-native-20261105/drawing-upstream/prepare.mjs --build'); }
  const configured = await loadModelEnvironment(configPath);
  const port = await freePort();
  const origin = `http://127.0.0.1:${port}`;
  const runtimeId = randomUUID();
  const env = {
    ...process.env,
    AI_PROVIDER: '', AI_MODEL: '', AI_MODELS_CONFIG: '{"providers":[]}',
    AI_MODELS_CONFIG_PATH: path.join(sourceDirectory, 'runtime-models-unused.json'),
    LANGFUSE_SECRET_KEY: '', LANGFUSE_PUBLIC_KEY: '', LANGFUSE_ENABLED: 'false',
    DEBUG_LLM_PAYLOAD: 'false', NEXT_TELEMETRY_DISABLED: '1', NEXT_PUBLIC_BASE_PATH: '',
    ORIGIN_SECRET: '', DYNAMODB_QUOTA_TABLE: '',
    ...configured,
    HOSTNAME: '127.0.0.1', PORT: String(port), NODE_ENV: 'production',
    LINGDOC_DRAWING_PARENT_ORIGIN: parentOrigin, LINGDOC_DRAWING_SELF_ORIGIN: origin,
    LINGDOC_DRAWING_RUNTIME_ID: runtimeId,
  };
  // Upstream diagnostics can include diagram tool input. Keep child stdio
  // private instead of forwarding research content into launcher logs.
  const child = spawn(process.execPath, [serverFile], { cwd: sourceDirectory, env, windowsHide: true, stdio: 'ignore' });
  let ended = false, failed = false, stopping;
  const done = new Promise(resolve => { child.once('error', () => { failed = true; }); child.once('close', () => { ended = true; resolve(); }); });
  const stop = () => stopping ??= (async () => {
    if (!ended && child.pid) {
      if (process.platform === 'win32') await execute('taskkill', ['/PID', String(child.pid), '/T', '/F'], { windowsHide: true }).catch(() => {});
      else child.kill('SIGTERM');
      await Promise.race([done, delay(5000)]);
      if (!ended && process.platform !== 'win32') child.kill('SIGKILL');
      await Promise.race([done, delay(2000)]);
      if (!ended) throw new Error('Owned drawing process could not be stopped');
    }
  })();
  try {
    for (let attempt = 0; attempt < 150; attempt++) {
      if (ended || failed) throw new Error('AI 绘图服务未能启动，请核对固定版本构建产物。');
      try {
        const response = await fetch(origin + '/api/lingdoc-bridge-config', { signal: AbortSignal.timeout(1000), redirect: 'error' });
        const value = response.ok ? await response.json() : null;
        if (value?.runtimeId === runtimeId) return { origin, runtimeId, directory, aiConfigured: !!configured.AI_MODEL, stop };
      } catch {}
      await delay(200);
    }
    throw new Error('AI 绘图服务启动超时。');
  } catch (error) { await stop(); throw error; }
}
