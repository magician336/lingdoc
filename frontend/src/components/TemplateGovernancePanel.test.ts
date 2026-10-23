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
