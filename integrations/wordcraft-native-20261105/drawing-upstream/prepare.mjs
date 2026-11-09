import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { access, mkdir, readFile, cp } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { applyOverlay, patchFiles, UPSTREAM_COMMIT, UPSTREAM_REPOSITORY, sourceDirectory } from './patch.mjs';

const execute = promisify(execFile);
async function command(executable, args, cwd) {
  const result = await execute(executable, args, { cwd, windowsHide: true, maxBuffer: 8 * 1024 * 1024, timeout: 20 * 60 * 1000 });
  return result.stdout.trim();
}

export async function prepareDrawingUpstream({ directory = sourceDirectory, install = false, build = false } = {}) {
  directory = path.resolve(directory);
  let existing = false;
  try { await access(directory); existing = true; } catch (error) { if (error.code !== 'ENOENT') throw error; }
  if (!existing) {
    await mkdir(directory, { recursive: true });
    await command('git', ['init', '--quiet'], directory);
    await command('git', ['remote', 'add', 'origin', UPSTREAM_REPOSITORY], directory);
  }
  const remote = await command('git', ['remote', 'get-url', 'origin'], directory);
  if (remote !== UPSTREAM_REPOSITORY) throw new Error('Drawing cache has an unexpected upstream remote');
  let head = await command('git', ['rev-parse', '--verify', 'HEAD'], directory).catch(() => '');
  if (!head) {
    const dirty = await command('git', ['status', '--porcelain', '--untracked-files=normal'], directory);
    if (dirty) throw new Error('Uninitialized drawing cache contains files; refusing to overwrite it');
    await command('git', ['fetch', '--depth=1', 'origin', UPSTREAM_COMMIT], directory);
    await command('git', ['checkout', '--detach', UPSTREAM_COMMIT], directory);
    head = await command('git', ['rev-parse', 'HEAD'], directory);
  }
  if (head !== UPSTREAM_COMMIT) throw new Error('Drawing cache is at a different commit; use a fresh directory');
  const changed = (await command('git', ['diff', '--name-only', 'HEAD'], directory)).split(/\r?\n/).filter(Boolean);
  if (changed.some(file => !patchFiles.includes(file))) throw new Error('Drawing cache has unrelated source changes; use a fresh directory');
  await applyOverlay(directory);
  if (install || build) {
    // npm ci uses the pinned upstream lockfile. Never resolve a new lockfile.
    await command(process.execPath, [path.join(path.dirname(process.execPath), 'node_modules/npm/bin/npm-cli.js'), 'ci', '--ignore-scripts', '--no-audit', '--no-fund'], directory);
  }
  if (build) {
    const npm = path.join(path.dirname(process.execPath), 'node_modules/npm/bin/npm-cli.js');
    await command(process.execPath, [npm, 'run', 'build'], directory);
    const standalone = path.join(directory, '.next', 'standalone');
    await cp(path.join(directory, 'public'), path.join(standalone, 'public'), { recursive: true });
    await cp(path.join(directory, '.next', 'static'), path.join(standalone, '.next', 'static'), { recursive: true });
    await readFile(path.join(standalone, 'server.js'));
  }
  return { directory, commit: UPSTREAM_COMMIT, installed: install || build, built: build };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2);
  if (args.some(arg => !['--install', '--build'].includes(arg))) throw new Error('Usage: node prepare.mjs [--install] [--build]');
  prepareDrawingUpstream({ install: args.includes('--install'), build: args.includes('--build') })
    .then(result => console.log(JSON.stringify(result)))
    .catch(() => { console.error('Drawing preparation failed. Verify access to the pinned GitHub source, npm lockfile dependencies, draw.io release and existing cache.'); process.exitCode = 1; });
}
