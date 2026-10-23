import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const ts = require('typescript') as typeof import('typescript')
const source = fs.readFileSync(new URL('./trialWindow.ts', import.meta.url), 'utf8')

interface RecordedCall {
  method: string
  url: string
  input?: Record<string, any>
  options?: { headers?: Record<string, string> }
}

// The client cannot be imported directly here: it pulls in the aliased
// request helper, which this test runtime cannot resolve.  So the module is
// transpiled and handed a recording transport instead -- the same seam the
// change-set client test uses.
function harness(result?: unknown) {
  const calls: RecordedCall[] = []
  const exports: Record<string, (...args: any[]) => any> = {}
  const record = (method: string) => async (url: string, input?: Record<string, any>, options?: RecordedCall['options']) => {
    calls.push({ method, url, input, options })
    return result ?? { data: null, request_id: 'synthetic.test', meta: { replayed: false, refresh_required: false } }
  }
  const transport = { get: record('get'), post: record('post'), put: record('put') }
  const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  vm.runInNewContext(compiled, { exports, require(name: string) { assert.equal(name, '@/utils/request'); return transport } })
  return { api: exports, calls }
}

// The server publishes no OpenAPI path for the trial routes, so these calls
// are the only machine-checked record of where they live.  A path or segment
// that drifts here fails before a panel asks for a resource the server never
// serves.
test('every trial endpoint keeps its exact path and encoding', async () => {
  const { api, calls } = harness()
  const project = 'project/other'
  const projectPath = '/api/v1/lingdoc/projects/project%2Fother'
  await api.listTrialWindows(project)
  await api.createTrialWindow(project, { starts_at: 'synthetic.start', ends_at: 'synthetic.end' }, 'key-window')
  await api.actOnTrialWindow(project, 'window/one', { expected_version: 1, action: 'lock', reason: 'synthetic.lock' }, 'key-window-action')
  await api.listTrialSamples(project, 'window/one')
  await api.recordTrialSample(project, 'window/one', { sample_version: 'synthetic.v1' }, 'key-sample')
  await api.reviewTrialSample(project, 'window/one', 'sample/one', { expected_version: 1, quality_result: 'PASS', human_decision: 'accept', reason: 'synthetic.review', word_roundtrip_evidence: 'synthetic.word' }, 'key-review')
  await api.getTrialReport(project, 'window/one')
  await api.listTrialDecisions(project, 'window/one')
  await api.createTrialDecision(project, 'window/one', { decision: 'expand', reason: 'synthetic.decision', report_hash: 'synthetic.hash' }, 'key-decision')
  await api.signTrialDecision(project, 'decision/one', { expected_version: 1, responsibility: 'product', reason: 'synthetic.sign' }, 'key-sign')
  assert.deepEqual(calls.map(call => call.url), [
    `${projectPath}/trial-windows`,
    `${projectPath}/trial-windows`,
    `${projectPath}/trial-windows/window%2Fone/actions`,
    `${projectPath}/trial-windows/window%2Fone/samples`,
    `${projectPath}/trial-windows/window%2Fone/samples`,
    `${projectPath}/trial-windows/window%2Fone/samples/sample%2Fone/review`,
    `${projectPath}/trial-windows/window%2Fone/report`,
    `${projectPath}/trial-windows/window%2Fone/decisions`,
    `${projectPath}/trial-windows/window%2Fone/decisions`,
    `${projectPath}/trial-decisions/decision%2Fone/sign`,
  ])
})

test('trial writes carry an idempotency key and trial reads carry no request body', async () => {
  const { api, calls } = harness()
  await api.listTrialWindows('p')
  await api.listTrialSamples('p', 'window/one')
  await api.getTrialReport('p', 'window/one')
  await api.signTrialDecision('p', 'd', { expected_version: 1, responsibility: 'qa', reason: 'synthetic.sign' }, 'key-sign')
  const reads = calls.filter(call => call.method === 'get')
  const writes = calls.filter(call => call.method === 'post')
  assert.equal(reads.length, 3)
  for (const call of reads) assert.equal(call.input, undefined)
  assert.equal(writes.length, 1)
  for (const call of writes) assert.match(String(call.options?.headers?.['Idempotency-Key']), /^key-/)
})

test('sample recording does not invent reviewer, PASS or real execution evidence', async () => {
  const { api, calls } = harness()
  const sample = { sample_version: 'synthetic.v1', fixture_digest: 'synthetic.digest', runtime_mode: 'mock', runtime_event_ids: [], metrics: [] }
  await api.recordTrialSample('p', 'w', sample, 'sample-original-001')
  assert.equal(calls[0].input!.runtime_mode, 'mock')
  assert.equal(calls[0].input!.quality_result, undefined)
  assert.equal(calls[0].input!.reviewer_id, undefined)
  assert.equal(calls[0].input, sample)
})

test('trial evidence reads reuse existing domain endpoints and encode every reference', async () => {
  const { api, calls } = harness()
  await api.listTrialResearchRuns('p/other'); await api.getTrialResearchRun('p/other', 'run?other')
  await api.listTrialGenerationCandidates('p/other', 'chapter/other'); await api.getTrialGenerationRun('p/other', 'run/other'); await api.getTrialGenerationCandidate('p/other', 'run/other')
  await api.listTrialReleases('p/other'); await api.getTrialRelease('p/other', 'snapshot/other'); await api.listTrialExports('p/other'); await api.getTrialExport('p/other', 'export/other')
  assert.equal(calls.every(call => call.method === 'get' && call.input === undefined), true)
  assert.equal(calls[1].url, '/api/v1/lingdoc/projects/p%2Fother/research-runs/run%3Fother')
  assert.equal(calls[2].url, '/api/v1/lingdoc/projects/p%2Fother/chapters/chapter%2Fother/candidates')
  assert.equal(calls[4].url, '/api/v1/lingdoc/projects/p%2Fother/generations/run%2Fother/candidate')
  assert.equal(calls[6].url, '/api/v1/lingdoc/projects/p%2Fother/releases/snapshot%2Fother')
  assert.equal(calls[8].url, '/api/v1/lingdoc/projects/p%2Fother/exports/export%2Fother')
})
