// No downloads, package installation, product compilation, or service startup.
import { execFile } from 'node:child_process';
import { access, readFile, stat } from 'node:fs/promises';
import { constants } from 'node:fs';
import { createHash } from 'node:crypto';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { root, distribution, goHome, goEnvironment, goRoot, runtimeDirectory,
  hostDirectory, edge, fontDirectory, fixture, shellQuote, wslPath } from './local-environment.mjs';

const execute = promisify(execFile);
const sha256 = bytes => createHash('sha256').update(bytes).digest('hex');
const command = async (executable, args, signal) => {
  try {
    const result = await execute(executable, args, { windowsHide: true, timeout: 25000, maxBuffer: 1024 * 1024, signal });
    return result.stdout.replace(/^\uFEFF/, '').trim();
  } catch (error) {
    // wsl.exe can mix a UTF-16 warning with the Linux command's UTF-8 stderr.
    // Keep the raw diagnostic in JSON, but show usable lines once in the CLI.
    const lines = [...new Set((error.stderr || '').replaceAll('\0', '').split(/\r?\n/)
      .map(line => line.trim()).filter(line => line && !line.includes('\uFFFD')))];
    const failure = new Error(`${path.basename(executable)} (${error.code ?? error.signal ?? 'error'}): ${lines.join('\n') || error.code || 'command unavailable or timed out'}`);
    failure.diagnostic = error.stderr || error.message;
    throw failure;
  }
};

