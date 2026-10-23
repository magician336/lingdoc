import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'
import { createRequire } from 'node:module'
const require = createRequire(import.meta.url)
const ts = require('typescript'), sfc = require('@vue/compiler-sfc')
const filename = new URL('./RuntimeOpsPanel.vue', import.meta.url)
const parsed = sfc.parse(fs.readFileSync(filename, 'utf8'), { filename: filename.pathname })
const compiled = sfc.compileScript(parsed.descriptor, { id: 'test-runtime-ops' })
// The grouping table is a plain module with no imports, so it is loaded the
// same way the api client test loads its subject: transpile, then hand the
// exports to the component instead of a stub.
const runtimeMetricsExports = {}
vm.runInNewContext(ts.transpileModule(fs.readFileSync(new URL('./runtimeMetrics.ts', import.meta.url), 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText, { exports: runtimeMetricsExports, require })
test('actual operator template compiles with the component binding metadata',()=>{
 const result=sfc.compileTemplate({id:'test-runtime-ops',filename:filename.pathname,source:parsed.descriptor.template.content,compilerOptions:{bindingMetadata:compiled.bindings}})
 assert.equal(result.errors.length,0)
})
function setup(readonly, options = {}) {
  const calls = [], emitted = [], exports = {}
  let keySequence = 0
  const auth = { currentTenantId: 7, effectiveTenantId: 7 }
  const vue = { defineComponent: value => value, ref: value => ({ value }), computed: fn => ({ get value() { return fn() } }), watch() {} }
  // The runtime client and the trial client are two modules; both are recorded
  // by the same transport here, so a call is identified by its method name.
  const api = new Proxy({}, { get: (_, method) => async (...args) => { calls.push({ method, args });if(options.handlers?.[method])return options.handlers[method](...args);return { data: [] } } })
  const js = ts.transpileModule(compiled.content, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  vm.runInNewContext(js, { exports, require(name) { if (name === 'vue') return vue;if(name==='@/stores/auth')return {useAuthStore:()=>auth};if(name==='./runtimeMetrics')return runtimeMetricsExports; return api }, crypto: { randomUUID: () => `test-key-${++keySequence}` }, console })
  const props = { project: { id: 'synthetic.project', name: 'Synthetic',status:'active',project_version:3,template_version:'1',template_copy:{version:1,ruleset_hash:'actual.rules'}, spec_revision: 1, current_context_revision: 1, spec: {}, members: [{ user_id: 'owner', role: 'owner' }] }, readonly, currentUserId: 'owner' }
  const state = exports.default.setup(props, { expose() {}, emit(...args) { emitted.push(args) } })
  return { state, calls, emitted, props,auth }
}
test('read-only panel cannot submit a business mutation even when invoked directly', async () => {
  const { state, calls, emitted } = setup(true)
  let mutations = 0
  await state.mutate('synthetic', {}, async () => { mutations++; return { data: {} } }, 'should not display')
  assert.equal(mutations, 0)
  assert.equal(calls.length, 0)
  assert.equal(emitted.length, 0)
  assert.equal(state.canOwner.value, false)
  assert.equal(state.canManage.value, false)
})
test('failed response retains the original idempotency key for the exact same payload', async () => {
  const { state } = setup(false)
  const keys = []
  const fail = async key => { keys.push(key); throw new Error('synthetic connection lost') }
  await state.mutate('proposal', { before: 'v1', after: 'v2' }, fail, '')
  await state.mutate('proposal', { before: 'v1', after: 'v2' }, fail, '')
  assert.equal(keys.length, 2)
  assert.equal(keys[0], keys[1])
  assert.match(state.error.value, /connection lost/)
})
test('review form does not inherit PASS or a Word result from a previous sample', () => {
  const { state } = setup(false)
  state.reviewResult.value = 'PASS'; state.humanDecision.value = 'accept'; state.wordEvidence.value = 'synthetic.previous'
  state.openReview({ id: 'next-sample', sample_version: 'v2', version: 1 })
  assert.equal(state.reviewResult.value, '')
  assert.equal(state.humanDecision.value, '')
  assert.equal(state.wordEvidence.value, '')
})
test('runtime pause selects each actual scope and its exact current CAS version',async()=>{
  const {state,calls}=setup(false)
  state.runtimeReadReady.value=true;state.reason.value='synthetic scope control'
  for(const [kind,value] of [['project','synthetic.project'],['tenant','7'],['template_version','1'],['ruleset_hash','actual.rules'],['runtime_mode','mock']]){
  state.runtimeScopeKind.value=kind;state.runtimeModeScope.value=kind==='runtime_mode'?value:'';state.controls.value=[{id:'existing',scope:{kind,value},version:9,status:'ACTIVE'}];state.reason.value='synthetic scope control'
  await state.pauseRuntime();const call=calls.filter(item=>item.method==='controlRuntime').at(-1);assert.equal(call.args[1].scope.kind,kind);assert.equal(call.args[1].scope.value,value);assert.equal(call.args[1].expected_version,9)
  state.runtimeReadReady.value=true
 }
})
test('unread runtime state and server authorization errors preserve the proposed input',async()=>{
  const {state,calls}=setup(false,{handlers:{controlRuntime:()=>{throw new Error('current tenant management denied')}}})
  state.reason.value='pending tenant control';state.runtimeScopeKind.value='tenant';await state.pauseRuntime();assert.equal(calls.some(call=>call.method==='controlRuntime'),false)
  state.runtimeReadReady.value=true;await state.pauseRuntime();assert.match(state.error.value,/denied/);assert.equal(state.reason.value,'pending tenant control');assert.equal(state.runtimeScopeKind.value,'tenant')
})
function syntheticRun(){return {id:'run-current',project_id:'synthetic.project',version:4,task_id:'task-current',task_version:2,basis:{spec_revision:1,context_revision:1,asset_versions:[{asset_id:'asset-current',asset_revision:3}]},status:'completed',run_mode:'real_api_fake_model',created_by:'owner',correlation_id:'correlation-current',started_at:'2026-01-10T08:00:00Z',finished_at:'2026-01-10T08:01:00Z',rounds:[],results:[]}}
function prepareSample(state){state.selectedWindow.value={id:'window-current',status:'LOCKED',version:2,starts_at:'2026-01-01T00:00:00Z',ends_at:'2026-02-01T00:00:00Z',included_user_ids:['owner'],included_project_ids:['synthetic.project'],template_version:'1',ruleset_hash:'actual.rules'};state.evidenceOptions.value=[{kind:'research_run',id:'run-current',label:'Actual run'}];state.selectedEvidenceKey.value='research_run/run-current'}
function completeManualFields(state){Object.assign(state.sampleForm.value,{sampleVersion:'sample.v1',fixtureDigest:'actual.fixture',permissionSnapshot:'actual.permission',inputHash:'actual.input',outputHash:'actual.output'});state.sampleOwnershipConfirmed.value=true}
test('typed sample editor derives exact references and preserves unmeasured fields without real or PASS defaults',async()=>{
  const {state,calls}=setup(false,{handlers:{getTrialResearchRun:()=>({data:syntheticRun()})}})
  assert.equal(state.sampleForm.value.runtimeMode,'');assert.equal(state.sampleForm.value.metrics.every(metric=>!metric.measured&&metric.numerator===''&&metric.denominator===''),true)
  prepareSample(state);await state.selectTrialEvidence();assert.equal(state.sampleForm.value.runtimeMode,'real_api_fake_model');assert.equal(state.sampleForm.value.dependencies['asset/asset-current'],'3');assert.equal(state.sampleForm.value.permissionSnapshot,'');assert.equal(state.sampleOwnershipConfirmed.value,false)
  completeManualFields(state);await state.submitSample();const call=calls.find(item=>item.method==='recordTrialSample');assert.ok(call);const input=call.args[2];assert.equal(input.runtime_mode,'real_api_fake_model');assert.equal(input.quality_result,undefined);assert.equal(input.reviewer_id,undefined);assert.equal(input.word_roundtrip_evidence,undefined);assert.equal(input.metrics.length,0);assert.equal(input.process_evidence.main_path,'research_run/run-current')
})
test('run mode and changed receipt version cannot inherit trial verification',async()=>{
  let run=syntheticRun();const {state,calls}=setup(false,{handlers:{getTrialResearchRun:()=>({data:run})}})
  prepareSample(state);await state.selectTrialEvidence();completeManualFields(state);state.sampleForm.value.runtimeMode='real';await state.submitSample();assert.match(state.error.value,/模式/);assert.equal(calls.some(call=>call.method==='recordTrialSample'),false)
  state.sampleForm.value.runtimeMode='real_api_fake_model';run={...run,version:5};await state.submitSample();assert.match(state.error.value,/版本或内容/);assert.equal(state.sampleForm.value.sampleVersion,'sample.v1');assert.equal(state.hasUnsavedChanges.value,true)
})
test('unknown, foreign actor and stale evidence reads preserve manual input without a trusted reference',async()=>{
  for(const result of [null,{...syntheticRun(),created_by:'another-person'},{...syntheticRun(),basis:{...syntheticRun().basis,context_revision:2}}]){
    const {state}=setup(false,{handlers:{getTrialResearchRun:()=>({data:result})}});prepareSample(state);state.sampleForm.value.sampleVersion='pending-real-record';await state.selectTrialEvidence();assert.equal(state.sampleFacts.value,null);assert.equal(state.sampleForm.value.sampleVersion,'pending-real-record');assert.ok(state.error.value)
  }
})
test('an identity change during an actual evidence read cannot replace newer input',async()=>{
  let finish;const pending=new Promise(resolve=>{finish=resolve});const {state,props}=setup(false,{handlers:{getTrialResearchRun:()=>pending}});prepareSample(state);state.sampleForm.value.sampleVersion='kept-input';const reading=state.selectTrialEvidence();props.currentUserId='another-person';finish({data:syntheticRun()});await reading;assert.equal(state.sampleFacts.value,null);assert.equal(state.sampleForm.value.sampleVersion,'kept-input')
})
test('measured metrics require actual numerator, denominator and version rather than blank-as-zero',async()=>{
  const {state,calls}=setup(false,{handlers:{getTrialResearchRun:()=>({data:syntheticRun()})}});prepareSample(state);await state.selectTrialEvidence();completeManualFields(state);const metric=state.sampleForm.value.metrics[0];metric.measured=true;metric.denominator='1';metric.version='measurement.v1';await state.submitSample();assert.match(state.error.value,/分子/);assert.equal(calls.some(call=>call.method==='recordTrialSample'),false);metric.numerator='12';await state.submitSample();const call=calls.find(call=>call.method==='recordTrialSample');assert.equal(call.args[2].metrics[0].numerator,12);assert.equal(call.args[2].metrics[0].denominator,1)
})
test('catalog isolates failed reads and reports truncated histories without manufacturing options',async()=>{
  const {state}=setup(false,{handlers:{listTrialResearchRuns:()=>{throw new Error('read denied')},listTrialReleases:()=>({data:{items:[{id:'snapshot-current',project_id:'synthetic.project',check:{status:'blocked'}}],truncated:true}}),listTrialExports:()=>({data:{items:[],truncated:false}}),listChapters:()=>({data:[]})}});await state.loadEvidenceCatalog();assert.equal(state.evidenceOptions.value.length,1);assert.equal(state.evidenceOptions.value[0].kind,'snapshot');assert.match(state.evidenceReadError.value,/denied/);assert.match(state.evidenceReadError.value,/截断/)
})
test('snapshot sample selection keeps runtime mode, authorization and human measurements unverified',async()=>{
  const snapshot={id:'snapshot-current',project_id:'synthetic.project',is_current:true,snapshot_digest:'actual.snapshot.digest',created_at:'2026-01-10T08:00:00Z',frozen_input:{project_id:'synthetic.project',spec_revision:1,template:{version:'1',ruleset_hash:'actual.rules'},template_copy_version:1,asset_versions:[]},check:{status:'passed'}}
  const {state}=setup(false,{handlers:{getTrialRelease:()=>({data:snapshot})}});state.evidenceOptions.value=[{kind:'snapshot',id:snapshot.id,label:'Actual snapshot'}];state.selectedEvidenceKey.value='snapshot/snapshot-current';await state.selectTrialEvidence();assert.equal(state.sampleForm.value.inputHash,snapshot.snapshot_digest);assert.equal(state.sampleForm.value.runtimeMode,'');assert.equal(state.sampleForm.value.contextRevision,null);assert.equal(state.sampleForm.value.permissionSnapshot,'');assert.equal(state.reviewResult.value,'')
})
test('recovery rejects changed or unrelated artifacts and retains the selected references',async()=>{
  const {state,calls}=setup(false,{handlers:{getTrialRelease:()=>({data:{id:'snapshot-current',project_id:'synthetic.project',is_current:false,check:{status:'passed'}}}),getTrialExport:()=>({data:{id:'export-current',project_id:'synthetic.project',snapshot_id:'snapshot-current',status:'verified',file_sha256:'actual.sha'}})}});const control={id:'paused-current',scope:{kind:'project',value:'synthetic.project'},version:4,status:'PAUSED'};state.controls.value=[control];state.runtimeReadReady.value=true;state.releaseOptions.value=[{id:'snapshot-current'}];state.exportOptions.value=[{id:'export-current',snapshot_id:'snapshot-current'}];state.recoverySnapshot.value='snapshot-current';state.recoveryExport.value='export-current';state.reason.value='actual recovery';await state.resumeRuntime(control);assert.equal(calls.some(call=>call.method==='controlRuntime'),false);assert.match(state.error.value,/陈旧/);assert.equal(state.recoveryExport.value,'export-current')
})

test('selected tenant rather than the home tenant identifies a runtime scope',()=>{
  const {state,auth}=setup(false);auth.currentTenantId=7;auth.effectiveTenantId=8;state.runtimeScopeKind.value='tenant';assert.equal(state.runtimeScope.value.value,'8');assert.equal(state.currentIdentity.value,'8:owner')
})
test('input entered during the receipt read is retained without inherited verification',async()=>{
  let finish;const pending=new Promise(resolve=>{finish=resolve});const {state}=setup(false,{handlers:{getTrialResearchRun:()=>pending}});prepareSample(state);const reading=state.selectTrialEvidence();state.sampleForm.value.metrics[0].numerator='23';finish({data:syntheticRun()});await reading;assert.equal(state.sampleFacts.value,null);assert.equal(state.sampleForm.value.metrics[0].numerator,'23');assert.match(state.error.value,/已保留/)
})
test('missing actual runtime event rejects completion and preserves typed human evidence',async()=>{
  const {state,calls}=setup(false,{handlers:{getTrialResearchRun:()=>({data:syntheticRun()}),listRuntimeEvents:()=>({data:[]})}});prepareSample(state);await state.selectTrialEvidence();completeManualFields(state);state.sampleForm.value.runtimeEventIds=['unknown-event'];state.sampleForm.value.process.manual_review='actual.manual-review';await state.submitSample();assert.equal(calls.some(call=>call.method==='recordTrialSample'),false);assert.match(state.error.value,/事件缺失/);assert.equal(state.sampleForm.value.process.manual_review,'actual.manual-review')
})

test('rollback alerts remain project-scoped requests and never claim execution',async()=>{
  const {state,calls}=setup(false);state.runtimeScopeKind.value='tenant';state.alertSignal.value='dependency/failed';state.alertResponsible.value='owner';state.alertAction.value='rollback';state.alertSeverity.value='P2';state.reason.value='actual rollback request';await state.addAlert();const call=calls.find(item=>item.method==='saveRuntimeAlertRule');assert.ok(call);assert.equal(call.args[1].scope.kind,'project');assert.equal(call.args[1].scope.value,'synthetic.project');assert.equal(call.args[1].action,'rollback');assert.match(state.message.value,/尚未执行/);assert.equal(calls.some(call=>call.method==='reverseMainlineChange'),false)
})

test('the runtime tab groups the server counts and keeps unmeasured metrics unobserved', () => {
  const { state } = setup(false)
  state.metrics.value = { event_count: 5, counts: { 'task/failed': 3, 'download/rejected': 1 }, p95_ms: null, p99_ms: 12, queue_depth: 2, missing: ['latency_p95_p99'] }
  const byKey = new Map(state.runtimeMetricItems.value.map(item => [item.key, item]))
  assert.equal(byKey.get('async_failed').total, 3)
  assert.equal(byKey.get('stale').total, null)
  assert.equal(byKey.get('permission_rejected').total, 1)
  assert.equal(byKey.get('download_reauth').total, 1)
  assert.equal(byKey.get('authorization_denial').total, null)
  assert.match(String(byKey.get('authorization_allow').unavailableReason), /未提供/)
  // The rows come from a vm realm, so compare the joined keys instead of
  // deep-comparing an array against one from this realm.
  assert.equal(state.runtimeCountRows.value.map(row => row.key).join(','), 'download/rejected,task/failed')
})

test('the runtime and trial tabs stay reachable for on-call and trial operators', () => {
  const { state } = setup(false)
  const ids = state.tabs.map(entry => entry.id)
  assert.equal(ids.includes('runtime'), true)
  assert.equal(ids.includes('trial'), true)
  assert.equal(ids.includes('changes'), false)
  assert.equal(ids.includes('impacts'), false)
})

test('the metric blocks stay inside the runtime section and the trial section keeps its content', () => {
  const source = fs.readFileSync(new URL('./RuntimeOpsPanel.vue', import.meta.url), 'utf8')
  const runtimeStart = source.indexOf("tab === 'runtime'")
  const trialStart = source.indexOf("tab === 'trial'")
  assert.ok(runtimeStart > 0, 'the runtime section must exist')
  assert.ok(trialStart > runtimeStart, 'the trial section must follow the runtime section')
  // The metric blocks have to sit between the two section markers.  A plain
  // presence check would still pass with the whole block deleted, because the
  // script section defines the same names.
  for (const marker of ['runtimeMetricItems', 'runtimeCountRows']) {
    const at = source.indexOf(marker, runtimeStart)
    assert.ok(at > runtimeStart && at < trialStart, `${marker} must live inside the runtime section`)
  }
  const trialSection = source.slice(trialStart, source.indexOf('</script>', trialStart))
  assert.ok(trialSection.includes('selectedWindow'), 'the trial section must keep the trial window content')
})

test('the change-and-impact panel no longer carries the operations surface', () => {
  const source = fs.readFileSync(new URL('./ImpactOpsPanel.vue', import.meta.url), 'utf8')
  assert.equal(source.includes("tab === 'runtime'"), false, 'the runtime tab must have left the change-and-impact panel')
  assert.equal(source.includes("tab === 'trial'"), false, 'the trial tab must have left the change-and-impact panel')
  assert.equal(source.includes('@/api/lingdoc/runtimeControl'), false, 'the change-and-impact panel must not import the runtime client')
  assert.equal(source.includes('@/api/lingdoc/trialWindow'), false, 'the change-and-impact panel must not import the trial client')
  assert.equal(source.includes('runtimeMetrics'), false, 'the change-and-impact panel must not project the runtime metrics')
})

test('the workspace gives the operations panel an entrance of its own', () => {
  const source = fs.readFileSync(new URL('./Workspace.vue', import.meta.url), 'utf8')
  assert.match(source, /import RuntimeOpsPanel from '\.\/RuntimeOpsPanel\.vue'/)
  assert.match(source, /<RuntimeOpsPanel v-if="fullPanel === 'operations'"/)
  assert.match(source, /type FullPanel = [^\n]*'operations'/)
  assert.match(source, /openFullPanel\('operations'\)/)
  assert.match(source, /fullPanelBusy=computed\(\(\)=>[^\n]*runtimeOpsPanelRef\.value\?\.busy/)
  // The panel must receive the same props the old combined panel received.
  // Dropping `readonly` would let a read-only operator submit writes, and
  // nothing else in the suite would notice.
  assert.match(source, /:readonly="fullReadonly"/)
  assert.match(source, /:current-user-id="auth\.currentUserId \?\? ''"/)
  // The entrance needs a label and the same availability rule as the old
  // path, otherwise the panel becomes reachable in states it was not.
  assert.match(source, /operations:'运行观测与真实试用'/)
  assert.match(source, /运行观测与试用/)
  assert.match(source, /project\.status !== 'active'/)
})

test('an edited sample version counts as an unsaved change', () => {
  const { state } = setup(false)
  assert.equal(state.hasUnsavedChanges.value, false)
  state.sampleForm.value.sampleVersion = 'sample.v2'
  assert.equal(state.hasUnsavedChanges.value, true)
})
