import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import { compileScript, parse } from '@vue/compiler-sfc'
import { createSSRApp, nextTick } from 'vue'
import { renderToString } from '@vue/server-renderer'
import ts from 'typescript'

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./TemplateGovernancePanel.vue', import.meta.url))
const { descriptor, errors } = parse(readFileSync(filename, 'utf8'), { filename })
assert.deepEqual(errors, [])
const compiled = ts.transpileModule(compileScript(descriptor, { id: 'governance-retry-test' }).content, {
 compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

// Execute the real SFC handlers. SSR does not run onMounted HTTP loading;
// this fixture proves error-event propagation, not browser or server behavior.
async function fixture(api: Record<string, any> = {}) {
 const exports: any = {}
 runInNewContext(compiled, {
  exports,
  require(name: string) {
   if (name === 'vue') return require('vue')
   if (name === './templateGovernanceState') return require('./templateGovernanceState.ts')
   if (name === '@/views/lingdoc/firstPassEntryState') return require('../views/lingdoc/firstPassEntryState.ts')
   if (name === '@/api/lingdoc/candidateAdoption') return { newIdempotencyKey: () => 'synthetic-key' }
   if (name === '@/api/lingdoc/templateGovernance') return api
   if (name === './TemplateGovernanceFixturePicker.vue') return { default: {} }
   throw new Error(`Unexpected test dependency: ${name}`)
  },
 })
 const events: string[] = []
 let controller: any
 const component = {
  ...exports.default,
  setup(props: any, context: any) { controller = exports.default.setup(props, context); return controller },
  render: () => null,
 }
 await renderToString(createSSRApp(component, {
  project: { id: 'synthetic-project', name: 'Synthetic', project_version: 1, spec_revision: 0, members: [], spec: {}, template_id: 'template-demo', template_version: '1' },
  chapters: [], sources: [], canManageCatalog: true,
  onError: (message: string) => events.push(message),
 }))
 return { controller, events, api }
}

function deferred<T>() {
 let resolve!: (value: T) => void
 let reject!: (reason?: unknown) => void
 const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
 return { promise, resolve, reject }
}

test('real governance panel clears the parent error when retry succeeds', async () => {
 const { controller, events } = await fixture()
 await controller.perform(async () => { throw { error: { code: 'unrecognized_fixture' } } })
 assert.match(controller.errorMessage.value, /服务请求失败/)
 assert.equal(events.at(-1), controller.errorMessage.value)
 await controller.perform(async () => { controller.message.value = '服务返回：已发布。' })
 assert.equal(controller.errorMessage.value, '')
 assert.equal(controller.message.value, '服务返回：已发布。')
 assert.equal(events.at(-1), '')
 assert.equal(controller.busy.value, false)
})

test('failed retry publishes its new error and a concurrent ignored call cannot clear it', async () => {
 const { controller, events } = await fixture()
 await controller.perform(async () => { throw { error: { code: 'version_conflict' } } })
 assert.match(events.at(-1)!, /版本冲突/)
 controller.busy.value = true
 const previous = events.slice()
 await controller.perform(async () => { assert.fail('busy operation must not execute') })
 assert.deepEqual(events, previous)
 assert.match(controller.errorMessage.value, /版本冲突/)
})

test('newer catalog request wins when an older response arrives late', async () => {
 const older = deferred<any>(), newer = deferred<any>()
 let calls = 0
 const { controller, api } = await fixture()
 api.listTemplateProfiles = () => ++calls === 1 ? older.promise : newer.promise

 const first = controller.loadCatalog()
 const second = controller.loadCatalog()
 newer.resolve({ data: [{ template_id: 'current-profile' }] })
 await second
 older.resolve({ data: [{ template_id: 'stale-profile' }] })
 await first

 assert.deepEqual(controller.profiles.value.map((profile: any) => profile.template_id), ['current-profile'])
 assert.equal(controller.catalogError.value, '')
 assert.equal(controller.catalogLoading.value, false)
})

test('catalog refresh invalidates a pending profile selection response', async () => {
 const pendingVersions = deferred<any>()
 const { controller, api } = await fixture()
 api.listTemplateVersions = () => pendingVersions.promise
 api.listTemplateProfiles = async () => ({ data: [] })
 controller.profileId.value = 'previous-profile'

 const choosing = controller.chooseProfile('late-profile')
 await controller.loadCatalog()
 pendingVersions.resolve({ data: [{ template_id: 'late-profile', version: '1', ruleset: { ruleset_id: '', version: '' } }] })
 await choosing

 assert.equal(controller.profileId.value, '')
	assert.equal(controller.versions.value.length, 0)
 assert.equal(controller.selectedVersionId.value, '')
})

// Draft version selected in the catalog tab: the registration entry and the
// coverage matrix are manager-only and target exactly this version.
const draftVersion = { template_id: 'template-demo', version: '3', revision: 4, state: 'draft',
 definition: { name: 'Draft', fields: [], sections: [] }, ruleset: { ruleset_id: 'ruleset-demo', version: '1' } }
function selectDraftVersion(controller: any) {
 controller.versions.value = [draftVersion]
 controller.selectedVersionId.value = '3'
}
const registeredFixtureInput = {
 fixture_id: 'org-goal-present-3-write_check-negative-1', rule_id: 'goal-present', scene: 'write_check', polarity: 'negative',
 target_ref: 'field/goal', input: { project_id: 'replay-synthetic', target_version: '1', fields: { goal: '' } },
 expected_status: 'ISSUE', review_reference: 'review-record-2026-001',
}

test('registerFixtures posts one audited batch; the receipt coverage and enriched catalog reach the panel', async () => {
 const registrationCalls: any[] = []
 let catalogLoads = 0
 const receipt = { data: { template_id: 'template-demo', version: '3', content_hash: 'sha:content-3', policy_hash: 'sha:policy-3',
  registered: [{ fixture_id: registeredFixtureInput.fixture_id, rule_id: 'goal-present', scene: 'write_check', polarity: 'negative', target_ref: 'field/goal', ruleset: { ruleset_id: 'ruleset-demo', version: '1' } }],
  coverage: { complete: false, entries: [{ rule_id: 'goal-present', scene: 'write_check', positive_covered: true, negative_covered: true,
   declared_positive_example_refs: ['ex-pos'], declared_negative_example_refs: ['ex-neg'], unsatisfied_declared_example_refs: ['ex-pos'] }], unsatisfied_declared_example_refs: ['ex-pos'] } } }
 const { controller } = await fixture({
  registerReplayFixtures: (input: any, key: string) => { registrationCalls.push({ input, key }); return Promise.resolve(receipt) },
  listReplayFixtures: () => { catalogLoads += 1; return Promise.resolve({ data: [{ fixture_id: registeredFixtureInput.fixture_id, rule_id: 'goal-present', scene: 'write_check', polarity: 'negative', target_ref: 'field/goal', ruleset: { ruleset_id: 'ruleset-demo', version: '1' }, template_id: 'template-demo', template_version: '3' }] }) },
 })
 selectDraftVersion(controller)
 controller.fixtureRegistration.reason = '补齐 goal-present 反例并核对声明引用'
 controller.fixtureRegistration.fixturesJSON = JSON.stringify([registeredFixtureInput])

 await controller.registerFixtures()

 assert.equal(registrationCalls.length, 1)
 // The panel executes inside a VM realm, so compare through JSON to avoid
 // cross-realm prototype identity while still asserting the exact payload.
 assert.deepEqual(JSON.parse(JSON.stringify(registrationCalls[0].input)), { template_id: 'template-demo', version: '3', reason: '补齐 goal-present 反例并核对声明引用', fixtures: [registeredFixtureInput] })
 assert.equal(registrationCalls[0].key, 'synthetic-key')
 assert.equal(controller.replayCoverage.value.complete, false)
 assert.deepEqual(controller.replayCoverage.value.unsatisfied_declared_example_refs, ['ex-pos'])
 assert.match(controller.message.value, /已登记 1 条回放夹具/)
 assert.match(controller.message.value, /仍有缺口/)
 // The publish picker reads the same enriched catalog source, publish payload unchanged.
 assert.equal(catalogLoads, 1)
 assert.deepEqual(controller.replayFixtures.value.map((item: any) => item.fixture_id), [registeredFixtureInput.fixture_id])
 assert.equal(controller.busy.value, false)
})

test('loadReplayCoverage stores the manager-only matrix rows of the selected draft version for display', async () => {
 const coverageCalls: string[][] = []
 const view = { data: { template_id: 'template-demo', version: '3', content_hash: 'sha:content-3', policy_hash: 'sha:policy-3', complete: false,
  entries: [
   { rule_id: 'goal-present', scene: 'write_check', positive_covered: true, negative_covered: false, declared_positive_example_refs: [], declared_negative_example_refs: [], unsatisfied_declared_example_refs: [] },
   { rule_id: 'source-fresh', scene: 'preflight', positive_covered: true, negative_covered: true, declared_positive_example_refs: ['ex-1'], declared_negative_example_refs: [], unsatisfied_declared_example_refs: ['ex-1'] },
  ], unsatisfied_declared_example_refs: ['ex-1'] } }
 const { controller } = await fixture({ readReplayCoverage: (...args: string[]) => { coverageCalls.push(args); return Promise.resolve(view) } })
 selectDraftVersion(controller)

 await controller.loadReplayCoverage()

 assert.deepEqual(coverageCalls, [['template-demo', '3']])
 assert.equal(controller.replayCoverage.value.complete, false)
 assert.equal(controller.replayCoverage.value.entries[0].negative_covered, false)
 assert.equal(controller.replayCoverage.value.entries[1].unsatisfied_declared_example_refs.join('、'), 'ex-1')
 assert.equal(controller.busy.value, false)
})

test('failed registration presents 422/400 evidence readably and keeps the typed batch for fixing', async () => {
 const registrationCalls: any[] = []
 const { controller, events } = await fixture({
  registerReplayFixtures: (input: any, key: string) => { registrationCalls.push({ input, key }); return Promise.reject(Object.defineProperty({ status: 422,
   message: '夹具 org-goal-present-3-write_check-negative-1 声明 ISSUE 但实际求值 PASS。',
   error: { code: 'template_replay_required', message: '须用服务器注册且绑定当前版本的正反例完成回放。', retryable: false }, request_id: 'req-1' }, '$httpStatus', { value: 422 })) },
 })
 selectDraftVersion(controller)
 controller.fixtureRegistration.reason = '登记理由'
 controller.fixtureRegistration.fixturesJSON = JSON.stringify([registeredFixtureInput])

 await controller.registerFixtures()

 assert.match(controller.errorMessage.value, /回放核验未通过（422）/)
 assert.match(controller.errorMessage.value, /声明 ISSUE 但实际求值 PASS/)
 assert.equal(events.at(-1), controller.errorMessage.value)

 // Local 400 boundary: unreadable JSON never reaches the server, input kept.
 controller.fixtureRegistration.fixturesJSON = '{broken'
 await controller.registerFixtures()
 assert.match(controller.errorMessage.value, /不是可解析的 JSON/)
 assert.equal(controller.fixtureRegistration.fixturesJSON, '{broken')
 assert.equal(registrationCalls.length, 1)
})

// Draft rule set selected in the rules tab: the registration entry targets the
// draft rule set plus its replay base (demo guided path by default). The
// selection change resets the registration state, so tests flush the watcher
// before typing a batch.
const draftRuleSet = { ruleset_id: 'org-rules', version: '2', state: 'draft', ruleset_hash: 'sha:ruleset-2', bindings: [] }
async function selectDraftRuleSet(controller: any) {
 controller.ruleSetId.value = 'org-rules'
 controller.rulesets.value = [draftRuleSet]
 controller.ruleSetVersion.value = '2'
 await nextTick()
}
const ruleSetFixtureInput = {
 fixture_id: 'org-rules-2-goal-present-write_check-positive-1', rule_id: 'goal-present', scene: 'write_check', polarity: 'positive',
 target_ref: 'field/goal', input: { project_id: 'replay-synthetic', target_version: '1', fields: { goal: '已确认研究目标' } },
 expected_status: 'PASS', review_reference: 'review-record-2026-002',
}
function rulesetReceipt(templateId: string, version: string) {
 return { data: {
  template_id: templateId, version, content_hash: 'sha:projection-content', policy_hash: 'sha:projection-policy',
  projection: { ruleset: { ruleset_id: 'org-rules', version: '2', ruleset_hash: 'sha:ruleset-2' }, replay_base: { template_id: templateId, version } },
  registered: [{ fixture_id: ruleSetFixtureInput.fixture_id, rule_id: 'goal-present', scene: 'write_check', polarity: 'positive', target_ref: 'field/goal', ruleset: { ruleset_id: 'org-rules', version: '2' } }],
  coverage: { complete: false, entries: [{ rule_id: 'goal-present', scene: 'write_check', positive_covered: true, negative_covered: false,
   declared_positive_example_refs: [], declared_negative_example_refs: [], unsatisfied_declared_example_refs: [] }], unsatisfied_declared_example_refs: [] },
 } }
}

test('rule set registration posts the draft ruleset over the demo guided base and shows the receipt gaps', async () => {
 const registrationCalls: any[] = []
 const { controller } = await fixture({
  registerRuleSetFixtures: (input: any, key: string) => { registrationCalls.push({ input, key }); return Promise.resolve(rulesetReceipt('template-demo', '1')) },
  listReplayFixtures: () => Promise.resolve({ data: [{ fixture_id: ruleSetFixtureInput.fixture_id, rule_id: 'goal-present', scene: 'write_check', polarity: 'positive', target_ref: 'field/goal', ruleset: { ruleset_id: 'org-rules', version: '2' }, template_id: 'template-demo', template_version: '1' }] }),
 })
 await selectDraftRuleSet(controller)
 // A publish base typed earlier must not survive the registration: the server
 // pins the fixtures to its own projection over the registered base.
 controller.replayTemplate.id = 'template-other'; controller.replayTemplate.version = '9'
 controller.ruleSetRegistration.reason = '为 org-rules/2 登记投影正例'
 controller.ruleSetRegistration.fixturesJSON = JSON.stringify([ruleSetFixtureInput])

 await controller.registerRuleSetFixtureBatch()

 assert.equal(registrationCalls.length, 1)
 assert.deepEqual(JSON.parse(JSON.stringify(registrationCalls[0].input)), {
  ruleset_id: 'org-rules', ruleset_version: '2', template_id: 'template-demo', template_version: '1',
  fixtures: [ruleSetFixtureInput], reason: '为 org-rules/2 登记投影正例' })
 assert.equal(registrationCalls[0].key, 'synthetic-key')
 assert.equal(controller.ruleSetCoverage.value.complete, false)
 assert.equal(controller.ruleSetCoverage.value.entries[0].negative_covered, false)
 assert.equal(controller.ruleSetCoverage.value.projection.replay_base.template_id, 'template-demo')
 assert.equal(controller.replayTemplate.id, 'template-demo')
 assert.equal(controller.replayTemplate.version, '1')
 assert.match(controller.message.value, /已登记 1 条/)
 assert.match(controller.message.value, /仍有缺口/)
 assert.match(controller.message.value, /发布底版已同步为 template-demo \/ 1/)
 assert.deepEqual(controller.replayFixtures.value.map((item: any) => item.fixture_id), [ruleSetFixtureInput.fixture_id])
 assert.equal(controller.busy.value, false)
})

test('the replay base select switches registration to a readable catalog draft version', async () => {
 const registrationCalls: any[] = []
 const { controller } = await fixture({
  registerRuleSetFixtures: (input: any, key: string) => { registrationCalls.push({ input, key }); return Promise.resolve(rulesetReceipt('org-template', '7')) },
  listReplayFixtures: () => Promise.resolve({ data: [] }),
 })
 await selectDraftRuleSet(controller)
 controller.versions.value = [{ template_id: 'org-template', version: '7', revision: 1, state: 'draft',
  definition: { name: 'Org draft', fields: [], sections: [] }, ruleset: { ruleset_id: 'ruleset-demo', version: '1' } }]
 controller.ruleSetReplayBase.value = 'org-template|7'
 controller.ruleSetRegistration.reason = '改用组织草稿底版'
 controller.ruleSetRegistration.fixturesJSON = JSON.stringify([ruleSetFixtureInput])

 await controller.registerRuleSetFixtureBatch()

 assert.equal(registrationCalls.length, 1)
 assert.equal(registrationCalls[0].input.template_id, 'org-template')
 assert.equal(registrationCalls[0].input.template_version, '7')
 assert.equal(controller.ruleSetCoverage.value.projection.replay_base.template_id, 'org-template')
 assert.equal(controller.replayTemplate.id, 'org-template')
 assert.equal(controller.replayTemplate.version, '7')
})

test('loadRuleSetCoverage reads the projection matrix through the paired ruleset query', async () => {
 const coverageCalls: any[][] = []
 const view = { data: { template_id: 'template-demo', version: '1', content_hash: 'sha:projection-content', policy_hash: 'sha:projection-policy',
  projection: { ruleset: { ruleset_id: 'org-rules', version: '2', ruleset_hash: 'sha:ruleset-2' }, replay_base: { template_id: 'template-demo', version: '1' } },
  complete: false,
  entries: [{ rule_id: 'goal-present', scene: 'preflight', positive_covered: false, negative_covered: false,
   declared_positive_example_refs: [], declared_negative_example_refs: [], unsatisfied_declared_example_refs: [] }],
  unsatisfied_declared_example_refs: [] } }
 const { controller } = await fixture({ readReplayCoverage: (...args: any[]) => { coverageCalls.push(args); return Promise.resolve(view) } })
 await selectDraftRuleSet(controller)

 await controller.loadRuleSetCoverage()

 // The readback targets the replay base's profile path with both ruleset
 // parameters; a lone parameter is a server-side 400 the panel never sends.
 // JSON round-trips the VM-realm query object before the strict compare.
 assert.deepEqual(coverageCalls.map(call => [call[0], call[1], JSON.parse(JSON.stringify(call[2]))]),
  [['template-demo', '1', { ruleset_id: 'org-rules', ruleset_version: '2' }]])
 assert.equal(controller.ruleSetCoverage.value.complete, false)
 assert.equal(controller.ruleSetCoverage.value.projection.ruleset.ruleset_id, 'org-rules')
 assert.equal(controller.busy.value, false)
})

test('rule set registration keeps 422 replay evidence and 409 idempotency conflicts distinct and readable', async () => {
 let failure: unknown = Object.defineProperty({ status: 422,
  message: '夹具 org-rules-2-goal-present-write_check-positive-1 声明 PASS 但实际求值 ISSUE。',
  error: { code: 'template_replay_required', message: '须用服务器注册且绑定当前版本的正反例完成回放。', retryable: false } }, '$httpStatus', { value: 422 })
 let calls = 0
 const { controller, events } = await fixture({
  registerRuleSetFixtures: () => { calls += 1; return Promise.reject(failure) },
  listReplayFixtures: () => Promise.resolve({ data: [] }),
 })
 await selectDraftRuleSet(controller)
 controller.ruleSetRegistration.reason = '登记理由'
 controller.ruleSetRegistration.fixturesJSON = JSON.stringify([ruleSetFixtureInput])

 await controller.registerRuleSetFixtureBatch()

 assert.match(controller.errorMessage.value, /回放核验未通过（422）/)
 assert.match(controller.errorMessage.value, /声明 PASS 但实际求值 ISSUE/)
 assert.equal(events.at(-1), controller.errorMessage.value)
 assert.equal(controller.ruleSetRegistration.fixturesJSON, JSON.stringify([ruleSetFixtureInput]))

 // A reused idempotency key over different content stays an idempotency
 // answer, never mislabeled as a version conflict.
 failure = Object.defineProperty({ status: 409, message: '同一幂等键对应不同请求。', error: { code: 'idempotency_conflict', message: '同一幂等键对应不同请求。', retryable: false } }, '$httpStatus', { value: 409 })
 await controller.registerRuleSetFixtureBatch()
 assert.match(controller.errorMessage.value, /同一幂等键/)
 assert.doesNotMatch(controller.errorMessage.value, /版本冲突/)
 assert.equal(calls, 2)
 assert.equal(controller.busy.value, false)
})
