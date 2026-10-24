import { createRequire } from 'node:module'
import Module from 'node:module'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'
import test from 'node:test'

const require = createRequire(import.meta.url)
const here = path.dirname(fileURLToPath(import.meta.url))
const ts = require('typescript'), compiler = require('@vue/compiler-sfc'), vue = require('vue')
function moduleOf(filename, source, dependencies = {}) {
  const m = new Module(filename)
  m.paths = Module._nodeModulePaths(here)
  m.require = id => id in dependencies ? dependencies[id] : require(id)
  m._compile(ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText, filename)
  return m.exports
}
function sfc(name, dependencies) {
  const filename = path.join(here, name)
  let source = fs.readFileSync(filename, 'utf8')
  if (process.env.R3_NEGATIVE === 'wrong-project' && name === 'ProjectContextPackagePanel.vue') source = source.replace('projectId: id, intent, client', "projectId: 'wrong-project', intent, client")
  const { descriptor, errors } = compiler.parse(source)
  assert.deepEqual(errors, [])
  const script = compiler.compileScript(descriptor, { id: name })
  const template = compiler.compileTemplate({ filename, id: name, source: descriptor.template.content, compilerOptions: { bindingMetadata: script.bindings }, isTS: true })
  assert.deepEqual(template.errors, [])
  const component = moduleOf(filename, script.content, dependencies).default
  component.render = moduleOf(filename + '.render', template.code).render
  return component
}
// Real Vue renderer, not SSR: assertion failures cannot be swallowed by async setup.
function node(tag, text = '') {
  return { tag, tagName: tag.toUpperCase(), text, children: [], props: {}, parent: null,
    listeners: {}, addEventListener(name, fn) { this.listeners[name] = fn },
    removeEventListener(name) { delete this.listeners[name] },
    get options() { return this.children.filter(n => n.tag === 'option') } }
}
const renderer = vue.createRenderer({
  createElement: tag => node(tag), createText: text => node('#text', text), createComment: text => node('#comment', text),
  setText(n, text) { n.text = text }, setElementText(n, text) { n.text = text; n.children = [] },
  parentNode: n => n.parent, nextSibling: n => n.parent?.children[n.parent.children.indexOf(n) + 1] ?? null,
  insert(n, parent, anchor = null) { if (n.parent) n.parent.children.splice(n.parent.children.indexOf(n), 1); n.parent = parent; const i = anchor ? parent.children.indexOf(anchor) : -1; if (i < 0) parent.children.push(n); else parent.children.splice(i, 0, n) },
  remove(n) { if (n.parent) n.parent.children.splice(n.parent.children.indexOf(n), 1); n.parent = null },
  patchProp(n, key, _old, value) { n.props[key] = value; if (key === 'value') n.value = value },
})
const textOf = n => n.tag === '#comment' ? '' : n.text + n.children.map(textOf).join(' ')
function all(n, predicate) { return [...(predicate(n) ? [n] : []), ...n.children.flatMap(c => all(c, predicate))] }
async function flush() { for (let i = 0; i < 12; i++) { await Promise.resolve(); await vue.nextTick() } }
async function click(root, label) {
  const button = all(root, n => n.tag === 'button' && textOf(n).includes(label))[0]
  assert.ok(button, `missing button ${label}`)
  assert.ok(!button.props.disabled, `disabled button ${label}`)
  await button.props.onClick(); await flush()
}
const project = id => ({ id, name: `Project ${id}`, status: 'active', project_version: 7, spec_revision: 2, current_context_revision: 3, members: [], template_id: 'demo', template_version: '1', spec: {} })
const card = id => ({ id, project_id: 'a', version: 2, status: 'CONFIRMED', anchor_refs: [], source_ids: [], basis: {} })
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
function harness(mount = true) {
  const calls = { project: [], preview: [], cards: [] }
  const fixture = { cards: [card('card-a')], failure: null, projectFailure: null, cardFailure: null, waitProject: null, waitPreview: null, waitCards: null, version: 7, malformedProject: false }
  const api = {
    async getProject(id) { calls.project.push(id); if (fixture.projectFailure) throw fixture.projectFailure; if (fixture.waitProject) return fixture.waitProject.promise; return { data: { ...project(fixture.malformedProject ? 'wrong' : id), project_version: fixture.version } } },
    async listEvidenceCards(id) { calls.cards.push(id); if (fixture.cardFailure) throw fixture.cardFailure; if (fixture.waitCards) return fixture.waitCards.promise; return { data: fixture.cards } },
    async previewAgentContext(id, input) {
      calls.preview.push({ id, input }); if (fixture.failure) throw fixture.failure; if (fixture.waitPreview) return fixture.waitPreview.promise
      return { data: { project_id: id, project_version: input.expected_project_version, spec_revision: 2, context_revision: input.expected_context_revision,
        template_id: 'demo', template_version: '1', card_refs: input.card_refs, source_refs: [], selection_reasons: ['服务端原文理由'], authorization_state: 'allowed', output_constraints: ['候选而非科学证明'], correlation_id: 'server-' + id } }
    },
  }
  const facadePath = path.resolve(here, '../../api/lingdoc/agentContext.ts')
  const facade = moduleOf(facadePath, fs.readFileSync(facadePath, 'utf8'), { './agentApi': api })
  const child = sfc('ContextPackagePanel.vue', { '@/api/lingdoc/agentContext': facade })
  const host = sfc('ProjectContextPackagePanel.vue', { './ContextPackagePanel.vue': child, '@/api/lingdoc/workspace': api, '@/api/lingdoc/agentApi': api, '@/api/lingdoc/agentContext': facade })
  const props = vue.reactive({ project: project('a'), identity: 'tenant:user', shown: true })
  const root = node('root')
  const app = renderer.createApp({ setup: () => () => props.shown ? vue.h(host, { project: props.project, identity: props.identity, key: props.identity + ':' + props.project.id }) : null })
  if (mount) app.mount(root)
  return { calls, fixture, props, root, app, host, text: () => textOf(root) }
}

