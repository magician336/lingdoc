import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'
import { createRequire } from 'node:module'
const require = createRequire(import.meta.url)
const ts = require('typescript')
const source = fs.readFileSync(new URL('./dependencyCandidates.ts', import.meta.url), 'utf8')

function harness() {
  const calls = [], exports = {}
  const transport = Object.fromEntries(['get', 'post'].map(method => [method, async (url, input, options) => { calls.push({ method, url, input, options }); return { data: null, request_id: 'synthetic.test', meta: { replayed: false, refresh_required: false } } }]))
  const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  vm.runInNewContext(compiled, { exports, require(name) { assert.equal(name, '@/utils/request'); return transport } })
  return { api: exports, calls }
}

test('the candidate endpoints keep the project and the candidate in single encoded segments', async () => {
  const { api, calls } = harness()
  await api.listDependencyCandidateBatches('project/other')
  assert.equal(calls[0].method, 'get')
  assert.equal(calls[0].url, '/api/v1/lingdoc/projects/project%2Fother/dependency-candidates')

  await api.listDependencyCandidateBatches('p', 'batch/1')
  assert.equal(calls[1].url, '/api/v1/lingdoc/projects/p/dependency-candidates?batch_id=batch%2F1')

  await api.decideDependencyCandidate('p', 'candidate/1', { expected_version: 1, decision: 'confirm', reason: '人工确认' }, 'candidate-decide-001')
  assert.equal(calls[2].method, 'post')
  assert.equal(calls[2].url, '/api/v1/lingdoc/projects/p/dependency-candidates/candidate%2F1/decision')
  assert.equal(calls[2].options.headers['Idempotency-Key'], 'candidate-decide-001')
})

test('a submitted batch carries the idempotency key of the proposal run', async () => {
  const { api, calls } = harness()
  await api.submitDependencyCandidateBatch('p', {
    expected_context_revision: 1,
    idempotency_key: 'batch-key-1',
    reason: '研究条件、证据卡和第 2-4 章',
    scope: { objects: [{ kind: 'ChapterVersion', id: 'c1', version: 'v1' }] },
    candidates: [{ source: { kind: 'ChapterVersion', id: 'c1', version: 'v1' }, target: { kind: 'ChapterVersion', id: 'c2', version: 'v2' }, relation: 'supports', action: 'REVALIDATE_EVIDENCE', evidence: '第 2 章引用了证据卡 E12', reason: '章节结论依赖该证据', confidence: 0.6 }],
  }, 'candidate-batch-001')
  assert.equal(calls[0].method, 'post')
  assert.equal(calls[0].url, '/api/v1/lingdoc/projects/p/dependency-candidates')
  assert.equal(calls[0].options.headers['Idempotency-Key'], 'candidate-batch-001')
  assert.equal(calls[0].input.idempotency_key, 'batch-key-1')
  assert.equal(calls[0].input.candidates[0].evidence, '第 2 章引用了证据卡 E12')
})

test('a confirmation never states anything the server decides', async () => {
  const { api, calls } = harness()
  await api.decideDependencyCandidate('p', 'c1', { expected_version: 2, decision: 'confirm', reason: '人工确认' }, 'candidate-decide-002')
  const body = calls[0].input
  assert.equal(body.expected_version, 2)
  assert.equal(body.decision, 'confirm')
  assert.equal('status' in body, false)
  assert.equal('edge_id' in body, false)
})
