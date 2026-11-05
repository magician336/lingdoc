// Paths shared by the environment check and the actual first-version launcher.
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
export const distribution = process.env.LINGDOC_WSL_DISTRO || 'LingDoc-Beta-R29-Ubuntu2404';
export const goHome = process.env.LINGDOC_GO_HOME || '/root/lingdoc-r29/toolchains/go';
export const goEnvironment = Object.freeze({
  PATH: `${goHome}/bin:/usr/bin:/bin`,
  GOMODCACHE: process.env.LINGDOC_GO_MOD_CACHE || '/root/lingdoc-r29/cache/go-mod',
  GOCACHE: process.env.LINGDOC_GO_BUILD_CACHE || '/root/lingdoc-r29/cache/go-build',
  GOTOOLCHAIN: 'local',
  GOPROXY: 'off',
});
export const goRoot = path.resolve(root, '..', 'updf-workspace-r1-20261026', 'go-src');
export const runtimeDirectory = path.join(root, 'runtime', 'first-version');
export const hostDirectory = path.join(root, 'ds', 'r11-bridge-combination-20261025', 'host');
export const edge = 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe';
export const fontDirectory = 'C:/Windows/Fonts'; // Fixed by the existing DS host.
export const fixture = path.join(root, 'ds', 'inputs', 'nssfc2026-official-template.docx');

export const shellQuote = value => `'${String(value).replaceAll("'", "'\"'\"'")}'`;
export function wslPath(value) {
  const match = /^([A-Za-z]):[\\/](.*)$/.exec(value);
  if (!match) throw new Error(`Expected a Windows drive path: ${value}`);
  return `/mnt/${match[1].toLowerCase()}/${match[2].replaceAll('\\', '/')}`;
}
