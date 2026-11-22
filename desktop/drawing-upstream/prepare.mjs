import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { access, mkdir, readFile, cp, rename, rm } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { applyOverlay, patchFiles, UPSTREAM_COMMIT, UPSTREAM_REPOSITORY, sourceDirectory } from './patch.mjs';

const execute = promisify(execFile);
async function command(executable, args, cwd) {
  const result = await execute(executable, args, { cwd, env: { ...process.env, NEXT_TELEMETRY_DISABLED: '1', NEXT_PUBLIC_BASE_PATH: '' }, windowsHide: true, maxBuffer: 8 * 1024 * 1024, timeout: 20 * 60 * 1000 });
  return result.stdout.trim();
}

async function verifyStandalone(directory) {
  for (const file of ['server.js', 'package.json', path.join('.next', 'required-server-files.json')]) {
    await readFile(path.join(directory, file));
  }
}

// Development builds may freely replace source/.next. WordCraft instead uses the
// separately promoted app/ directory, so an incomplete build cannot destroy the
// last usable drawing runtime. The application keeps the legacy nested layout.
export async function promoteDrawingRuntime(directory) {
  directory = path.resolve(directory);
  const standalone = path.join(directory, '.next', 'standalone');
  await verifyStandalone(standalone);
  const runtimeRoot = path.dirname(directory);
  const application = path.join(runtimeRoot, 'app');
  const suffix = `${process.pid}-${Date.now()}`;
  const staged = path.join(runtimeRoot, `.app-staging-${suffix}`);
  const previous = path.join(runtimeRoot, `.app-previous-${suffix}`);
  await cp(standalone, path.join(staged, '.next', 'standalone'), { recursive: true });
  try {
    await verifyStandalone(path.join(staged, '.next', 'standalone'));
    let hadPrevious = false;
    try {
      await rename(application, previous);
      hadPrevious = true;
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
    try {
      await rename(staged, application);
    } catch (error) {
      if (hadPrevious) await rename(previous, application);
      throw error;
    }
    if (hadPrevious) await rm(previous, { recursive: true, force: true });
  } finally {
    await rm(staged, { recursive: true, force: true });
  }
  return application;
}

export async function prepareDrawingUpstream({ directory = sourceDirectory, install = false, build = false, promote = false } = {}) {
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
    await command('git', ['config', '--local', 'core.autocrlf', 'false'], directory);
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
    const buildArgs = ['run', 'build'];
    // Turbopack fails to spawn its pooled Node process on the verified Windows host.
    if (process.platform === 'win32') buildArgs.push('--', '--webpack');
    await command(process.execPath, [npm, ...buildArgs], directory);
    const standalone = path.join(directory, '.next', 'standalone');
    await cp(path.join(directory, 'public'), path.join(standalone, 'public'), { recursive: true });
    await cp(path.join(directory, '.next', 'static'), path.join(standalone, '.next', 'static'), { recursive: true });
    await verifyStandalone(standalone);
  }
  const applicationDirectory = promote ? await promoteDrawingRuntime(directory) : null;
  return { directory, commit: UPSTREAM_COMMIT, installed: install || build, built: build, promoted: promote, applicationDirectory };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2);
  if (args.some(arg => !['--install', '--build', '--promote'].includes(arg))) throw new Error('Usage: node prepare.mjs [--install] [--build] [--promote]');
  prepareDrawingUpstream({ install: args.includes('--install'), build: args.includes('--build'), promote: args.includes('--promote') })
    .then(result => console.log(JSON.stringify(result)))
    .catch(error => { console.error(`Drawing preparation failed: ${error?.message || error}`); process.exitCode = 1; });
}
