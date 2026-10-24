import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import Module from 'node:module'

const require = createRequire(import.meta.url)
const directory = path.dirname(fileURLToPath(import.meta.url))
const ts = require('typescript')
const compiler = require('@vue/compiler-sfc')
const vue = require('vue')
const { renderToString } = require('@vue/server-renderer')
function compile(filename, source, resolve = require) {
  const module = new Module(filename)
  module.paths = Module._nodeModulePaths(directory)
  module.require = resolve
  module._compile(ts.transpileModule(source ?? fs.readFileSync(filename, 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText, filename)
  return module.exports
}
const state = compile(path.join(directory, 'projectMaterialIntake.ts'))
const fixture = { duplicate: false, denied: false, cancelled: false, uploads: [], binds: [], bound: false, placeholder: false, indexFailed: false }
const knowledge = () => ({ id: 'k-a', knowledge_base_id: 'kb-a', parse_status: 'completed', title: 'Real SFC synthetic material' })
const asset = () => ({ id: 'a-a', project_id: 'p-a', knowledge_id: 'k-a', processing_state: 'ready', asset_revision: 1 })
const api = {
  async uploadKnowledgeFile(kb, body) {
    fixture.uploads.push({ kb, body })
    if (fixture.denied) throw { status: 403, message: 'synthetic KB denial' }
    if (fixture.duplicate) throw { code: 'duplicate_file', data: knowledge() }
    return { success: true, data: knowledge() }
  },
  async getKnowledgeDetails() { return { data: knowledge() } },
  async getKnowledgeSpans() {
    return { data: { knowledge_id: 'k-a', current_attempt: 1, latest_attempt: 1, parse_status: 'completed', trace: {
      name: 'root', kind: 'root', status: 'done', children: [{ span_id: fixture.placeholder ? '' : 'real-index-span', name: 'embedding', kind: 'stage', status: fixture.indexFailed ? 'failed' : 'done', error_message: fixture.indexFailed ? 'synthetic index storage unavailable' : '', attempt: 1, knowledge_id: 'k-a' }],
    } } }
  },
  async getKnowledgeBaseById(id) { return { data: { id, name: 'Synthetic KB', type: 'document' } } },
  async listKnowledgeBases() { return { data: [{ id: 'kb-a', name: 'Synthetic KB', type: 'document' }] } },
}
const workspace = {
  async listAssets() { return { data: fixture.bound ? [asset()] : [] } },
  async bindAsset(...args) { fixture.binds.push(args); fixture.bound = true; return { data: asset() } },
  async retrieveSources() { return { data: [] } },
}
const ports = compile(path.join(directory, 'projectMaterialsApi.ts'), undefined, id => {
  if (id === '@/api/knowledge-base') return api
  if (id === '@/api/lingdoc/workspace') return workspace
  if (id === '@/api/lingdoc/candidateAdoption') return { newIdempotencyKey: () => 'binding-key' }
  return require(id)
}).projectMaterialPorts
const filename = path.join(directory, 'ProjectMaterialIntakePanel.vue')
const parsed = compiler.parse(fs.readFileSync(filename, 'utf8'))
assert.deepEqual(parsed.errors, [])
const script = compiler.compileScript(parsed.descriptor, { id: 'project-material-test' })
const template = compiler.compileTemplate({ filename, id: 'project-material-test', source: parsed.descriptor.template.content, compilerOptions: { bindingMetadata: script.bindings }, isTS: true })
assert.deepEqual(template.errors, [])
const store = { async open(options) {
  if (fixture.cancelled) throw Error('cancelled by user')
  return { mode: 'file', files: options.files, processConfig: { enable_multimodel: false } }
} }
const component = compile(filename, script.content, id => {
  if (id === '@/api/knowledge-base') return api
  if (id === '@/stores/uploadConfirm') return { useUploadConfirmStore: () => store }
  if (id === '@/utils') return { kbFileTypeVerification: () => false }
  if (id === './projectMaterialIntake') return state
  if (id === './projectMaterialsApi') return { projectMaterialPorts: ports }
  return require(id)
}).default
component.render = compile(filename + '.template', template.code).render
const file = new File(['synthetic'], 'material.txt')
async function render(action) {
  Object.assign(fixture, { duplicate: false, denied: false, cancelled: false, uploads: [], binds: [], bound: false, placeholder: false, indexFailed: false })
  const values = new Map()
  globalThis.window = { localStorage: { getItem: key => values.get(key) || null, setItem: (key, value) => values.set(key, value) } }
  let controller
  const events = []
  const wrapper = { ...component, setup(props, context) {
    controller = vue.proxyRefs(component.setup(props, context))
    return Promise.resolve(action?.(controller)).then(() => controller)
  } }
  const app = vue.createSSRApp(wrapper, { projectId: 'p-a', projectName: '目标合成课题', identity: 'tenant-a:user-a', onBound: () => events.push('bound') })
  const html = await renderToString(app)
  delete globalThis.window
  return { html, controller, events, values }
}
test('real panel compiles and labels the target project and separate upload/binding/search stages', async () => {
  const { html } = await render()
  assert.match(html, /目标合成课题/)
  assert.match(html, /上传不会自动扩大资料权限/)
  assert.match(html, /不是服务端的正式绑定列表/)
})
test('actual file handler and API adapter retain multipart upload intent without implicitly binding', async () => {
  const { html } = await render(async c => {
    c.kbId = 'kb-a'
    await c.chooseFile({ target: { files: [file], value: 'picked' } })
  })
  assert.equal(fixture.uploads.length, 1)
  assert.equal(fixture.uploads[0].kb, 'kb-a')
  assert.deepEqual(JSON.parse(fixture.uploads[0].body.metadata), { lingdoc_upload_project_id: 'p-a', lingdoc_upload_origin: 'workspace' })
  assert.equal(fixture.binds.length, 0)
  assert.match(html, /文件已上传/)
  assert.match(html, /处理完成（当前索引阶段已完成）/)
})
test('real duplicate adapter recovers the existing knowledge instead of reporting a fresh upload', async () => {
  const { html } = await render(async c => { fixture.duplicate = true; await c.model.upload('kb-a', file) })
  assert.match(html, /复用服务端已有材料/)
  assert.equal(fixture.binds.length, 0)
})
test('synthetic done placeholder renders uncertainty and disables binding; actual handler refuses a forced click', async () => {
  const { html } = await render(async c => {
    fixture.placeholder = true
    await c.model.upload('kb-a', file)
    await c.bind(c.model.rows[0])
  })
  assert.match(html, /缺少真实的当前索引记录/)
  assert(!html.includes('处理完成（当前索引阶段已完成）'))
  assert.equal(fixture.binds.length, 0)
})
test('actual binding uses the original API and emits only after current project readback', async () => {
  const { html, events } = await render(async c => {
    await c.model.upload('kb-a', file)
    await c.bind(c.model.rows[0])
  })
  assert.deepEqual(fixture.binds, [['p-a', 'k-a', 'binding-key']])
  assert.deepEqual(events, ['bound'])
  assert.match(html, /已读回当前项目绑定/)
})

test('real panel exposes the index error when parse_status is completed and refuses binding', async () => {
  const { html } = await render(async c => {
    fixture.indexFailed = true
    await c.model.upload('kb-a', file)
    await c.bind(c.model.rows[0])
  })
  assert.match(html, /解析完成，但索引失败/)
  assert.match(html, /synthetic index storage unavailable/)
  assert(!html.includes('处理完成（当前索引阶段已完成）'))
  assert.equal(fixture.binds.length, 0)
})
test('KB denial is visible and does not render a material row or successful binding', async () => {
  const { html, controller } = await render(async c => { fixture.denied = true; await c.model.upload('kb-a', file) })
  assert.match(html, /synthetic KB denial/)
  assert.equal(controller.model.rows.length, 0)
  assert.equal(fixture.binds.length, 0)
})
test('upload configuration dialog cancellation performs no upload and is not a failure', async () => {
  const { controller } = await render(async c => {
    fixture.cancelled = true; c.kbId = 'kb-a'
    await c.chooseFile({ target: { files: [file], value: 'picked' } })
  })
  assert.equal(fixture.uploads.length, 0)
  assert.equal(controller.model.uploadError, '')
})

