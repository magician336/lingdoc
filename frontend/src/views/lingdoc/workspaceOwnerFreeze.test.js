import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'

// The Owner-only entries of the real workspace, mounted.
//
// The freeze is a fact the server owns, and the workspace's job is to show it:
// the entry is greyed out and the reason is on the page. So the assertion runs
// on the real component -- the real template, the real composable wiring --
// and not on a copy of it.
//
// The file is a test host: the component under test is mounted unchanged, the
// network is a stub, and the only thing this file decides is what the server
// answered.
const require = createRequire(import.meta.url)
const __filename = fileURLToPath(import.meta.url)
const __dirname = require('node:path').dirname(__filename)

const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const test = require('node:test')
const vue = require('vue')
const { parse, compileScript, compileTemplate } = require('@vue/compiler-sfc')
const ts = require('typescript')

const file = path.join(__dirname, 'Workspace.vue')
const noticeFile = path.join(__dirname, 'OwnerUnavailableNotice.vue')

const FREEZE_REASON = '项目 Owner 的租户身份已失效，立项确认、模板升级与变更集应用已冻结。'
const FREEZE_RECOVERY = '可等待租户恢复 Owner 的身份，或由租户 Owner 指定受让人完成一次所有权转让。'

// The server's answer to the permission read outlet, per case.
let ownerAvailability = { frozen: false, reason: '', recovery: '' }

const auth = vue.reactive({ currentUserId: 'fixture-user', effectiveTenantId: 1 })
const api = new Proxy({}, { get: (_, name) => async () => ({ data: [] }) })

function compileComponent(target) {
  const parsed = parse(fs.readFileSync(target, 'utf8'), { filename: target })
  assert.deepEqual(parsed.errors, [])
  const script = compileScript(parsed.descriptor, { id: 'owner-freeze-' + path.basename(target) })
  const template = compileTemplate({
    source: parsed.descriptor.template.content,
    filename: target,
    id: 'owner-freeze-' + path.basename(target),
    compilerOptions: { bindingMetadata: script.bindings, hoistStatic: false },
  })
  assert.deepEqual(template.errors, [])
  return { ...evaluate(script.content, target).default, render: evaluate(template.code, target).render }
}

