import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
const require = createRequire(import.meta.url)
const __filename = fileURLToPath(import.meta.url)
const __dirname = require('node:path').dirname(__filename)

// Actual Workspace SFC + Vue in-memory renderer. This is not a browser layout test.
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const test = require('node:test')
const vue = require('vue')
const { parse, compileScript, compileTemplate, compileStyle } = require('@vue/compiler-sfc')
const ts = require('typescript')
const postcss = require('postcss')

const file = path.join(__dirname, 'Workspace.vue')
const descriptor = parse(fs.readFileSync(file, 'utf8'), { filename: file }).descriptor
const script = compileScript(descriptor, { id: 'history-regression' })
const template = compileTemplate({ source: descriptor.template.content, filename: file, id: 'history-regression', compilerOptions: { bindingMetadata: script.bindings } })
assert.deepEqual(template.errors, [])
const css = descriptor.styles.map(style => {
  const compiled = compileStyle({ source: style.content, filename: file, id: 'history-regression', scoped: style.scoped })
  assert.deepEqual(compiled.errors, [])
  return compiled.code
}).join('\n')

// Lifecycle IO, HTTP and unrelated child/editor components are explicit boundaries.
// The Workspace setup, computed state, watchers, render function and click handlers run unchanged.
const calls = []
const auth = vue.reactive({ currentUserId: 'fixture-user', effectiveTenantId: 1 })
const api = new Proxy({}, { get: (_, name) => async (...args) => {
  calls.push({ name, args })
  if (name === 'listKnowledgeBases') return { data: { items: [] } }
  if (name === 'getAccessStatus') return { data: null }
  if (name === 'getCurrentConfirmation') return { data: null }
  if (name === 'getEffectivePermission') return { data: { allowed: false, project_version: 2, reason_code: 'synthetic_denied' } }
  throw new Error('Unexpected Workspace IO: ' + String(name))
} })
const child = { inheritAttrs: false, setup(props, { expose }) { expose({ hasUnsaved: false, currentParagraphSources: () => [], currentMarkdown: () => null }); return () => null } }
const localModules = new Map()
function evaluate(source, filename) {
  const output = ts.transpileModule(source, { fileName: filename, compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const module = { exports: {} }
  function load(id) {
    if (id === 'vue') return { ...vue, onMounted: () => {}, onUnmounted: () => {} }
    if (id === 'vue-router') return { onBeforeRouteLeave: () => {} }
    if (id === '@/stores/auth') return { useAuthStore: () => auth }
    if (id.endsWith('.vue')) return { __esModule: true, default: child }
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
const Workspace = evaluate(script.content, file).default
Workspace.__scopeId = 'data-v-history-regression'
Workspace.render = evaluate(template.code, file + '.render.ts').render

function hostNode(type, text = '') {
  const node = { type, tagName: type.toUpperCase(), text, props: {}, children: [], parent: null, style: {}, listeners: {}, value: '', selected: false,
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
function classes(node) { return String(node.props.class ?? '').split(/\s+/).filter(Boolean) }
function find(root, predicate) { const result = flatten(root).find(predicate); assert.ok(result, 'expected mounted node'); return result }
function hasClass(node, name) { return classes(node).includes(name) }

// Consume real compiled stylesheet display declarations against the mounted ancestor tree.
// Restricted deliberately to class-only descendant selectors used for these panes;
// unsupported selectors cannot create a false visible result. No box/layout/paint assertion.
function matches(node, selector) {
  const segments = selector.trim().split(/\s+/)
  const segmentParts = segment => {
    const attrs = [...segment.matchAll(/\[(data-v-[\w-]+)\]/g)].map(match => match[1])
    const classPart = segment.replace(/\[data-v-[\w-]+\]/g, '')
    return /^(\.[\w-]+)+$/.test(classPart) ? { attrs, classes: classPart.slice(1).split('.') } : null
  }
  if (!segments.every(segment => segmentParts(segment))) return false
  const match = (current, segment) => {
    const parts = segmentParts(segment)
    return parts.classes.every(name => hasClass(current, name)) && parts.attrs.every(name => Object.hasOwn(current.props, name))
  }
  if (!match(node, segments.pop())) return false
  let ancestor = node.parent
  while (segments.length) {
    const segment = segments.pop()
    while (ancestor && !match(ancestor, segment)) ancestor = ancestor.parent
    if (!ancestor) return false
    ancestor = ancestor.parent
  }
  return true
}
function paneDisplay(node, stylesheet = css) {
  let winner = { value: 'block', weight: -1 }
  let order = 0
  postcss.parse(stylesheet).walkRules(rule => {
    // This regression concerns unconditional pane rules, not viewport simulation.
    if (rule.parent.type !== 'root') return
    const selectors = rule.selectors.filter(selector => matches(node, selector))
    if (!selectors.length) return
    const specificity = Math.max(...selectors.map(selector => (selector.match(/\./g) ?? []).length))
    rule.walkDecls('display', declaration => {
      const weight = (declaration.important ? 1000000 : 0) + specificity * 1000 + order++
      if (weight >= winner.weight) winner = { value: declaration.value, weight }
    })
  })
  return winner.value
}
function withoutHistorySelectors() {
  const sheet = postcss.parse(css)
  sheet.walkRules(rule => {
    const selectors = rule.selectors.filter(selector => !selector.includes('wb--tab-history'))
    if (selectors.length) rule.selector = selectors.join(', ')
    else rule.remove()
  })
  return sheet.toString()
}
async function mount() {
  calls.length = 0
  let bindings
  const storage = new Map()
  global.localStorage = { getItem: key => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value), removeItem: key => storage.delete(key) }
  // Native v-model directives need an activeElement lookup in this host only.
  global.document = { activeElement: null }
  const project = { id: 'p-fixture', name: '历史测试课题', status: 'active', project_version: 2, spec_revision: 1, current_context_revision: 1, template_id: 'demo', template_version: '1', spec: {}, members: [], template_copy: { definition: { fields: [], sections: [], terms: [], rules: [] } } }
  const chapter = { id: 'chapter-fixture', project_id: project.id, section_id: 'intro', title: '引言', current_version_id: 'version-fixture', body_markdown: '测试正文', source_ids: [], citation_usages: [], review_items: [], confirmation_valid: false }
  const copy = { project_id: project.id, chapter_id: chapter.id, base_chapter_version_id: chapter.current_version_id, spec_revision: 1, working_copy_revision: 4, body_markdown: chapter.body_markdown, source_ids: [], citation_usages: [], review_items: [], updated_at: '2026-10-14T01:00:00Z' }
  const component = { ...Workspace, setup(props, context) {
    bindings = Workspace.setup(props, context)
    bindings.project.value = project; bindings.chapters.value = [chapter]; bindings.chapter.value = chapter
    bindings.workingCopy.value = copy; bindings.bodyDraft.value = chapter.body_markdown
    bindings.editorDocs.value = { [chapter.id]: { copy, markdown: copy.body_markdown, revision: 4, committed: 4, source_ids: [], citation_usages: [], base_chapter_version_id: chapter.current_version_id } }
    bindings.editorReady.value = true
    bindings.chapterVersions.value = [{ ...chapter, id: chapter.current_version_id, parent_version_id: null, spec_revision: 1, created_at: '2026-10-14T01:00:00Z' }]
    return bindings
  } }
  const root = hostNode('root')
  const app = renderer.createApp(component)
  app.component('RouterLink', { render() { return vue.h('a', {}, this.$slots.default?.()) } })
  app.mount(root); await vue.nextTick(); await vue.nextTick()
  return { root, state: bindings, app, pane: () => find(root, node => hasClass(node, 'wb-pane-history')), click: async predicate => { find(root, predicate).props.onClick(); await vue.nextTick() } }
}

test('版本 tab click renders its actual form/history and selects the history display rule', async () => {
  const h = await mount()
  try {
    assert.equal(paneDisplay(h.pane()), 'none')
    await h.click(node => node.props.role === 'tab' && textOf(node) === '版本')
    const tab = find(h.root, node => node.props.role === 'tab' && textOf(node) === '版本')
    assert.equal(tab.props['aria-selected'], true)
    assert.equal(paneDisplay(h.pane()), 'block')
    assert.match(textOf(h.pane()), /保存工作副本/)
    assert.match(textOf(h.pane()), /提交为正式版本/)
    assert.match(textOf(h.pane()), /确认当前章节版本/)
    assert.match(textOf(h.pane()), /正式版本历史/)
    assert.match(textOf(h.pane()), /恢复到工作副本/)
    assert.equal(calls.some(call => !String(call.name).startsWith('get') && call.name !== 'listKnowledgeBases'), false)
  } finally { h.app.unmount() }
})

test('actual history click cannot pass when every history visibility selector is removed', async () => {
  const h = await mount()
  try {
    await h.click(node => node.props.role === 'tab' && textOf(node) === '版本')
    assert.equal(paneDisplay(h.pane(), withoutHistorySelectors()), 'none')
    assert.equal(paneDisplay(h.pane()), 'block')
  } finally { h.app.unmount() }
})

test('switching tabs and closing the inspector hides history and retains draft input', async () => {
  const h = await mount()
  try {
    h.state.bodyDraft.value = '仍未提交的输入'
    await h.click(node => node.props.role === 'tab' && textOf(node) === '版本')
    await h.click(node => node.props.role === 'tab' && textOf(node) === '来源')
    assert.equal(paneDisplay(h.pane()), 'none')
    assert.equal(h.state.bodyDraft.value, '仍未提交的输入')
    await h.click(node => node.props.role === 'tab' && textOf(node) === '版本')
    await h.click(node => node.props['aria-label'] === '收起辅助面板')
    assert.equal(paneDisplay(h.pane()), 'none')
    assert.equal(h.state.bodyDraft.value, '仍未提交的输入')
  } finally { h.app.unmount() }
})

test('focus mode topbar 版本 click overrides the real focus hide rule', async () => {
  const h = await mount()
  try {
    await h.click(node => node.type === 'button' && textOf(node) === '专注')
    assert.equal(paneDisplay(h.pane()), 'none')
    await h.click(node => node.type === 'button' && textOf(node) === '版本' && node.props.role !== 'tab')
    assert.equal(paneDisplay(h.pane()), 'block')
    assert.equal(paneDisplay(h.pane(), withoutHistorySelectors()), 'none')
    assert.match(textOf(h.pane()), /正式版本历史/)
  } finally { h.app.unmount() }
})
