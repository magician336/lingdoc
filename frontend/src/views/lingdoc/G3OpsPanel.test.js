import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'
import { createRequire } from 'node:module'
const require = createRequire(import.meta.url)
const ts = require('typescript'), sfc = require('@vue/compiler-sfc')
const filename = new URL('./G3OpsPanel.vue', import.meta.url)
const parsed = sfc.parse(fs.readFileSync(filename, 'utf8'), { filename: filename.pathname })
const compiled = sfc.compileScript(parsed.descriptor, { id: 'test-g3-ops' })
test('actual operator template compiles with the component binding metadata',()=>{
 const result=sfc.compileTemplate({id:'test-g3-ops',filename:filename.pathname,source:parsed.descriptor.template.content,compilerOptions:{bindingMetadata:compiled.bindings}})
 assert.equal(result.errors.length,0)
})
function setup(readonly, options = {}) {
  const calls = [], emitted = [], exports = {}
  let keySequence = 0
  const auth = { currentTenantId: 7, effectiveTenantId: 7 }
  const vue = { defineComponent: value => value, ref: value => ({ value }), computed: fn => ({ get value() { return fn() } }), watch() {} }
  const api = new Proxy({}, { get: (_, method) => async (...args) => { calls.push({ method, args });if(options.handlers?.[method])return options.handlers[method](...args); if(method==='getImpactComputationReceipt')return {data:options.receipts?.[args[1]] || null};return { data: [] } } })
  const js = ts.transpileModule(compiled.content, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  vm.runInNewContext(js, { exports, require(name) { if (name === 'vue') return vue;if(name==='@/stores/auth')return {useAuthStore:()=>auth}; return api }, crypto: { randomUUID: () => `test-key-${++keySequence}` }, console })
  const props = { project: { id: 'synthetic.project', name: 'Synthetic',status:'active',project_version:3,template_version:'1',template_copy:{version:1,ruleset_hash:'actual.rules'}, spec_revision: 1, current_context_revision: 1, spec: {}, members: [{ user_id: 'owner', role: 'owner' }] }, readonly, currentUserId: 'owner', preflightReceiptId: options.preflightReceiptId || '' }
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
test('a previous project impact response cannot replace the current project view', async () => {
  const { state, props } = setup(false)
  const pending = state.showAssessment({ id: 'synthetic.change', project_id: 'synthetic.project' })
  props.project.id = 'synthetic.next-project'
  await pending
  assert.equal(state.assessment.value, null)
})

test('a semantic claim requires explicit current consent and never derives scientific confirmation', () => {
 const { state }=setup(false)
 state.semanticKind.value='ResearchClaim';state.semanticTarget.value='claim-current';state.semanticVersion.value=3;state.claimText.value='synthetic claim';state.claimScope.value='synthetic scope'
 state.addSemanticDelta();assert.equal(state.semanticDraft.value.length,0);assert.match(state.error.value,/确认/)
 state.claimConsent.value=true;state.addSemanticDelta();assert.equal(state.semanticDraft.value.length,1);assert.equal(state.semanticDraft.value[0].proposed_claim.confirmed,true)
 state.resetSemanticEditor();assert.equal(state.claimConsent.value,false);assert.equal(state.assetModelConsent.value,false);assert.equal(state.routeStatus.value,'planned')
})
test('recomputation completion submits a server result receipt instead of the old rule target', async () => {
 const receipt={id:'actual-check',project_id:'synthetic.project',version:7,context_revision:1,source_basis_hash:'actual-sources',computation_basis_hash:'actual-basis',input_hash:'actual-input',evaluations:[{status:'ISSUE'}]}
 const { state,calls }=setup(false,{receipts:{'actual-check':receipt}})
 state.reason.value='synthetic actual recompute';state.closingTask.value={id:'impact-current',target:{kind:'RuleBinding',id:'rule-a',version:'2'},target_revision:1,action:'RECOMPUTE',version:4};state.closureReceipt.value='actual-check';state.closureVersion.value='1'
 await state.closeTask();const call=calls.find(item=>item.method==='actOnImpactTask');assert.ok(call);assert.equal(call.args[2].closure_evidence[0].kind,'ComputationReceipt');assert.equal(call.args[2].closure_evidence[0].id,'actual-check');assert.equal(call.args[2].closure_evidence[0].version,'7');assert.equal(calls[0].method,'getImpactComputationReceipt')
})
test('semantic-only drafts preserve the typed proposal array without manufacturing before state', async () => {
 const { state,calls }=setup(false)
 state.reason.value='synthetic draft';state.semanticDraft.value=[{kind:'RouteNode',target_id:'route-new',expected_version:0,proposed_route:{name:'Synthetic',status:'planned',required:true,inputs:[]}}]
 await state.saveDraft();const call=calls.find(item=>item.method==='createChangeDraft');assert.ok(call);assert.equal(Object.keys(call.args[1].fields).length,0);assert.equal(call.args[1].semantic_deltas[0].proposed_route.status,'planned');assert.equal(call.args[1].semantic_deltas[0].before_hash,undefined)
})


test('unsaved state covers semantic, dependency, disposition and trial inputs', () => {
 const {state}=setup(false);assert.equal(state.hasUnsavedChanges.value,false)
 state.assetPurpose.value='synthetic purpose';assert.equal(state.hasUnsavedChanges.value,true);state.resetAllForms();assert.equal(state.hasUnsavedChanges.value,false)
 state.targetRef.value.id='unsubmitted dependency';assert.equal(state.hasUnsavedChanges.value,true);state.resetAllForms()
 state.reason.value='unsent human decision';assert.equal(state.hasUnsavedChanges.value,true);state.resetAllForms()
 state.sampleForm.value.sampleVersion='unsubmitted real receipt';assert.equal(state.hasUnsavedChanges.value,true)
})
test('a previous view epoch response cannot clear current pending input', async () => {
 const {state}=setup(false);state.reason.value='pending newer input';let finish
 const response=new Promise(resolve=>{finish=resolve});const mutation=state.mutate('epoch-test',{},()=>response,'old success')
 await state.refresh();finish({data:{}});await mutation
 assert.equal(state.reason.value,'pending newer input');assert.equal(state.message.value,'');assert.equal(state.hasUnsavedChanges.value,true)
})

test('a missing or stale server computation receipt cannot become typed version one proof',async()=>{
 for(const receipt of [null,{id:'actual-check',project_id:'synthetic.project',version:1,context_revision:2,source_basis_hash:'sources',computation_basis_hash:'basis',input_hash:'input',evaluations:[]}]){
  const {state,calls}=setup(false,{receipts:{'actual-check':receipt}})
  state.reason.value='synthetic current review';state.closingTask.value={id:'task',target:{kind:'RuleBinding',id:'rule',version:'2'},target_revision:1,action:'RECOMPUTE',version:4};state.closureReceipt.value='actual-check';state.closureVersion.value='1'
  await state.closeTask();assert.equal(calls.some(call=>call.method==='actOnImpactTask'),false);assert.match(state.error.value,/不一致/)
 }
})
test('actual parent check receipt prefills without creating unsaved changes and preserves user input',()=>{
 const {state}=setup(false,{preflightReceiptId:'actual-initial'})
 assert.equal(state.preflightSnapshot.value,'actual-initial');assert.equal(state.hasUnsavedChanges.value,false)
 state.syncPreflightReceipt('actual-next');assert.equal(state.preflightSnapshot.value,'actual-next');assert.equal(state.hasUnsavedChanges.value,false)
 state.preflightSnapshot.value='user-selected';state.syncPreflightReceipt('actual-third');assert.equal(state.preflightSnapshot.value,'user-selected');assert.equal(state.hasUnsavedChanges.value,true)
})
test('check closure selects only actual applied changes at the current context revision',()=>{
 const {state}=setup(false)
 state.changes.value=[{id:'current',status:'applied',target_context_revision:1},{id:'old',status:'applied',target_context_revision:0},{id:'draft',status:'draft',target_context_revision:1}]
 assert.equal(state.preflightChanges.value.length,1);assert.equal(state.preflightChanges.value[0].id,'current')
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
 const {state,calls}=setup(false,{handlers:{getTrialResearchRun:()=>({data:syntheticRun()})}});prepareSample(state);await state.selectTrialEvidence();completeManualFields(state);const metric=state.sampleForm.value.metrics[0];metric.measured=true;metric.denominator='1';metric.version='measurement.v1';await state.submitSample();assert.match(state.error.value,/分子/);assert.equal(calls.some(call=>call.method==='recordTrialSample'),false);metric.numerator='12';await state.submitSample();const call=calls.find(item=>item.method==='recordTrialSample');assert.equal(call.args[2].metrics[0].numerator,12);assert.equal(call.args[2].metrics[0].denominator,1)
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


function mainlineChange(status, extra = {}) {
  return { id: 'change-current', project_id: 'synthetic.project', created_by: 'owner', reason: 'Actual historical proposal', status, base_context_revision: 1, base_spec_revision: 1, fields: [{ key: 'research_goal', old_value: 'old goal', new_value: 'new goal' }], impacts: [], created_at: '2026-10-17T10:00:00Z', ...extra }
}
function renderChanges(state, props) {
  const template = sfc.compileTemplate({ id: 'test-g3-render', filename: filename.pathname, source: parsed.descriptor.template.content, compilerOptions: { bindingMetadata: compiled.bindings } })
  assert.equal(template.errors.length, 0)
  const output = ts.transpileModule(template.code, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const exports = {}
  vm.runInNewContext(output, { exports, require(name) { assert.equal(name, 'vue'); return require('vue') } })
  const setupState = new Proxy(state, { get(target, key) { const value = target[key]; return value && typeof value === 'object' && 'value' in value ? value.value : value } })
  return exports.render({ $emit() {} }, [], props, setupState, {}, {})
}
function renderedNodes(node) {
  if (!node || typeof node !== 'object') return []
  const children = Array.isArray(node) ? node : Array.isArray(node.children) ? node.children : []
  return [node, ...children.flatMap(renderedNodes)]
}
function renderedText(node) {
  if (typeof node === 'string' || typeof node === 'number') return String(node)
  if (!node || typeof node !== 'object') return ''
  const children = Array.isArray(node) ? node : node.children
  return Array.isArray(children) ? children.map(renderedText).join(' ') : typeof children === 'string' ? children : ''
}

test('the rendered change history shows actual decision metadata, source links, stale reason and satisfied terminal meaning', () => {
  const { state, props } = setup(true)
  const source = mainlineChange('stale', { id: 'source/change', stale_reason: 'target_changed' })
  const satisfied = mainlineChange('satisfied', { id: 'satisfied-current', decision_by: 'actual-decision-owner', decided_at: '2026-10-17T12:00:00Z', reassessed_from_id: source.id, reverses_change_set_id: 'original?change' })
  const legacy = mainlineChange('rejected', { id: 'legacy-current', created_by: 'legacy-author-not-decision-owner', applied_at: 'legacy-apply-time-not-decision-time' })
  state.changes.value = [source, satisfied, legacy]
  const view = renderChanges(state, props), text = renderedText(view), nodes = renderedNodes(view)
  assert.match(text, /目标已满足/)
  assert.match(text, /未新增正式基线/)
  assert.match(text, /actual-decision-owner/)
  assert.match(text, /2026-10-17T12:00:00Z/)
  assert.match(text, /target_changed/)
  assert.equal(text.includes('legacy-author-not-decision-owner'), false)
  assert.equal(text.includes('legacy-apply-time-not-decision-time'), false)
  assert.equal(state.decisionText(legacy), '')
  const links = nodes.filter(node => node.type === 'a')
  for (const id of [source.id, 'original?change']) assert.equal(links.some(node => node.props.href === state.changeLink(id)), true)
  assert.equal(nodes.some(node => node.type === 'article' && node.props.id === state.changeAnchor(source.id)), true)
  assert.equal(decodeURIComponent(state.changeLink(source.id).slice(1)), state.changeAnchor(source.id))
})

test('Admin keeps coordination access but direct invocation cannot make Owner decisions or reverse', async () => {
  const { state, calls, props } = setup(false)
  props.project.members = [{ user_id: 'owner', role: 'writer', governance_role: 'admin' }]
  assert.equal(state.canManage.value, true)
  assert.equal(state.canOwner.value, false)
  state.reason.value = 'Unsubmitted coordination input'
  for (const [action, status] of [['apply', 'assessed'], ['reject', 'assessed'], ['reverse', 'applied'], ['stabilize', 'applied']]) {
    const change = mainlineChange(status)
    state.changes.value = [change]
    await state.changeAction(change, action)
  }
  assert.equal(calls.length, 0)
  assert.equal(state.reason.value, 'Unsubmitted coordination input')
})

test('current writers can assess and reassess while current Owners can perform the supported decision actions', async () => {
  const cases = [
    ['writer', 'draft', 'assess', 'assessMainlineChange'],
    ['writer', 'stale', 'reassess', 'reassessMainlineChange'],
    ['owner', 'assessed', 'apply', 'applyChangeSet'],
    ['owner', 'assessed', 'reject', 'rejectChangeSet'],
    ['owner', 'applied', 'reverse', 'reverseMainlineChange'],
    ['owner', 'applied', 'stabilize', 'stabilizeRevision'],
  ]
  for (const [role, status, action, method] of cases) {
    const { state, calls, props } = setup(false)
    props.project.members = [{ user_id: 'owner', role }]
    const change = mainlineChange(status)
    state.changes.value = [change]
    await state.changeAction(change, action)
    const requests = calls.filter(call => call.method === method)
    assert.equal(requests.length, 1, `${role} ${status} ${action}`)
    assert.equal(requests[0].args[0], props.project.id)
    assert.equal(requests[0].args[1], change.id)
  }
})

test('state checks reject terminal results, foreign records and stale object references before any write', async () => {
  const { state, calls } = setup(false)
  for (const status of ['satisfied', 'rejected']) {
    const change = mainlineChange(status)
    state.changes.value = [change]
    for (const action of ['assess', 'reassess', 'apply', 'reject', 'reverse', 'stabilize']) await state.changeAction(change, action)
  }
  const old = mainlineChange('assessed')
  state.changes.value = [{ ...old, status: 'satisfied' }]
  await state.changeAction(old, 'apply')
  state.changes.value = [old]
  await state.changeAction({ ...old, project_id: 'other-project' }, 'apply')
  assert.equal(calls.length, 0)
})

test('read-only or inactive current projects cannot invoke change actions or save drafts directly', async () => {
  for (const readonly of [true, false]) {
    const { state, calls, props } = setup(readonly)
    if (!readonly) props.project.status = 'discarded'
    const change = mainlineChange('assessed')
    state.changes.value = [change]
    state.reason.value = 'Kept proposal'
    state.draftField.value = 'research_goal'
    state.draftValue.value = 'Kept value'
    await state.changeAction(change, 'assess')
    await state.changeAction(change, 'apply')
    await state.saveDraft()
    state.editDraft(change)
    assert.equal(calls.length, 0)
    assert.equal(state.reason.value, 'Kept proposal')
    assert.equal(state.draftValue.value, 'Kept value')
  }
})

test('a changed draft status or authorship preserves pending edits instead of sending an invalid edit', async () => {
  const { state, calls, props } = setup(false)
  const change = mainlineChange('draft')
  state.changes.value = [change]
  state.editDraft(change)
  state.reason.value = 'Pending edited reason'
  state.draftValue.value = 'Pending edited value'
  state.changes.value = [{ ...change, status: 'satisfied' }]
  await state.saveDraft()
  props.project.members = [{ user_id: 'owner', role: 'writer', governance_role: 'admin' }]
  state.changes.value = [{ ...change, created_by: 'another-author' }]
  await state.saveDraft()
  assert.equal(calls.length, 0)
  assert.equal(state.reason.value, 'Pending edited reason')
  assert.equal(state.draftValue.value, 'Pending edited value')
})

test('reassessment preserves the stale source and pending draft while displaying the returned satisfied result', async () => {
  const stale = mainlineChange('stale'), satisfied = mainlineChange('satisfied', { id: 'satisfied-current', reassessed_from_id: stale.id })
  const { state, calls } = setup(false, { handlers: { reassessMainlineChange: () => ({ data: satisfied }), listMainlineChanges: () => ({ data: [stale, satisfied] }) } })
  state.changes.value = [stale]
  state.reason.value = 'Pending draft reason'
  state.draftValue.value = 'Pending draft value'
  await state.changeAction(stale, 'reassess')
  const request = calls.find(call => call.method === 'reassessMainlineChange')
  assert.ok(request)
  assert.equal(request.args[0], 'synthetic.project')
  assert.equal(request.args[1], stale.id)
  assert.equal(stale.status, 'stale')
  assert.equal(state.changes.value.some(change => change.id === satisfied.id && change.status === 'satisfied'), true)
  assert.equal(state.reason.value, 'Pending draft reason')
  assert.equal(state.draftValue.value, 'Pending draft value')
  await state.changeAction(satisfied, 'apply')
  assert.equal(calls.some(call => call.method === 'applyChangeSet'), false)
})

test('unknown reassessment outcomes retain the exact original key and all pending input for retry', async () => {
  const { state, calls } = setup(false, { handlers: { reassessMainlineChange: () => { throw new Error('unknown connection outcome') } } })
  const stale = mainlineChange('stale')
  state.changes.value = [stale]
  state.reason.value = 'Pending draft reason'
  state.draftValue.value = 'Pending draft value'
  await state.changeAction(stale, 'reassess')
  await state.changeAction(stale, 'reassess')
  const requests = calls.filter(call => call.method === 'reassessMainlineChange')
  assert.equal(requests.length, 2)
  assert.equal(requests[0].args[2], requests[1].args[2])
  assert.equal(state.reason.value, 'Pending draft reason')
  assert.equal(state.draftValue.value, 'Pending draft value')
  assert.match(state.error.value, /unknown connection outcome/)
})

test('an identity or view epoch change during reassessment cannot clear or overwrite newer pending input', async () => {
  for (const changed of ['identity', 'epoch']) {
    let finish
    const pending = new Promise(resolve => { finish = resolve })
    const { state, props } = setup(false, { handlers: { reassessMainlineChange: () => pending } })
    const stale = mainlineChange('stale')
    state.changes.value = [stale]
    const request = state.changeAction(stale, 'reassess')
    state.reason.value = 'Typed while waiting'
    state.draftValue.value = 'Newer pending value'
    if (changed === 'identity') props.currentUserId = 'another-person'
    else await state.refresh()
    finish({ data: mainlineChange('satisfied') })
    await request
    assert.equal(state.reason.value, 'Typed while waiting')
    assert.equal(state.draftValue.value, 'Newer pending value')
    assert.equal(state.message.value, '')
  }
})

test('assessment reads also ignore a previous current identity', async () => {
  let finish
  const pending = new Promise(resolve => { finish = resolve })
  const { state, props } = setup(false, { handlers: { getImpactAssessment: () => pending } })
  const request = state.showAssessment(mainlineChange('assessed'))
  props.currentUserId = 'another-person'
  finish({ data: { input_hash: 'previous-identity-result' } })
  await request
  assert.equal(state.assessment.value, null)
})


test('satisfied recovery does not request a fabricated impact assessment', async () => {
  const { state, calls, props } = setup(false)
  state.assessment.value = { change_set_id: 'previous-change' }
  await state.showAssessment({ id: 'satisfied-copy', project_id: props.project.id, status: 'satisfied' })
  assert.equal(calls.length, 0)
  assert.equal(state.assessment.value, null)
})


test('confirmed evidence relationship uses the existing draft form and fresh consent without applying', async () => {
 const {state,calls}=setup(false)
 state.semanticKind.value='EvidenceCard'
 state.cards.value=[{id:'formal-card',version:4,claim_ref:{id:'current-claim',version:2},anchor_refs:[{id:'original-anchor',version:3}],support_type:'supports',scope:'bounded original scope',scope_chapter_ids:['current-chapter'],limitations:'synthetic',gaps:'',alternative_explanation:'',status:'CONFIRMED',confirmation:{confirmed_by:'old-user'}}]
 state.semanticTarget.value='formal-card';state.selectSemanticTarget()
 assert.equal(state.semanticVersion.value,4);assert.equal(state.cardConsent.value,false)
 const edited=JSON.parse(state.cardEditor.value);edited.support_type='refutes';state.cardEditor.value=JSON.stringify(edited)
 state.addSemanticDelta();assert.equal(state.semanticDraft.value.length,0);assert.match(state.error.value,/确认/)
 state.cardConsent.value=true;state.addSemanticDelta();assert.equal(state.semanticDraft.value.length,1)
 const delta=state.semanticDraft.value[0]
 assert.equal(delta.proposed_card.support_type,'refutes');assert.equal(delta.proposed_card.confirmed,true)
 assert.equal(delta.proposed_card.status,undefined);assert.equal(delta.proposed_card.confirmation,undefined)
 state.reason.value='explicit evidence relationship review';await state.saveDraft()
 const call=calls.find(item=>item.method==='createChangeDraft');assert.ok(call)
 assert.equal(call.args[1].semantic_deltas[0].kind,'EvidenceCard');assert.equal(call.args[1].semantic_deltas[0].expected_version,4)
 assert.equal(calls.some(item=>item.method==='applyChangeSet'||item.method==='saveEvidenceCard'),false)
 state.resetSemanticEditor();assert.equal(state.cardConsent.value,false);assert.equal(state.cardEditor.value,'{}')
})

test('evidence JSON cannot smuggle confirmation or source authority through the editor', () => {
 const {state}=setup(false);state.semanticKind.value='EvidenceCard';state.semanticTarget.value='formal-card';state.semanticVersion.value=4;state.cardConsent.value=true
 state.cardEditor.value=JSON.stringify({claim_ref:{id:'claim',version:1},anchor_refs:[{id:'anchor',version:1}],support_type:'supports',scope:'scope',scope_chapter_ids:['chapter'],status:'CONFIRMED'})
 state.addSemanticDelta();assert.equal(state.semanticDraft.value.length,0);assert.match(state.error.value,/合法证据关系/)
})
