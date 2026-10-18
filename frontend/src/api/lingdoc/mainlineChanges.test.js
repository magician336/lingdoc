import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'
import { createRequire } from 'node:module'
const require = createRequire(import.meta.url)
const ts = require('typescript')
const source = fs.readFileSync(new URL('./mainlineChanges.ts', import.meta.url), 'utf8')
function harness(result) {
  const calls = [], exports = {}
  const transport = Object.fromEntries(['get', 'post', 'put'].map(method => [method, async (url, input, options) => { calls.push({ method, url, input, options }); return result ?? { data: null, request_id: 'synthetic.test', meta: { replayed: false, refresh_required: false } } }]))
  transport.postNoBody = (url, options) => transport.post(url, undefined, options)
  const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  vm.runInNewContext(compiled, { exports, require(name) { assert.equal(name, '@/utils/request'); return transport } })
  return { api: exports, calls }
}
test('project, change and snapshot references remain encoded single resource segments', async () => {
  const { api, calls } = harness()
  await api.acknowledgeImpactPreflight('project/other', 'change?scope=other', 'snapshot-existing', 'retry-original-001')
  assert.equal(calls[0].url, '/api/v1/lingdoc/projects/project%2Fother/change-sets/change%3Fscope%3Dother/preflight')
  assert.deepEqual(Object.keys(calls[0].input), ['proof_id'])
  assert.equal(calls[0].input.proof_id, 'snapshot-existing')
  assert.equal(calls[0].options.headers['Idempotency-Key'], 'retry-original-001')
})
test('sample recording does not invent reviewer, PASS or real execution evidence', async () => {
  const { api, calls } = harness()
  const sample = { sample_version: 'synthetic.v1', fixture_digest: 'synthetic.digest', runtime_mode: 'mock', runtime_event_ids: [], metrics: [] }
  await api.recordTrialSample('p', 'w', sample, 'sample-original-001')
  assert.equal(calls[0].input, sample)
  assert.equal(calls[0].input.runtime_mode, 'mock')
  assert.equal(calls[0].input.quality_result, undefined)
  assert.equal(calls[0].input.reviewer_id, undefined)
})
test('runtime recovery sends existing artifact references and leaves validation to the server', async () => {
  const { api, calls } = harness()
  const input = { expected_version: 4, scope: { kind: 'project', value: 'p' }, action: 'resume', reason: 'synthetic.recovery', snapshot_id: 'snapshot-4', export_id: 'export-4' }
  await api.controlRuntime('p', input, 'recovery-original-001')
  assert.equal(calls[0].input, input)
  assert.equal(calls[0].input.recovery_verified, undefined)
  assert.equal(calls[0].options.headers['Idempotency-Key'], 'recovery-original-001')
})
test('task completion preserves expected version, target revision and exact closure evidence', async () => {
  const { api, calls } = harness()
  const input = { expected_version: 3, target_revision: 7, action: 'complete', reason: 'synthetic.review', closure_evidence: [{ kind: 'ChapterVersion', id: 'chapter', version: 'current-v7' }] }
  await api.actOnImpactTask('p', 'task/isolated', input, 'closure-original-001')
  assert.equal(calls[0].url, '/api/v1/lingdoc/projects/p/impact-tasks/task%2Fisolated/actions')
  assert.equal(calls[0].input, input)
  assert.equal(calls[0].options.headers['Idempotency-Key'], 'closure-original-001')
})
test('typed semantic drafts preserve explicit consent and let the server pin current before state', async () => {
 const {api,calls}=harness();const input={expected_context_revision:2,reason:'synthetic intent',fields:{},affected_chapter_ids:[],semantic_deltas:[{kind:'ResearchClaim',target_id:'claim-a',expected_version:3,proposed_claim:{text:'claim edit',scope:'limited scope',status:'disputed',confirmed:false}}]}
 await api.createChangeDraft('p',input,'semantic-retry-01');assert.equal(calls[0].input,input);assert.equal(calls[0].input.semantic_deltas[0].proposed_claim.confirmed,false);assert.equal(calls[0].input.semantic_deltas[0].before_hash,undefined);assert.equal(calls[0].options.headers['Idempotency-Key'],'semantic-retry-01')
})