export async function checkEnvironment({ profile = 'all', runtimeDir = runtimeDirectory, signal } = {}) {
  if (!['all', 'run'].includes(profile)) throw new Error(`Unknown environment profile: ${profile}`);
  signal?.throwIfAborted();
  const started = Date.now();
  const checks = [];
  const check = (id, label, hint, action) => {
    const row = { id, label, status: 'pending' };
    checks.push(row);
    return (async () => {
      try { row.detail = await action(); row.status = 'ok'; }
      catch (error) { row.status = 'fail'; row.detail = error.message; row.hint = hint; if (error.diagnostic) row.diagnostic = error.diagnostic; }
    })();
  };
  const jobs = [check('node', 'Windows / Node', '使用现有 Windows Node 22+；不要为此重装项目依赖。', async () => {
    if (process.platform !== 'win32') throw new Error('This candidate requires Windows fonts and WSL.');
    if (Number(process.versions.node.split('.')[0]) < 22) throw new Error(`Node 22+ required; found ${process.version}`);
    return `${process.version} — ${process.execPath}`;
  })];

  if (process.platform === 'win32') {
    jobs.push(check('go', 'WSL / Go / 缓存', '核对所列 WSL 发行版、Go 路径、gcc 和已有缓存；可在 local-environment.mjs 对应的 LINGDOC_* 变量指定另一套本地位置。', async () => {
      await access(path.join(goRoot, 'go.mod'));
      const requirements = [
        `export PATH=${shellQuote(goEnvironment.PATH)}`,
        `test -x ${shellQuote(`${goHome}/bin/go`)} || { echo 'Go executable missing' >&2; exit 1; }`,
        ...[goEnvironment.GOMODCACHE, goEnvironment.GOCACHE].map(directory => `test -d ${shellQuote(directory)} || { echo ${shellQuote(`Cache directory missing: ${directory}`)} >&2; exit 1; }`),
        "command -v gcc >/dev/null || { echo 'gcc missing' >&2; exit 1; }",
        "command -v setsid >/dev/null || { echo 'setsid missing' >&2; exit 1; }",
        "test -x /bin/kill || { echo '/bin/kill missing' >&2; exit 1; }",
        `test -r ${shellQuote(wslPath(path.join(goRoot, 'go.mod')))} || { echo 'WSL cannot read the Go source directory' >&2; exit 1; }`,
        `exec env ${Object.entries(goEnvironment).map(([key, value]) => `${key}=${shellQuote(value)}`).join(' ')} go version`,
      ].join('\n');
      const version = await command('wsl.exe', ['-d', distribution, '--', 'sh', '-c', `set -eu\n${requirements}`], signal);
      const expected = /^go\s+([^\s]+)/m.exec(await readFile(path.join(goRoot, 'go.mod'), 'utf8'))?.[1];
      if (!expected || !version.startsWith(`go version go${expected} `)) throw new Error(`Expected Go ${expected} from go.mod; found ${version}`);
      return { version, distribution, goHome, caches: [goEnvironment.GOMODCACHE, goEnvironment.GOCACHE], coverage: '目录和工具可用；不编译、不扫描全部依赖' };
    }));
    if (profile === 'all') {
      jobs.push(check('rust', 'Rust / WASM / MSVC', '在候选根目录执行 . ./scripts/use-rust.ps1 -Repair；优先复用本地组件。', async () => {
        const output = await command('powershell.exe', ['-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', path.join(root, 'build-s0.ps1'), '-CheckEnvironment'], signal);
        return JSON.parse(output);
      }));
      jobs.push(check('git', 'Git', '复用本机 Git，并确保 git.exe 在 PATH 中。', () => command('git', ['--version'], signal)));
      jobs.push(check('edge', '浏览器验收入口', `当前验收脚本使用 ${edge}；先核对现有 Edge 的安装位置。`, async () => {
        await access(edge);
        return { executable: edge, coverage: '文件存在；本次不启动浏览器、不进行产品验收' };
      }));
    }
  }

  jobs.push(check('artifacts', '编辑器产物 / 固定 DOCX', '复用现有 build/out 和 ds/inputs；缺产物才运行正式 build-s0.ps1，不能把空目录当成已构建。', async () => {
    const files = ['wordcraft.js', 'wordcraft_bg.wasm'].map(name => path.join(root, 'build', 'out', name));
    const artifacts = [];
    for (const file of files) {
      const bytes = await readFile(file);
      if (!bytes.length) throw new Error(`Empty artifact: ${file}`);
      if (file.endsWith('.wasm') && bytes.subarray(0, 8).toString('hex') !== '0061736d01000000') throw new Error(`Invalid WASM header: ${file}`);
      artifacts.push({ file, bytes: bytes.length });
    }
    const bytes = await readFile(fixture);
    if (sha256(bytes) !== 'e6ef31b4b284f509b478071f8be09c76efceab9f32ca8803afdf905eee136181') throw new Error(`Fixed DOCX identity mismatch: ${fixture}`);
    return { artifacts, fixture, coverage: '文件及固定模板身份；不证明 JS/WASM 与当前源码一致' };
  }));
  jobs.push(check('fonts', 'DS 固定字体', '按 host/font-manifest.json 核对 Windows 已有字体；不要修改清单绕过身份检查或另行打包字体。', async () => {
    const fonts = JSON.parse(await readFile(path.join(hostDirectory, 'font-manifest.json'), 'utf8'));
    if (!Array.isArray(fonts) || fonts.length !== 12 || !fonts.every(font => /^[a-z0-9_-]+\.(ttf|ttc)$/i.test(font.name))) throw new Error('Invalid fixed font manifest');
    const invalid = [];
    for (const font of fonts) {
      const bytes = await readFile(path.join(fontDirectory, font.name));
      if (bytes.length !== font.bytes || sha256(bytes) !== font.sha256) invalid.push(font.name);
    }
    if (invalid.length) throw new Error(`Font identity mismatch: ${invalid.join(', ')}`);
    return { directory: fontDirectory, matched: fonts.length };
  }));
  jobs.push(check('data', '持久化目录', '保留 lingdoc.db、actual-origin.txt 和 ds/；运行锁存在时先确认原实例，不删库或强占进程。', async () => {
    const directory = path.resolve(runtimeDir);
    let parent = directory;
    while (true) {
      try { await stat(parent); break; }
      catch (error) {
        if (error.code !== 'ENOENT' || path.dirname(parent) === parent) throw error;
        parent = path.dirname(parent);
      }
    }
    if (!(await stat(parent)).isDirectory()) throw new Error(`Not a directory: ${parent}`);
    await access(parent, constants.W_OK);
    const lockPath = path.join(directory, 'runtime.lock');
    const lock = await readFile(lockPath, 'utf8').catch(error => { if (error.code === 'ENOENT') return null; throw error; });
    if (lock !== null && profile === 'run') throw new Error(`Runtime already active or not cleanly stopped: ${lockPath}`);
    return { directory, database: path.join(directory, 'lingdoc.db'), dsData: path.join(directory, 'ds'), activeOrUnclean: lock !== null, coverage: '路径和访问权限；不创建数据库、不验证保存业务' };
  }));
  await Promise.all(jobs);
  signal?.throwIfAborted();
  return { observedAt: new Date().toISOString(), profile, elapsedMs: Date.now() - started,
    ok: checks.every(row => row.status === 'ok'), checks,
    scope: '环境就绪检查，不代表产品验收或完整 CI 通过；无下载、产品编译或服务启动' };
}

export function printEnvironment(report) {
  for (const row of report.checks) {
    console.log(`[${row.status === 'ok' ? '可用' : '缺项'}] ${row.label}`);
    if (row.status !== 'ok') console.log(`  ${row.detail}\n  处理：${row.hint}`);
    else if (row.id === 'rust' && row.detail.optionalMissing.length) console.log(`  可选工具待补：${row.detail.optionalMissing.join(', ')}`);
  }
  console.log(`环境检查${report.ok ? '通过' : '未通过'}，耗时 ${(report.elapsedMs / 1000).toFixed(1)} 秒。`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const options = {};
    let json = false;
    const args = process.argv.slice(2);
    for (let index = 0; index < args.length; index++) {
      if (args[index] === '--runtime') options.profile = 'run';
      else if (args[index] === '--json') json = true;
      else if (args[index] === '--runtime-dir' && args[index + 1] && !args[index + 1].startsWith('--')) options.runtimeDir = args[++index];
      else throw new Error('Usage: check-environment.mjs [--runtime] [--json] [--runtime-dir PATH]');
    }
    const report = await checkEnvironment(options);
    if (json) console.log(JSON.stringify(report, null, 2)); else printEnvironment(report);
    if (!report.ok) process.exitCode = 1;
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
