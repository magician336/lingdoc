import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'
import { createRequire } from 'node:module'
const require = createRequire(import.meta.url)
const ts = require('typescript')
const source = fs.readFileSync(new URL('./roadmap.ts', import.meta.url), 'utf8')

function harness() {
  const calls = [], exports = {}
  const transport = Object.fromEntries(['get', 'post'].map(method => [method, async (url, input, options) => { calls.push({ method, url, input, options }); return { data: null, request_id: 'synthetic.test', meta: { replayed: false, refresh_required: false } } }]))
  const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  vm.runInNewContext(compiled, { exports, require(name) { assert.equal(name, '@/utils/request'); return transport } })
  return { api: exports, calls }
}

test('the editing session endpoints keep the project and session in single encoded segments', async () => {
  const { api, calls } = harness()
  await api.beginRoadmapEdit('project/other', { expected_context_revision: 2, reason: '编辑路线图' }, 'roadmap-begin-001')
  assert.equal(calls[0].method, 'post')
  assert.equal(calls[0].url, '/api/v1/lingdoc/projects/project%2Fother/roadmap/edits')
  assert.equal(calls[0].options.headers['Idempotency-Key'], 'roadmap-begin-001')

  await api.proposeRoadmapNode('p', 'session/1', { node_id: 'r1', kind: 'update', name: '改写', status: 'ready', reason: '改写' }, 'roadmap-propose-001')
  assert.equal(calls[1].url, '/api/v1/lingdoc/projects/p/roadmap/edits/session%2F1/nodes')

  await api.commitRoadmapEdit('p', 's1', { expected_baseline_version: 1, reason: '提交' }, 'roadmap-commit-001')
  assert.equal(calls[2].url, '/api/v1/lingdoc/projects/p/roadmap/edits/s1/commit')

  await api.cancelRoadmapEdit('p', 's1', { expected_baseline_version: 1, reason: '取消' }, 'roadmap-cancel-001')
  assert.equal(calls[3].url, '/api/v1/lingdoc/projects/p/roadmap/edits/s1/cancel')
})

test('a node proposal never states the required flag to the server', async () => {
  const { api, calls } = harness()
  await api.proposeRoadmapNode('p', 's1', { node_id: 'r1', kind: 'update', name: '改写', status: 'ready', reason: '改写' }, 'roadmap-propose-002')
  assert.equal(calls[0].input.required, undefined)
  assert.equal('required' in calls[0].input, false)
})

test('the version comparison asks the server for both versions', async () => {
  const { api, calls } = harness()
  await api.compareRoadmapNodeVersions('p', 'r1', 1, 2)
  assert.equal(calls[0].method, 'get')
  assert.equal(calls[0].url, '/api/v1/lingdoc/projects/p/roadmap/nodes/r1/compare?from=1&to=2')
})