test('mount reads the current baseline once then uses the original panel and API', async () => {
  const h = harness(); await flush()
  assert.deepEqual(h.calls.project, ['a'])
  assert.deepEqual(h.calls.cards, ['a'])
  assert.equal(h.calls.preview.length, 1)
  assert.deepEqual(h.calls.preview[0], { id: 'a', input: { expected_project_version: 7, expected_context_revision: 3, expected_version: 0, source_ids: [], card_refs: [], sensitive_context_confirmed: false } })
  assert.match(h.text(), /服务端原文理由/)
  assert.match(h.text(), /server-a/)
  assert.match(h.text(), /服务端这一轮没有带任何证据卡/)
  h.app.unmount()
})

test('project read failure is an error, not an empty successful context; explicit retry recovers', async () => {
  const h = harness(); h.fixture.projectFailure = { status: 403, message: 'forbidden' }
  await flush()
  // The first read is already dispatched; force the same action as the refresh button.
  await click(h.root, '刷新上下文包')
  assert.match(h.text(), /无法读取当前课题/)
  assert.doesNotMatch(h.text(), /server-a/)
  const count = h.calls.preview.length
  h.fixture.projectFailure = null
  await click(h.root, '刷新上下文包')
  assert.equal(h.calls.preview.length, count + 1)
  assert.match(h.text(), /server-a/)
  h.app.unmount()
})

test('in-flight project switch never mounts or renders the previous project response', async () => {
  const h = harness(); await flush()
  const pending = deferred(); h.fixture.waitProject = pending
  const action = click(h.root, '刷新上下文包'); await flush()
  assert.match(h.text(), /正在读取当前课题/)
  assert.doesNotMatch(h.text(), /server-a/)
  h.fixture.waitProject = null; h.fixture.cards = [card('card-b')]; h.props.project = project('b'); await flush()
  pending.resolve({ data: project('a') }); await action; await flush()
  assert.match(h.text(), /server-b/)
  assert.doesNotMatch(h.text(), /server-a/)
  assert.equal(h.calls.preview.filter(c => c.id === 'b').length, 1)
  h.app.unmount()
})

async function addFirstCard(h) {
  const select = all(h.root, n => n.tag === 'select')[0]
  assert.ok(select?.options.length, 'real candidate select must have options')
  select.options[0].selected = true
  select.listeners.change(); await flush()
  await click(h.root, '加入这张卡')
}