test('current computation receipts are read from the exact encoded project and receipt',async()=>{
 const {api,calls}=harness();await api.getImpactComputationReceipt('project/other','receipt?foreign')
 assert.equal(calls[0].method,'get');assert.equal(calls[0].url,'/api/v1/lingdoc/projects/project%2Fother/check-receipts/receipt%3Fforeign');assert.equal(calls[0].input,undefined)
})

test('trial evidence reads reuse existing domain endpoints and encode every reference',async()=>{
 const {api,calls}=harness()
 await api.listTrialResearchRuns('p/other');await api.getTrialResearchRun('p/other','run?other')
 await api.listTrialGenerationCandidates('p/other','chapter/other');await api.getTrialGenerationRun('p/other','run/other');await api.getTrialGenerationCandidate('p/other','run/other')
 await api.listTrialReleases('p/other');await api.getTrialRelease('p/other','snapshot/other');await api.listTrialExports('p/other');await api.getTrialExport('p/other','export/other')
 assert.equal(calls.every(call=>call.method==='get'&&call.input===undefined),true)
 assert.equal(calls[1].url,'/api/v1/lingdoc/projects/p%2Fother/research-runs/run%3Fother')
 assert.equal(calls[2].url,'/api/v1/lingdoc/projects/p%2Fother/chapters/chapter%2Fother/candidates')
 assert.equal(calls[4].url,'/api/v1/lingdoc/projects/p%2Fother/generations/run%2Fother/candidate')
 assert.equal(calls[6].url,'/api/v1/lingdoc/projects/p%2Fother/releases/snapshot%2Fother')
 assert.equal(calls[8].url,'/api/v1/lingdoc/projects/p%2Fother/exports/export%2Fother')
})

test('all runtime scope kinds preserve the exact proposal without asserting permission or recovery',async()=>{
 const {api,calls}=harness()
 for(const scope of [{kind:'tenant',value:'7'},{kind:'project',value:'p'},{kind:'template_version',value:'1'},{kind:'ruleset_hash',value:'actual.hash'},{kind:'runtime_mode',value:'mock'}])await api.controlRuntime('p',{expected_version:9,scope,action:'pause',reason:'actual.report'},'current-scope-key')
 assert.equal(calls.length,5)
 for(const call of calls){assert.equal(call.input.expected_version,9);assert.equal(call.input.recovery_verified,undefined);assert.equal(call.input.permission_granted,undefined);assert.equal(call.options.headers['Idempotency-Key'],'current-scope-key')}
})


test('stale reassessment reuses the existing endpoint with encoded segments and the caller original key', async () => {
  const response = { data: { id: 'satisfied-current', status: 'satisfied', decision_by: 'actual-owner', decided_at: '2026-10-17T12:00:00Z', reassessed_from_id: 'stale?other', reverses_change_set_id: 'original/other', stale_reason: 'target_already_current' }, request_id: 'actual.response', meta: { replayed: true, refresh_required: false } }
  const { api, calls } = harness(response)
  const result = await api.reassessMainlineChange('project/other', 'stale?other', 'reassess-original-001')
  assert.equal(calls[0].method, 'post')
  assert.equal(calls[0].url, '/api/v1/lingdoc/projects/project%2Fother/change-sets/stale%3Fother/reassess')
  assert.equal(calls[0].input, undefined)
  assert.equal(calls[0].options.headers['Idempotency-Key'], 'reassess-original-001')
  assert.equal(result, response)
  assert.equal(result.data.status, 'satisfied')
  assert.equal(result.data.decision_by, 'actual-owner')
  assert.equal(result.meta.replayed, true)
})
