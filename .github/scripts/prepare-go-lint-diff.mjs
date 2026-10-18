import { execFileSync } from 'node:child_process'
import { appendFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'

export function lintMergeBase(base, head, cwd = process.cwd()) {
  for (const [name, value] of [['base', base], ['head', head]]) {
    if (typeof value !== 'string' || !/^[0-9a-f]{40}(?:[0-9a-f]{24})?$/.test(value)) throw new Error(`Invalid ${name} commit SHA; lint baseline unavailable`)
  }
  const git = (...args) => execFileSync('git', args, { cwd, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim()
  git('cat-file', '-e', `${base}^{commit}`)
  git('cat-file', '-e', `${head}^{commit}`)
  const mergeBase = git('merge-base', base, head)
  if (!/^[0-9a-f]{40}(?:[0-9a-f]{24})?$/.test(mergeBase)) throw new Error('No usable PR merge base; do not skip or expand lint')
  return mergeBase
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const base = lintMergeBase(process.env.PR_BASE_SHA, process.env.PR_HEAD_SHA)
  if (!process.env.GITHUB_OUTPUT) throw new Error('GITHUB_OUTPUT missing; no lint arguments emitted')
  appendFileSync(process.env.GITHUB_OUTPUT, `base_sha=${base}\n`)
  console.log(`Linting PR additions relative to local merge base ${base}; no GitHub diff API request`)
}