const localModules = new Map()
function evaluate(source, filename) {
  const output = ts.transpileModule(source, { fileName: filename, compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const module = { exports: {} }
  function load(id) {
    if (id === 'vue') return { ...vue, onMounted: () => {}, onUnmounted: () => {} }
    if (id === 'vue-router') return { onBeforeRouteLeave: () => {} }
    if (id === '@/stores/auth') return { useAuthStore: () => auth }
    if (id === '@/composables/useOwnerAvailability') {
      // The real composable hands out refs; the stub has to as well, or the
      // template would read a plain object instead of the value.
      return {
        useOwnerAvailability: () => ({
          frozen: vue.ref(ownerAvailability.frozen),
          reason: vue.ref(ownerAvailability.reason),
          recovery: vue.ref(ownerAvailability.recovery),
          refresh: async () => {},
        }),
      }
    }
    // The notice under test is the real one; the other child panels are not
    // what this file is about, so they stay empty.
    if (id.endsWith('OwnerUnavailableNotice.vue')) return { __esModule: true, default: compileComponent(noticeFile) }
    if (id.endsWith('.vue')) return { __esModule: true, default: { render: () => null } }
    if (id.startsWith('@/api/')) return api
    if (id.startsWith('.')) {
      const absolute = path.resolve(path.dirname(filename), id + (path.extname(id) ? '' : '.ts'))
      if (!localModules.has(absolute)) localModules.set(absolute, evaluate(fs.readFileSync(absolute, 'utf8'), absolute))
      return localModules.get(absolute)
    }
    return require(id)
  }
  new Function('require', 'module', 'exports', output)(load, module, module.exports)
  return module.exports
}

const Workspace = compileComponent(file)

function hostNode(type, text = '') {
  const node = {
    type, tagName: type.toUpperCase(), text, props: {}, children: [], parent: null, style: {}, listeners: {}, value: '', selected: false,
    focus() { global.document.activeElement = this },
    addEventListener(name, fn) { this.listeners[name] = fn },
    removeEventListener(name) { delete this.listeners[name] },
    setAttribute(name, value) { this.props[name] = value },
    removeAttribute(name) { delete this.props[name] },
    getAttribute(name) { return this.props[name] },
  }
  Object.defineProperty(node, 'options', { get() { return flatten(this).filter(n => n.type === 'option') } })
  return node
}

const renderer = vue.createRenderer({
  setScopeId(node, id) { node.props[id] = '' },
  createElement: hostNode, createText: text => hostNode('#text', text), createComment: text => hostNode('#comment', text),
  setText(node, text) { node.text = text },
  setElementText(node, text) { node.text = text; node.children = [] },
  parentNode: node => node.parent,
  nextSibling(node) { const siblings = node.parent?.children ?? []; return siblings[siblings.indexOf(node) + 1] ?? null },
  insert(node, parent, anchor = null) {
    if (node.parent) node.parent.children.splice(node.parent.children.indexOf(node), 1)
    const at = anchor ? parent.children.indexOf(anchor) : -1
    parent.children.splice(at < 0 ? parent.children.length : at, 0, node); node.parent = parent
  },
  remove(node) { if (node.parent) node.parent.children.splice(node.parent.children.indexOf(node), 1); node.parent = null },
  patchProp(node, key, previous, next) { node.props[key] = next; if (key === 'value') node.value = next },
})
function flatten(node) { return [node, ...node.children.flatMap(flatten)] }
function textOf(node) { return node.text + node.children.map(textOf).join('') }
function find(root, predicate) { const result = flatten(root).find(predicate); assert.ok(result, 'expected mounted node'); return result }

const draftProject = {
  id: 'p-owner-freeze',
  name: '冻结测试课题',
  status: 'draft',
  project_version: 2,
  spec_revision: 1,
  current_context_revision: 0,
  template_id: 'demo',
  template_version: '1',
  template_copy_version: 1,
  spec: { research_subject: '合成研究对象', research_goal: '合成研究目标' },
  members: [{ user_id: 'fixture-user', role: 'owner' }],
  template_copy: {
    version: 1,
    status: 'draft',
    definition: {
      id: 'demo',
      version: '1',
      fields: [
        { field_id: 'research_subject', label: '研究主题', type: 'string', required: true },
        { field_id: 'research_goal', label: '研究目标', type: 'string', required: true },
      ],
      sections: [],
      terms: [],
      required_fields: ['research_subject', 'research_goal'],
      rules: [],
    },
  },
}

async function mount() {
  const storage = new Map()
  global.localStorage = { getItem: key => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value), removeItem: key => storage.delete(key) }
  global.sessionStorage = { getItem: key => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value), removeItem: key => storage.delete(key) }
  global.document = { activeElement: null }
  let bindings
  const component = {
    ...Workspace,
    setup(props, context) {
      bindings = Workspace.setup(props, context)
      bindings.project.value = draftProject
      bindings.subject.value = draftProject.spec.research_subject
      bindings.goal.value = draftProject.spec.research_goal
      return bindings
    },
  }
  const root = hostNode('root')
  const app = renderer.createApp(component)
  app.component('RouterLink', { render() { return vue.h('a', {}, this.$slots.default?.()) } })
  app.mount(root)
  await vue.nextTick()
  await vue.nextTick()
  return {
    root,
    app,
    button(label) { return find(root, node => node.type === 'button' && textOf(node).trim() === label) },
    text() { return textOf(root) },
  }
}

test('a frozen project greys the activation entry out and says why', async () => {
  ownerAvailability = { frozen: true, reason: FREEZE_REASON, recovery: FREEZE_RECOVERY }
  const h = await mount()
  try {
    const entry = h.button('立项并创建章节')
    assert.equal(entry.props.disabled, true, 'the activation entry is greyed out while the Owner is unavailable')
    assert.match(String(entry.props.title ?? ''), /Owner/, 'the entry carries the freeze reason')
    assert.match(h.text(), /租户身份已失效/, 'the page tells why the Owner-only entries are greyed out')
    assert.match(h.text(), /恢复|转让/, 'the page tells how the project can be recovered')
  } finally { h.app.unmount() }
})

test('a project that is not frozen leaves the activation entry alone', async () => {
  ownerAvailability = { frozen: false, reason: '', recovery: '' }
  const h = await mount()
  try {
    const entry = h.button('立项并创建章节')
    assert.equal(entry.props.disabled, false, 'the activation entry stays usable while the Owner is available')
    assert.doesNotMatch(h.text(), /租户身份已失效/, 'the page claims no freeze it did not read')
  } finally { h.app.unmount() }
})