test('actual selection handlers refresh server facts; refresh preserves exact refs but rereads baseline', async () => {
  const h = harness(); await flush(); await addFirstCard(h)
  assert.equal(h.calls.preview.length, 2)
  assert.equal(h.calls.cards.length, 2)
  assert.deepEqual(h.calls.preview[1].input.card_refs, [{ id: 'card-a', version: 2 }])
  assert.ok(all(h.root, n => n.props['data-fact-card'] === 'card-a').length)
  h.fixture.version = 8
  await click(h.root, '刷新上下文包')
  assert.equal(h.calls.project.length, 2)
  assert.equal(h.calls.preview.length, 3)
  assert.equal(h.calls.cards.length, 3)
  assert.equal(h.calls.preview[2].input.expected_project_version, 8)
  assert.deepEqual(h.calls.preview[2].input.card_refs, [{ id: 'card-a', version: 2 }])
  await click(h.root, '移除这张卡')
  assert.deepEqual(h.calls.preview.at(-1).input.card_refs, [])
  assert.equal(all(h.root, n => n.props['data-fact-card'] === 'card-a').length, 0)
  h.app.unmount()
})

for (const status of [403, 404, 409, 503]) test(`preview ${status} clears old facts and does not auto-retry`, async () => {
  const h = harness(); await flush(); await addFirstCard(h)
  h.fixture.failure = { status, message: 'synthetic denial', error: { code: 'version_conflict' } }
  await click(h.root, '刷新上下文包')
  assert.match(h.text(), status === 409 ? /版本基线已变/ : /预览失败/)
  assert.doesNotMatch(h.text(), /server-a/)
  assert.equal(all(h.root, n => n.props['data-fact-card']).length, 0)
  assert.equal(h.calls.preview.length, 3)
  await flush(); assert.equal(h.calls.preview.length, 3)
  h.fixture.failure = null
  await click(h.root, '刷新上下文包')
  assert.match(h.text(), /server-a/)
  h.app.unmount()
})

test('card catalog failure is explicitly unknown, not a successful empty list', async () => {
  const h = harness(); await flush(); await addFirstCard(h)
  h.fixture.cardFailure = Error('catalog unavailable')
  await click(h.root, '刷新上下文包')
  assert.match(h.text(), /证据卡清单暂不可读/)
  assert.match(h.text(), /状态未知（清单读不到）/)
  assert.match(h.text(), /server-a/)
  h.app.unmount()
})

test('late preview and candidate response cannot refill after identity switch', async () => {
  const h = harness(); await flush()
  const oldCards = deferred(), oldPreview = deferred()
  h.fixture.waitCards = oldCards; h.fixture.waitPreview = oldPreview
  await click(h.root, '刷新上下文包')
  assert.match(h.text(), /正在预览/)
  assert.doesNotMatch(h.text(), /server-a/)
  h.fixture.waitCards = null; h.fixture.waitPreview = null; h.fixture.cards = []
  h.props.identity = 'tenant:other-user'; h.props.project = project('b'); await flush()
  oldCards.resolve({ data: [card('old-secret-card')] })
  oldPreview.resolve({ data: { project_id: 'a', source_refs: [], card_refs: [], selection_reasons: ['old-secret'], output_constraints: [], correlation_id: 'old-secret' } })
  await flush()
  assert.match(h.text(), /server-b/)
  assert.match(h.text(), /尚无可加入的证据卡/)
  assert.doesNotMatch(h.text(), /old-secret/)
  h.app.unmount()
})

test('reopen mounts exactly once and starts a fresh local selection without persistent cache', async () => {
  const h = harness(); await flush(); await addFirstCard(h)
  h.props.shown = false; await flush()
  h.props.shown = true; await flush()
  assert.equal(h.calls.project.length, 2)
  assert.equal(h.calls.preview.length, 3)
  assert.equal(h.calls.cards.length, 3)
  assert.deepEqual(h.calls.preview.at(-1).input.card_refs, [])
  await flush(); assert.equal(h.calls.preview.length, 3)
  h.app.unmount()
})

test('mismatched project readback refuses to mount the preview', async () => {
  const h = harness(); await flush()
  h.fixture.malformedProject = true; const count = h.calls.preview.length
  await click(h.root, '刷新上下文包')
  assert.match(h.text(), /无法读取当前课题/)
  assert.equal(h.calls.preview.length, count)
  assert.doesNotMatch(h.text(), /server-a/)
  h.app.unmount()
})

