import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import { compileScript, parse } from '@vue/compiler-sfc'
import { createSSRApp } from 'vue'
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
async function fixture(api: Record<string, any> = {}, canManageCatalog = true) {
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
  chapters: [], sources: [], canManageCatalog,
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

function templateVersion(revision = 1) {
 return {
  template_id: 'synthetic-template', version: '1', revision, state: 'draft',
  definition: { name: 'Synthetic template', fields: [], sections: [], terms: [], required_fields: [] },
  ruleset: { ruleset_id: 'rules', version: '1' }, binding_policies: [],
  source_type: 'project_copy', source_refs: [], scope: 'university', curator: 'curator',
 }
}

test('template ID filter searches only returned profiles and distinguishes empty, filtered and failed states', async () => {
 const { controller } = await fixture()
 controller.profiles.value = [{ template_id: 'alpha-01' }, { template_id: 'beta-02' }]
 controller.templateIdFilter.value = 'BETA'
 assert.deepEqual(controller.visibleProfiles.value.map((item: any) => item.template_id), ['beta-02'])
 controller.templateIdFilter.value = 'missing'
 assert.match(controller.catalogNotice.value, /筛选下没有模板/)
 controller.catalogError.value = '网络中断'
 assert.match(controller.catalogNotice.value, /读取失败/)
 controller.catalogError.value = ''; controller.templateIdFilter.value = ''; controller.profiles.value = []
 assert.match(controller.catalogNotice.value, /没有模板档案/)
})

test('ordinary project users never request audit history', async () => {
 let requests = 0
 const { controller } = await fixture({ listTemplateAuditHistory: async () => { requests++; return { data: { events: [] } } } }, false)
 controller.profileId.value = 'synthetic-template'
 await controller.loadAudit()
 assert.equal(requests, 0)
 assert.equal(controller.auditEvents.value.length, 0)
})

test('audit denial removes previously visible management data and rechecking can restore access', async () => {
 let attempt = 0
 const { controller } = await fixture({ listTemplateAuditHistory: async () => {
  attempt++
  if (attempt === 2) throw { status: 403 }
  return { data: { events: [{ id: attempt, action: 'publish' }], next_after_id: 12 } }
 } })
 controller.profileId.value = 'synthetic-template'
 controller.profiles.value = [{ template_id: 'sensitive-draft' }]
 controller.versions.value = [templateVersion()]
 await controller.loadAudit()
 assert.equal(controller.auditEvents.value.length, 1)
 assert.equal(controller.auditNextId.value, 12)
 await controller.loadAudit(true)
 assert.equal(controller.auditEvents.value.length, 0)
 assert.equal(controller.profiles.value.length, 0)
 assert.equal(controller.versions.value.length, 0)
 assert.equal(controller.managerAccess.value, false)
 assert.match(controller.auditError.value, /权限不足/)
 await controller.loadAudit()
 assert.equal(controller.managerAccess.value, true)
 assert.equal(controller.auditEvents.value.length, 1)
 assert.match(controller.versionError.value, /重新读取版本/)
})

test('late audit response cannot expose a previous profile after selection changes', async () => {
 const pending = deferred<any>()
 const { controller } = await fixture({ listTemplateAuditHistory: () => pending.promise })
 controller.profileId.value = 'profile-a'
 const request = controller.loadAudit()
 controller.profileId.value = 'profile-b'
 pending.resolve({ data: { events: [{ id: 1, action: 'old-profile' }] } })
 await request
 assert.equal(controller.auditEvents.value.length, 0)
})

test('successful draft write remains successful when version readback fails', async () => {
 const saved = templateVersion(2)
 const { controller } = await fixture({
  saveTemplateDraft: async () => ({ data: saved }),
  listTemplateVersions: async () => { throw { status: 503 } },
  listTemplateAuditHistory: async () => ({ data: { events: [] } }),
  listTemplateProfiles: async () => ({ data: [{ template_id: 'synthetic-template' }] }),
 })
 controller.profileId.value = 'synthetic-template'; controller.versions.value = [templateVersion()]; controller.selectedVersionId.value = '1'
 controller.editTemplate(false)
 controller.templateDraft.value.input.reason = 'approved edit'
 await controller.saveDraft()
 assert.match(controller.message.value, /草稿已保存/)
 assert.match(controller.readbackNotice.value, /操作已成功.*版本读取未完成/)
 assert.equal(controller.errorMessage.value, '')
 assert.equal(controller.templateDraft.value.input.expected_revision, 2)
 assert.ok(controller.versionError.value)
})

test('version conflict preserves the unsaved draft for comparison', async () => {
 const { controller } = await fixture({ saveTemplateDraft: async () => { throw { status: 409 } } })
 controller.profileId.value = 'synthetic-template'; controller.versions.value = [templateVersion()]; controller.selectedVersionId.value = '1'
 controller.editTemplate(false)
 controller.templateDraft.value.input.reason = 'keep this edit'
 await controller.saveDraft()
 assert.match(controller.errorMessage.value, /版本冲突/)
 assert.equal(controller.templateDraft.value.input.reason, 'keep this edit')
 assert.equal(controller.templateDraft.value.input.expected_revision, 1)
})


test('source authorization denial preserves the draft and shows the server explanation', async () => {
 const { controller } = await fixture({ saveTemplateDraft: async () => {
  throw { status: 403, error: { code: 'source_access_denied', message: '来源已不可用或无当前授权。' } }
 } })
 controller.profileId.value = 'synthetic-template'; controller.versions.value = [templateVersion()]; controller.selectedVersionId.value = '1'
 controller.editTemplate(false)
 controller.templateDraft.value.input.reason = 'retain my changes'
 await controller.saveDraft()
 assert.equal(controller.managerAccess.value, true)
 assert.match(controller.errorMessage.value, /来源已不可用/)
 assert.equal(controller.templateDraft.value.input.reason, 'retain my changes')
})

test('replay and state errors retain the server explanation rather than a generic failure', async () => {
 const { controller } = await fixture()
 await controller.perform(async () => { throw { status: 422, error: { code: 'template_replay_required', message: '须完成服务器注册样例回放。' } } })
 assert.match(controller.errorMessage.value, /样例回放/)
 await controller.perform(async () => { throw { status: 409, error: { code: 'invalid_state', message: '当前版本状态不允许此操作。' } } })
 assert.match(controller.errorMessage.value, /状态不允许/)
})


test('an unavailable historical version is not silently replaced by another', async () => {
 const replacement = { ...templateVersion(), version: '2' }
 const { controller } = await fixture({ listTemplateVersions: async () => ({ data: [replacement] }) })
 controller.profileId.value = 'synthetic-template'; controller.selectedVersionId.value = '1'
 await controller.refreshVersions()
 assert.equal(controller.selectedVersionId.value, '')
 assert.match(controller.versionNotice.value, /历史版本 1 当前不可用/)
 assert.equal(controller.selectedVersion.value, undefined)
})


test('403 revocation invalidates older catalog and profile reads before they return', async () => {
 const pendingProfiles = deferred<any>(), pendingVersions = deferred<any>()
 const { controller } = await fixture({
  listTemplateProfiles: () => pendingProfiles.promise,
  listTemplateVersions: () => pendingVersions.promise,
  listTemplateAuditHistory: async () => { throw { status: 403, error: { code: 'forbidden' } } },
 })
 controller.profileId.value = 'current-profile'
 const catalogRequest = controller.loadCatalog()
 const profileRequest = controller.chooseProfile('draft-profile')
 await controller.loadAudit()
 assert.equal(controller.managerAccess.value, false)
 assert.equal(controller.catalogLoading.value, false)
 pendingProfiles.resolve({ data: [{ template_id: 'private-draft', name: 'Sensitive draft' }] })
 pendingVersions.resolve({ data: [{ ...templateVersion(), template_id: 'draft-profile' }] })
 await Promise.all([catalogRequest, profileRequest])
 assert.equal(controller.profiles.value.length, 0)
 assert.equal(controller.versions.value.length, 0)
 assert.equal(controller.selectedVersionId.value, '')
 assert.equal(controller.catalogLoading.value, false)
 assert.match(controller.errorMessage.value, /权限不足/)
})

test('403 revocation prevents an older version refresh from restoring draft details', async () => {
 const pendingVersions = deferred<any>()
 const { controller } = await fixture({
  listTemplateVersions: () => pendingVersions.promise,
  listTemplateAuditHistory: async () => { throw { status: 403, error: { code: 'forbidden' } } },
 })
 controller.profileId.value = 'synthetic-template'
 const refreshing = controller.refreshVersions()
 await controller.loadAudit()
 pendingVersions.resolve({ data: [templateVersion()] })
 await refreshing
 assert.equal(controller.versions.value.length, 0)
 assert.equal(controller.selectedVersionId.value, '')
 assert.equal(controller.managerAccess.value, false)
})


test('403 from protected rule definitions invalidates all concurrent initialization results', async () => {
 const pendingProfiles = deferred<any>(), pendingDefinitions = deferred<any>(), pendingFixtures = deferred<any>()
 const { controller } = await fixture({
  listTemplateProfiles: () => pendingProfiles.promise,
  listRuleSuggestions: async () => ({ data: [] }),
  listRuleDefinitions: () => pendingDefinitions.promise,
  listReplayFixtures: () => pendingFixtures.promise,
 })
 const initializing = controller.perform(controller.loadInitialData)
 pendingDefinitions.reject({ status: 403, error: { code: 'forbidden' } })
 await new Promise<void>(resolve => setImmediate(resolve))
 assert.equal(controller.managerAccess.value, false)
 pendingProfiles.resolve({ data: [{ template_id: 'private-draft' }] })
 pendingFixtures.resolve({ data: [{ fixture_id: 'private-fixture' }] })
 await initializing
 assert.equal(controller.profiles.value.length, 0)
 assert.equal(controller.replayFixtures.value.length, 0)
 assert.equal(controller.definitions.value.length, 0)
 assert.match(controller.errorMessage.value, /权限不足/)
})

test('403 from protected replay fixtures invalidates all concurrent initialization results', async () => {
 const pendingProfiles = deferred<any>(), pendingDefinitions = deferred<any>(), pendingFixtures = deferred<any>()
 const { controller } = await fixture({
  listTemplateProfiles: () => pendingProfiles.promise,
  listRuleSuggestions: async () => ({ data: [] }),
  listRuleDefinitions: () => pendingDefinitions.promise,
  listReplayFixtures: () => pendingFixtures.promise,
 })
 const initializing = controller.perform(controller.loadInitialData)
 pendingFixtures.reject({ status: 403, error: { code: 'forbidden' } })
 await new Promise<void>(resolve => setImmediate(resolve))
 assert.equal(controller.managerAccess.value, false)
 pendingProfiles.resolve({ data: [{ template_id: 'private-draft' }] })
 pendingDefinitions.resolve({ data: [{ definition_id: 'private-rule' }] })
 await initializing
 assert.equal(controller.profiles.value.length, 0)
 assert.equal(controller.definitions.value.length, 0)
 assert.equal(controller.replayFixtures.value.length, 0)
 assert.match(controller.errorMessage.value, /权限不足/)
})

test('404 reading a version clears old sensitive details without claiming deletion', async () => {
 const { controller } = await fixture({ listTemplateVersions: async () => { throw { status: 404, error: { code: 'not_found', message: '资源不存在或不可访问。' } } } })
 controller.profileId.value = 'synthetic-template'; controller.profiles.value = [{ template_id: 'synthetic-template' }]
 controller.versions.value = [templateVersion()]; controller.selectedVersionId.value = '1'; controller.editTemplate(false)
 await assert.rejects(controller.refreshVersions())
 assert.equal(controller.profiles.value.length, 0)
 assert.equal(controller.versions.value.length, 0)
 assert.equal(controller.selectedVersionId.value, '')
 assert.equal(controller.managerAccess.value, false)
 assert.equal(Object.keys(controller.draftCache).length, 0)
 assert.match(controller.managerAccessNotice.value, /不存在或当前不可访问/)
 assert.doesNotMatch(controller.managerAccessNotice.value, /已删除/)
})

test('404 reading audit clears cached sensitive details and remains ambiguous about absence or access', async () => {
 const { controller } = await fixture({ listTemplateAuditHistory: async () => { throw { status: 404, error: { code: 'not_found' } } } })
 controller.profileId.value = 'synthetic-template'; controller.profiles.value = [{ template_id: 'synthetic-template' }]
 controller.versions.value = [templateVersion()]; controller.selectedVersionId.value = '1'
 controller.auditEvents.value = [{ id: 1, action: 'private' }]
 await controller.loadAudit()
 assert.equal(controller.profiles.value.length, 0)
 assert.equal(controller.versions.value.length, 0)
 assert.equal(controller.auditEvents.value.length, 0)
 assert.equal(controller.managerAccess.value, false)
 assert.match(controller.managerAccessNotice.value, /不存在或当前不可访问/)
})

test('404 also clears previously visible versions for an ordinary project user', async () => {
 const { controller } = await fixture({ listTemplateVersions: async () => { throw { status: 404, error: { code: 'not_found' } } } }, false)
 controller.profileId.value = 'synthetic-template'; controller.versions.value = [templateVersion()]; controller.selectedVersionId.value = '1'
 await assert.rejects(controller.refreshVersions())
 assert.equal(controller.versions.value.length, 0)
 assert.equal(controller.selectedVersionId.value, '')
 assert.match(controller.managerAccessNotice.value, /不存在或当前不可访问/)
})


test('ordinary user can recover after 404 by re-reading the visible directory and selected version', async () => {
 let versionReads = 0
 const published = { ...templateVersion(), state: 'published' }
 const { controller } = await fixture({
  listTemplateProfiles: async () => ({ data: [{ template_id: 'synthetic-template' }] }),
  listTemplateVersions: async () => {
   if (++versionReads === 1) throw { status: 404, error: { code: 'not_found' } }
   return { data: [published] }
  },
 }, false)
 controller.profileId.value = 'synthetic-template'; controller.versions.value = [published]; controller.selectedVersionId.value = '1'
 await assert.rejects(controller.refreshVersions())
 assert.equal(controller.managerDenied.value, true)
 await controller.retryOrdinaryCatalogAccess()
 assert.equal(controller.managerDenied.value, false)
 assert.equal(controller.managerAccessNotice.value, '')
 assert.equal(controller.versionError.value, '')
 assert.equal(controller.selectedVersion.value.version, '1')
})

test('ordinary user retry does not claim recovery when version remains inaccessible', async () => {
 const { controller } = await fixture({
  listTemplateProfiles: async () => ({ data: [{ template_id: 'synthetic-template' }] }),
  listTemplateVersions: async () => { throw { status: 404, error: { code: 'not_found' } } },
 }, false)
 controller.profileId.value = 'synthetic-template'; controller.versions.value = [templateVersion()]; controller.selectedVersionId.value = '1'
 await assert.rejects(controller.refreshVersions())
 await controller.retryOrdinaryCatalogAccess()
 assert.equal(controller.managerDenied.value, true)
 assert.equal(controller.versions.value.length, 0)
 assert.equal(controller.selectedVersion.value, undefined)
 assert.match(controller.managerAccessNotice.value, /不存在或当前不可访问/)
})
