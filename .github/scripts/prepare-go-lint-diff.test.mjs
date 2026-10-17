import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import test from 'node:test'
import { lintMergeBase } from './prepare-go-lint-diff.mjs'

function fixture() {
 const cwd = mkdtempSync(path.join(tmpdir(), 'lingdoc-lint-diff-'))
 const git = (...args) => execFileSync('git', ['-c', 'commit.gpgsign=false', ...args], { cwd, encoding:'utf8', stdio:['ignore','pipe','pipe'] }).trim()
 git('init');git('config','user.name','Synthetic lint test');git('config','user.email','lint-test@example.invalid')
 writeFileSync(path.join(cwd,'base.txt'),'base\n');git('add','.');git('commit','-m','base')
 return { cwd, git, base:git('rev-parse','HEAD'), close:()=>rmSync(cwd,{recursive:true,force:true}) }
}

test('more than 300 changed files still resolve a local immutable PR baseline',()=>{
 const f=fixture()
 try {
  for(let i=0;i<301;i++)writeFileSync(path.join(f.cwd,`addition-${i}.txt`),'synthetic\n')
  f.git('add','.');f.git('commit','-m','301 additions')
  assert.equal(lintMergeBase(f.base,f.git('rev-parse','HEAD'),f.cwd),f.base)
  assert.equal(f.git('diff','--name-only',f.base,'HEAD').split('\n').length,301)
 }finally{f.close()}
})

test('diverged base resolves the common ancestor, not unrelated base branch changes',()=>{
 const f=fixture()
 try {
  f.git('checkout','-b','feature');writeFileSync(path.join(f.cwd,'feature.txt'),'feature\n');f.git('add','.');f.git('commit','-m','feature');const head=f.git('rev-parse','HEAD')
  f.git('checkout','--detach',f.base);writeFileSync(path.join(f.cwd,'base-next.txt'),'other\n');f.git('add','.');f.git('commit','-m','other branch');const baseNext=f.git('rev-parse','HEAD')
  assert.equal(lintMergeBase(baseNext,head,f.cwd),f.base)
 }finally{f.close()}
})

test('missing commit or untrusted revision text fails rather than silently skipping lint',()=>{
 const f=fixture()
 try {
  assert.throws(()=>lintMergeBase('f'.repeat(40),f.base,f.cwd))
  for(const invalid of ['', 'main', '--all', `${f.base};echo injected`, undefined])assert.throws(()=>lintMergeBase(invalid,f.base,f.cwd),/Invalid base commit SHA/)
 }finally{f.close()}
})