test('Workspace real navigation/render seam opens the context host and preserves other panel entries', async () => {
  const h = harness(false)
  const filename = path.join(here, 'Workspace.vue'), source = fs.readFileSync(filename, 'utf8')
  const { descriptor } = compiler.parse(source)
  const script = compiler.compileScript(descriptor, { id: 'workspace-context-integration' })
  // Select actual template elements by AST location, not a synthetic replacement UI.
  const tree = require('@vue/compiler-dom').parse(descriptor.template.content)
  function find(elements, className) {
    for (const n of elements) {
      if (n.type === 1 && n.props.some(p => p.type === 6 && p.name === 'class' && p.value?.content.split(' ').includes(className))) return n.loc.source
      const nested = n.children && find(n.children, className); if (nested) return nested
    }
  }
  const nav = find(tree.children, 'wb-rail-entries'), drawer = find(tree.children, 'full-workflow-backdrop')
  assert.ok(nav && drawer)
  const template = compiler.compileTemplate({ filename, id: 'workspace-context-integration', source: nav + drawer, compilerOptions: { bindingMetadata: script.bindings }, isTS: true })
  assert.deepEqual(template.errors, [])
  const projectRef = vue.ref(project('a')), panel = vue.ref(null)
  const busy = vue.ref(false), dirty = vue.ref(false)
  const context = { project: projectRef, fullPanel: panel, fullPanelBusy: busy, fullPanelDirty: dirty, fullDrawerRef: vue.ref(null), chapter: vue.ref({ id: 'chapter' }),
    fullPanelLabel: vue.computed(() => panel.value === 'context' ? '上下文包' : String(panel.value)), materialIdentity: 'tenant:user', auth: { currentUserId: 'user' },
    fullReadonly: false, generationEvidenceRefs: [], assets: [], chapters: [], sources: [], restricted: false, currentCheckReceipt: '', commentSelection: undefined,
    specOpen: false, templateOpen: false, openInspector() {}, refreshFullWorkflowProject() {}, useGenerationEvidence() {}, locateComment() {} }
  const fnText = source.slice(source.indexOf('function closeFullPanel()'), source.indexOf('async function refreshFullWorkflowProject()'))
  const helpers = { ...context, editorRef: vue.ref(null), commentSelection: vue.ref(null), workingCopyDiffersFromFormal: vue.ref(false), chapterChanged: vue.ref(false),
    nextTick: vue.nextTick, document: { activeElement: null }, HTMLElement: class {}, window: { confirm: () => true } }
  const js = ts.transpileModule('let fullReturnFocus=null;\n' + fnText, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText
  Object.assign(context, new Function(...Object.keys(helpers), js + ';return {openFullPanel,closeFullPanel}')(...Object.values(helpers)))
  const stub = { render: () => vue.h('p', 'existing panel') }
  Object.assign(context, { ProjectContextPackagePanel: h.host, ResearchPanel: stub, ImpactOpsPanel: stub, RuntimeOpsPanel: stub, TemplateGovernancePanel: stub, ProjectCollaborationPanel: stub, ChapterReviewPanel: stub })
  const component = { setup: () => context, render: moduleOf(filename + '.test.render', template.code).render }
  const app = renderer.createApp(component); app.mount(h.root); await flush()
  assert.equal(h.calls.preview.length, 0, 'closed host must not request context')
  for (const label of ['调研与证据', '上传与项目资料', '正式变更与运行', '运行观测与试用', '模板目录与规则', '章节评审与批注']) assert.match(h.text(), new RegExp(label))
  await click(h.root, '上下文包 →')
  assert.equal(panel.value, 'context')
  assert.equal(h.calls.preview.length, 1)
  assert.match(h.text(), /server-a/)
  await click(h.root, '上下文包 →')
  assert.equal(h.calls.preview.length, 1, 'opening the same panel does not remount')
  await click(h.root, '前往调研与证据')
  assert.equal(panel.value, 'research')
  await click(h.root, '上下文包 →')
  assert.equal(h.calls.preview.length, 2)
  projectRef.value = project('b'); await flush()
  assert.match(h.text(), /server-b/)
  assert.doesNotMatch(h.text(), /server-a/)
  await click(h.root, '返回文稿')
  assert.equal(panel.value, null)
  projectRef.value = null; await flush()
  const entry = all(h.root, n => n.tag === 'button' && textOf(n).includes('上下文包 →'))[0]
  assert.equal(entry.props.disabled, true)
  context.openFullPanel('context'); assert.equal(panel.value, null)
  app.unmount()
})
