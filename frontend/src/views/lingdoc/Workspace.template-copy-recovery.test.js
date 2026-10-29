import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'

// Template-copy recovery against the real mounted Workspace and mocked HTTP.
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
let apiHandlers = {}
const api = new Proxy({}, { get: (_, name) => async (...args) => apiHandlers[name] ? apiHandlers[name](...args) : { data: [] } })

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
    getRootNode() { return global.document },
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
    id: 'fixture-copy',
    source_template_id: 'demo',
    source_template_version: '1',
    content_hash: 'fixture-content',
    ruleset_hash: 'fixture-rules',
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
  global.Document = class Document {}; global.document = new Document(); global.document.activeElement = null
  global.window = { confirm: () => true, matchMedia: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }) }
  let bindings
  const component = {
    ...Workspace,
    setup(props, context) {
      bindings = Workspace.setup(props, context)
      bindings.project.value = structuredClone(draftProject)
      bindings.hydrateTemplateCopyDraft(bindings.project.value)
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
    bindings,
    button(label) { return find(root, node => node.type === 'button' && textOf(node).trim() === label) },
    text() { return textOf(root) },
  }
}


const flush = async () => { for (let i = 0; i < 5; i++) await Promise.resolve(); await vue.nextTick() }
const deferred = () => {
  let resolve, reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
function cleanFixture() {
  let allowed = true
  let permissionVersion = 2
  let permissionThrows = false
  let saves = 0, previews = 0
  apiHandlers = {
    getTemplateVersion: async (id, version) => ({ data: { template_id: id, version, state: 'published' } }),
    getEffectivePermission: async (id, capability) => {
      if (permissionThrows) throw { status: 404 }
      return { data: { allowed, capability, project_version: permissionVersion } }
    },
    previewTemplateCopyEdit: async (id, input) => {
      previews++
      return { data: { project_id: id, expected_project_version: input.expected_project_version,
        source_copy_version: input.expected_template_copy_version, target_copy_version: input.expected_template_copy_version + 1,
        fields: [], section_changes: [], missing_required: [], orphaned: [], incompatible: [], target_content_hash: '', target_ruleset_hash: '', ruleset_changed: false } }
    },
    saveTemplateCopyEdit: async () => { saves++; throw new Error('configure save in test') },
  }
  return {
    deny: () => { allowed = false },
    allow: () => { allowed = true },
    unavailable: () => { permissionThrows = true },
    setVersion: n => { permissionVersion = n },
    saves: () => saves,
    previews: () => previews,
    countSave: () => { saves++ },
  }
}
async function changedAndPreviewed(h) {
  h.bindings.templateCopyDraft.value.fields[0].label = '本地修改'
  await h.bindings.previewTemplateCopy()
  await flush()
  assert.equal(h.bindings.templateCopyPreviewCurrent.value, true)
}
test('new required fields have an explicit optional-first path in a draft', async () => {
  cleanFixture()
  const h = await mount()
  try {
    await flush()
    assert.equal(h.bindings.templateCopyCanEdit.value, true)
    h.bindings.addTemplateField()
    const added = h.bindings.templateCopyDraft.value.fields.at(-1)
    const target = { checked: true }
    h.bindings.updateTemplateFieldRequired(added.field_id, { target })
    await flush()
    assert.equal(added.required, false)
    assert.equal(target.checked, false)
    assert.match(h.text(), /新字段先作为选填保存/)
    assert.match(h.bindings.templateCopyNotice.value, /选填保存/)
  } finally { h.app.unmount() }
})
test('missing and incompatible values can be saved without losing the template draft', async () => {
  const fixture = cleanFixture()
  let saved
  apiHandlers.previewTemplateCopyEdit = async (id, input) => ({ data: {
    project_id: id, expected_project_version: input.expected_project_version,
    source_copy_version: input.expected_template_copy_version, target_copy_version: input.expected_template_copy_version + 1,
    fields: [], section_changes: [], missing_required: ['research_goal', 'new_field'], orphaned: [], incompatible: ['new_field'],
    target_content_hash: '', target_ruleset_hash: '', ruleset_changed: false,
  } })
  const h = await mount()
  try {
    await flush()
    h.bindings.project.value.spec.research_goal = ''
    h.bindings.goal.value = ''
    h.bindings.templateCopyDraft.value.fields[0].label = '未保存的模板名称'
    h.bindings.templateCopyDraft.value.fields.push({ field_id: 'new_field', label: '新增条件', type: 'integer', required: true })
    apiHandlers.saveSpec = async (id, expected, fields) => {
      saved = { id, expected, fields: structuredClone(fields) }
      const next = JSON.parse(JSON.stringify(h.bindings.project.value))
      next.spec = structuredClone(fields)
      next.spec_revision++
      next.project_version++
      fixture.setVersion(next.project_version)
      return { data: next }
    }
    await h.bindings.previewTemplateCopy()
    await flush()
    assert.equal(h.bindings.templateCopyPreviewBlocked.value, true)
    assert.match(h.text(), /保存研究条件/)
    h.bindings.updateTemplateRepairValue('research_goal', { target: { value: '修复目标' } })
    h.bindings.updateTemplateRepairValue('new_field', { target: { value: '42' } })
    await h.bindings.saveTemplateRepairValues()
    await flush()
    assert.equal(saved.expected, draftProject.spec_revision)
    assert.deepEqual(saved.fields, { research_subject: '合成研究对象', research_goal: '修复目标', new_field: '42' })
    assert.equal(h.bindings.templateCopyDraft.value.fields[0].label, '未保存的模板名称')
    assert.equal(h.bindings.project.value.spec.new_field, '42')
    assert.equal(h.bindings.templateCopyPreviewCurrent.value, false)
    assert.match(h.bindings.templateCopyNotice.value, /研究条件已保存.*重新预览/)
  } finally { h.app.unmount() }
})
test('repair conflict keeps both field input and template edits for recovery', async () => {
  cleanFixture()
  apiHandlers.previewTemplateCopyEdit = async (id, input) => ({ data: {
    project_id: id, expected_project_version: input.expected_project_version,
    source_copy_version: input.expected_template_copy_version, target_copy_version: input.expected_template_copy_version + 1,
    fields: [], section_changes: [], missing_required: ['research_goal'], orphaned: [], incompatible: [],
    target_content_hash: '', target_ruleset_hash: '', ruleset_changed: false,
  } })
  apiHandlers.saveSpec = async () => { throw { status: 409, error: { code: 'version_conflict' } } }
  const h = await mount()
  try {
    await flush()
    h.bindings.project.value.spec.research_goal = ''
    h.bindings.goal.value = ''
    h.bindings.templateCopyDraft.value.fields[0].label = '保留此模板输入'
    await h.bindings.previewTemplateCopy()
    h.bindings.updateTemplateRepairValue('research_goal', { target: { value: '保留此研究条件' } })
    await h.bindings.saveTemplateRepairValues()
    await flush()
    assert.equal(h.bindings.goal.value, '保留此研究条件')
    assert.equal(h.bindings.templateCopyDraft.value.fields[0].label, '保留此模板输入')
    assert.equal(h.bindings.templateCopyConflict.value, true)
    assert.equal(h.bindings.templateCopyPreviewCurrent.value, false)
  } finally { h.app.unmount() }
})
test('an unknown orphan can be added to the draft without deleting its stored value', async () => {
  cleanFixture()
  apiHandlers.previewTemplateCopyEdit = async (id, input) => ({ data: {
    project_id: id, expected_project_version: input.expected_project_version,
    source_copy_version: input.expected_template_copy_version, target_copy_version: input.expected_template_copy_version + 1,
    fields: [], section_changes: [], missing_required: [], orphaned: ['legacy_value', 'invalid field id'], incompatible: [],
    target_content_hash: '', target_ruleset_hash: '', ruleset_changed: false,
  } })
  const h = await mount()
  try {
    await flush()
    h.bindings.project.value.spec.legacy_value = '原有值'
    h.bindings.templateCopyDraft.value.fields[0].label = '本地修改'
    await h.bindings.previewTemplateCopy()
    await flush()
    assert.deepEqual(h.bindings.templateUnknownOrphaned.value, ['legacy_value'])
    assert.match(h.text(), /旧字段标识不符合当前模板规则/)
    h.bindings.addOrphanedFieldPlaceholder('legacy_value')
    assert.equal(h.bindings.project.value.spec.legacy_value, '原有值')
    assert.equal(h.bindings.templateCopyDraft.value.fields.at(-1).field_id, 'legacy_value')
    assert.equal(h.bindings.templateCopyDraft.value.fields.at(-1).required, false)
    assert.equal(h.bindings.templateCopyPreviewCurrent.value, false)
  } finally { h.app.unmount() }
})
test('a field is archived and viewed before confirmation removes it, while the template draft survives', async () => {
  const fixture = cleanFixture()
  let archiveWrites = 0, removeWrites = 0, archiveInput, removeInput
  let removed = false
  apiHandlers.previewTemplateCopyEdit = async (id, input) => ({ data: {
    project_id: id, expected_project_version: input.expected_project_version,
    source_copy_version: input.expected_template_copy_version, target_copy_version: input.expected_template_copy_version + 1,
    fields: [], section_changes: [], missing_required: [], orphaned: removed ? [] : ['research_goal'], incompatible: [],
    target_content_hash: 'target-hash', target_ruleset_hash: 'rules-hash', ruleset_changed: false,
  } })
  apiHandlers.createTemplateFieldArchive = async (id, input) => {
    archiveWrites++; archiveInput = structuredClone(input)
    return { data: { id: 'archive-1', status: 'recorded', archive_version: 1, project_version: 2, spec_revision: 1, template_copy_version: 1 }, meta: { replayed: false } }
  }
  const archive = {
    id: 'archive-1', project_id: draftProject.id, field_id: 'research_goal', status: 'recorded', archive_version: 1,
    value_present: true, value: '合成研究目标', metadata_present: true,
    metadata: { value: '合成研究目标', origin: 'ai_generated', status: 'draft', modified_by: 'fixture-user', modified_at: '2026-01-01', provenance: { source_type: 'agent_turn', source_id: 'turn-1', locator: '位置', created_by: 'fixture-user', accessible: true, needs_review: false } },
    value_redacted: false, created_by: 'fixture-user', created_at: '2026-01-01', project_version: 2, spec_revision: 1, template_copy_version: 1, target_content_hash: 'target-hash',
  }
  apiHandlers.getTemplateFieldArchive = async () => ({ data: structuredClone(archive) })
  apiHandlers.removeArchivedTemplateField = async (id, archiveId, input) => {
    removeWrites++; removeInput = structuredClone(input); removed = true
    const next = JSON.parse(JSON.stringify(h.bindings.project.value))
    delete next.spec.research_goal
    next.spec_revision = 2; next.project_version = 3
    apiHandlers.getProject = async () => ({ data: next })
    fixture.setVersion(3)
    return { data: { id: archiveId, status: 'removed', archive_version: 2, project_version: 3, spec_revision: 2, template_copy_version: 1 } }
  }
  const h = await mount()
  try {
    await flush()
    h.bindings.removeTemplateField('research_goal')
    await h.bindings.previewTemplateCopy()
    await flush()
    assert.equal(h.bindings.templateCopyPreviewBlocked.value, true)
    await h.bindings.archiveTemplateField('research_goal')
    await flush()
    assert.equal(archiveWrites, 1)
    assert.equal(archiveInput.field_id, 'research_goal')
    assert.equal(archiveInput.expected_spec_revision, 1)
    assert.equal(archiveInput.template_copy.fields.some(f => f.field_id === 'research_goal'), false)
    assert.equal(h.bindings.project.value.spec.research_goal, '合成研究目标')
    assert.match(h.text(), /旧值：合成研究目标/)
    assert.match(h.text(), /agent_turn/)
    assert.equal(h.bindings.canRemoveArchivedTemplateField.value, true)
    global.window.confirm = () => false
    await h.bindings.removeTemplateFieldAfterArchive()
    assert.equal(removeWrites, 0)
    global.window.confirm = () => true
    await h.bindings.removeTemplateFieldAfterArchive()
    await flush()
    assert.equal(removeWrites, 1)
    assert.equal(removeInput.confirmed, true)
    assert.equal(removeInput.expected_archive_version, 1)
    assert.equal(Object.hasOwn(h.bindings.project.value.spec, 'research_goal'), false)
    assert.equal(Object.hasOwn(h.bindings.specDraftFields.value, 'research_goal'), false)
    assert.equal(h.bindings.templateCopyDraft.value.fields.some(f => f.field_id === 'research_goal'), false)
    assert.equal(h.bindings.templateCopyPreviewCurrent.value, false)
    assert.match(h.text(), /已移除/)
    await h.bindings.previewTemplateCopy()
    await flush()
    assert.equal(h.bindings.templateCopyPreviewBlocked.value, false)
  } finally { h.app.unmount() }
})
test('archive detail read failure and redacted or stale details never enable removal', async () => {
  cleanFixture()
  let reads = 0, removals = 0
  apiHandlers.previewTemplateCopyEdit = async (id, input) => ({ data: {
    project_id: id, expected_project_version: input.expected_project_version,
    source_copy_version: input.expected_template_copy_version, target_copy_version: input.expected_template_copy_version + 1,
    fields: [], section_changes: [], missing_required: [], orphaned: ['research_goal'], incompatible: [],
    target_content_hash: 'target-hash', target_ruleset_hash: 'rules-hash', ruleset_changed: false,
  } })
  apiHandlers.createTemplateFieldArchive = async () => ({ data: { id: 'archive-2', status: 'recorded', archive_version: 1 } })
  apiHandlers.getTemplateFieldArchive = async () => {
    reads++
    if (reads === 1) throw { status: 503 }
    return { data: { id: 'archive-2', project_id: draftProject.id, field_id: 'research_goal', status: 'recorded', archive_version: 1,
      value_present: true, value: '', metadata_present: true, value_redacted: true, created_by: 'fixture-user', created_at: '2026-01-01',
      project_version: 2, spec_revision: 1, template_copy_version: 1, target_content_hash: 'target-hash' } }
  }
  apiHandlers.removeArchivedTemplateField = async () => { removals++; throw new Error('unexpected write') }
  const h = await mount()
  try {
    await flush()
    h.bindings.removeTemplateField('research_goal')
    await h.bindings.previewTemplateCopy()
    await h.bindings.archiveTemplateField('research_goal')
    await flush()
    assert.match(h.bindings.templateCopyNotice.value, /归档已保存.*读取详情失败/)
    assert.equal(h.bindings.templateFieldArchivePendingId.value, 'archive-2')
    assert.equal(h.bindings.canRemoveArchivedTemplateField.value, false)
    await h.bindings.loadTemplateFieldArchive('archive-2')
    await flush()
    assert.match(h.text(), /来源不可访问/)
    assert.equal(h.bindings.canRemoveArchivedTemplateField.value, false)
    h.bindings.templateFieldArchive.value.value_redacted = false
    h.bindings.templateFieldArchive.value.target_content_hash = 'other-target'
    assert.equal(h.bindings.canRemoveArchivedTemplateField.value, false)
    await h.bindings.removeTemplateFieldAfterArchive()
    assert.equal(removals, 0)
  } finally { h.app.unmount() }
})
test('archive removal success with failed project readback preserves the template draft and reports the write', async () => {
  cleanFixture()
  apiHandlers.previewTemplateCopyEdit = async (id, input) => ({ data: {
    project_id: id, expected_project_version: input.expected_project_version,
    source_copy_version: input.expected_template_copy_version, target_copy_version: input.expected_template_copy_version + 1,
    fields: [], section_changes: [], missing_required: [], orphaned: ['research_goal'], incompatible: [],
    target_content_hash: 'target-hash', target_ruleset_hash: 'rules-hash', ruleset_changed: false,
  } })
  apiHandlers.getTemplateFieldArchive = async () => ({ data: {
    id: 'archive-3', project_id: draftProject.id, field_id: 'research_goal', status: 'recorded', archive_version: 1,
    value_present: true, value: '合成研究目标', metadata_present: false, value_redacted: false,
    created_by: 'fixture-user', created_at: '2026-01-01', project_version: 2, spec_revision: 1,
    template_copy_version: 1, target_content_hash: 'target-hash',
  } })
  apiHandlers.removeArchivedTemplateField = async () => ({ data: { id: 'archive-3', status: 'removed', archive_version: 2, project_version: 3, spec_revision: 2, template_copy_version: 1 } })
  apiHandlers.getProject = async () => { throw { status: 503 } }
  const h = await mount()
  try {
    await flush()
    h.bindings.removeTemplateField('research_goal')
    await h.bindings.previewTemplateCopy()
    await h.bindings.loadTemplateFieldArchive('archive-3')
    await h.bindings.removeTemplateFieldAfterArchive()
    await flush()
    assert.match(h.bindings.templateCopyNotice.value, /字段已移除.*重新读取失败/)
    assert.equal(h.bindings.templateCopyConflict.value, true)
    assert.equal(h.bindings.templateCopyPreviewCurrent.value, false)
    assert.equal(h.bindings.templateCopyDraft.value.fields.some(f => f.field_id === 'research_goal'), false)
    assert.equal(h.bindings.templateFieldArchive.value.status, 'removed')
  } finally { h.app.unmount() }
})
test('workflow refresh preserves an edited template and requires comparing a new copy', async () => {
  const fixture = cleanFixture()
  const latest = structuredClone(draftProject)
  latest.project_version = 3
  latest.template_copy_version = 2
  latest.template_copy.version = 2
  latest.template_copy.ruleset_hash = 'updated-rules'
  latest.template_copy.definition.fields[0].label = '服务器的新名称'
  apiHandlers.getProject = async () => ({ data: latest })
  const h = await mount()
  try {
    await flush()
    await changedAndPreviewed(h)
    fixture.setVersion(3)
    await h.bindings.refreshFullWorkflowProject()
    await flush()
    assert.equal(h.bindings.templateCopyDraft.value.fields[0].label, '本地修改')
    assert.equal(h.bindings.project.value.template_copy.definition.fields[0].label, '服务器的新名称')
    assert.equal(h.bindings.templateCopyConflict.value, true)
    assert.equal(h.bindings.templateCopyLatestLoaded.value, true)
    assert.equal(h.bindings.templateCopyPreviewCurrent.value, false)
  } finally { h.app.unmount() }
})
test('workflow refresh hydrates a new template when no draft is being edited', async () => {
  const fixture = cleanFixture()
  const latest = structuredClone(draftProject)
  latest.project_version = 3
  latest.template_copy_version = 2
  latest.template_copy.version = 2
  latest.template_copy.definition.fields[0].label = '服务器的新名称'
  apiHandlers.getProject = async () => ({ data: latest })
  const h = await mount()
  try {
    await flush()
    fixture.setVersion(3)
    await h.bindings.refreshFullWorkflowProject()
    await flush()
    assert.equal(h.bindings.templateCopyDraft.value.fields[0].label, '服务器的新名称')
    assert.equal(h.bindings.templateCopyConflict.value, false)
    assert.equal(h.bindings.templateCopyChanged.value, false)
  } finally { h.app.unmount() }
})
test('legacy rule override uses the returned policy and requires a current risk preview', async () => {
  const fixture = cleanFixture()
  let saved
  apiHandlers.saveLegacyRuleOverrides = async (id, input) => {
    saved = { id, input: structuredClone(input) }
    const next = JSON.parse(JSON.stringify(h.bindings.project.value))
    next.project_version++
    next.template_copy_version++
    next.template_copy.version++
    next.template_copy.rule_set_snapshot.bindings[0].severity = 'info'
    fixture.setVersion(next.project_version)
    return { data: next }
  }
  const h = await mount()
  try {
    await flush()
    const copy = h.bindings.project.value.template_copy
    copy.rule_set_snapshot = { ruleset_id: 'legacy', version: '1', bindings: [{ rule_id: 'limit', enabled: true, severity: 'warning', parameters: { limit: 3 } }] }
    copy.source_policy_hash = 'reviewed-policy'
    copy.binding_policies = [{ rule_id: 'limit', may_disable: false, allowed_severities: ['info'], allowed_parameter_keys: ['limit'] }]
    h.bindings.legacyRuleId.value = 'limit'
    h.bindings.legacySeverity.value = 'info'
    h.bindings.legacyReason.value = '项目需要提示'
    h.bindings.legacyParameterEdits.value.limit.action = 'set'
    h.bindings.legacyParameterEdits.value.limit.value = '5'
    await h.bindings.saveLegacyOverride()
    assert.equal(saved, undefined)
    await h.bindings.previewTemplateCopy()
    await flush()
    await h.bindings.saveLegacyOverride()
    await flush()
    assert.deepEqual(saved.input, { expected_project_version: 2, expected_template_copy_version: 1,
      reason: '项目需要提示', overrides: [{ rule_id: 'limit', severity: 'info', parameters: { limit: 5 } }] })
    assert.equal(h.bindings.project.value.template_copy_version, 2)
    assert.match(h.bindings.templateCopyNotice.value, /规则覆盖已保存/)
    assert.equal(h.bindings.templateCopyPreviewCurrent.value, false)
  } finally { h.app.unmount() }
})
test('legacy override does not submit malformed parameter input or a canonical binding', async () => {
  cleanFixture()
  let calls = 0
  apiHandlers.saveLegacyRuleOverrides = async () => { calls++; throw new Error('unexpected write') }
  const h = await mount()
  try {
    await flush()
    const copy = h.bindings.project.value.template_copy
    copy.rule_set_snapshot = { ruleset_id: 'legacy', version: '1', bindings: [{ rule_id: 'limit', enabled: true, severity: 'warning', parameters: { limit: 3 } }] }
    copy.source_policy_hash = 'reviewed-policy'
    copy.binding_policies = [{ rule_id: 'limit', may_disable: false, allowed_parameter_keys: ['limit'] }]
    h.bindings.legacyRuleId.value = 'limit'
    h.bindings.legacyReason.value = '修改限制'
    h.bindings.legacyParameterEdits.value.limit.action = 'set'
    h.bindings.legacyParameterEdits.value.limit.value = 'not a number'
    await h.bindings.previewTemplateCopy()
    await h.bindings.saveLegacyOverride()
    assert.equal(calls, 0)
    assert.match(h.bindings.legacyOverrideError.value, /需要数字/)
    copy.definition.ruleset_ref = { ruleset_id: 'canonical' }
    await flush()
    assert.equal(h.bindings.legacyBindingCopy.value, null)
    await h.bindings.saveLegacyOverride()
    assert.equal(calls, 0)
  } finally { h.app.unmount() }
})
test('a policy refresh preserves unsaved override input until the user reviews it', async () => {
  const fixture = cleanFixture()
  const h = await mount()
  try {
    await flush()
    const copy = h.bindings.project.value.template_copy
    copy.rule_set_snapshot = { ruleset_id: 'legacy', version: '1', bindings: [{ rule_id: 'limit', enabled: true, severity: 'warning', parameters: { limit: 3 } }] }
    copy.source_policy_hash = 'policy-1'
    copy.binding_policies = [{ rule_id: 'limit', may_disable: false, allowed_parameter_keys: ['limit'] }]
    h.bindings.legacyRuleId.value = 'limit'
    h.bindings.legacyReason.value = '保留理由'
    h.bindings.legacyParameterEdits.value.limit.action = 'set'
    h.bindings.legacyParameterEdits.value.limit.value = '5'
    const latest = JSON.parse(JSON.stringify(h.bindings.project.value))
    latest.project_version = 3
    latest.template_copy_version = 2
    latest.template_copy.version = 2
    latest.template_copy.source_policy_hash = 'policy-2'
    latest.template_copy.binding_policies[0].allowed_parameter_keys = ['limit', 'extra']
    apiHandlers.getProject = async () => ({ data: latest })
    fixture.setVersion(3)
    await h.bindings.refreshFullWorkflowProject()
    await flush()
    assert.equal(h.bindings.legacyOverrideNeedsReview.value, true)
    assert.equal(h.bindings.legacyParameterEdits.value.limit.value, '5')
    assert.equal(h.bindings.legacyReason.value, '保留理由')
    assert.equal(h.bindings.unsavedInputs.value, true)
    h.bindings.resetStaleLegacyOverride()
    assert.equal(h.bindings.legacyOverrideNeedsReview.value, false)
    assert.equal(h.bindings.legacyParameterEdits.value.limit.action, 'keep')
    assert.ok(h.bindings.legacyParameterEdits.value.extra)
  } finally { h.app.unmount() }
})
test('a changed binding keeps legacy override input visible and discardable', async () => {
  const fixture = cleanFixture()
  let writes = 0
  apiHandlers.saveLegacyRuleOverrides = async () => { writes++; throw new Error('unexpected write') }
  const h = await mount()
  try {
    await flush()
    const copy = h.bindings.project.value.template_copy
    copy.rule_set_snapshot = { ruleset_id: 'legacy', version: '1', bindings: [{ rule_id: 'limit', enabled: true, severity: 'warning', parameters: { limit: 3 } }] }
    copy.source_policy_hash = 'policy-1'
    copy.binding_policies = [{ rule_id: 'limit', may_disable: true, allowed_parameter_keys: ['limit'] }]
    h.bindings.legacyRuleId.value = 'limit'
    h.bindings.legacyReason.value = '保留理由'
    h.bindings.legacyEnabled.value = 'false'
    h.bindings.legacyParameterEdits.value.limit.action = 'set'
    h.bindings.legacyParameterEdits.value.limit.value = '5'
    const latest = JSON.parse(JSON.stringify(h.bindings.project.value))
    latest.project_version = 3
    latest.template_copy_version = 2
    latest.template_copy.version = 2
    latest.template_copy.definition.ruleset_ref = { ruleset_id: 'canonical' }
    apiHandlers.getProject = async () => ({ data: latest })
    fixture.setVersion(3)
    await h.bindings.refreshFullWorkflowProject()
    await flush()
    assert.equal(h.bindings.legacyBindingCopy.value, null)
    assert.equal(h.bindings.legacyOverrideNeedsReview.value, true)
    assert.match(h.text(), /绑定方式已变化/)
    assert.match(h.text(), /保留理由/)
    assert.match(h.text(), /启用 false/)
    assert.match(h.text(), /"value":"5"/)
    await h.bindings.saveLegacyOverride()
    assert.equal(writes, 0)
    h.button('放弃未保存的规则覆盖').props.onClick()
    await flush()
    assert.equal(h.bindings.legacyOverrideNeedsReview.value, false)
    assert.doesNotMatch(h.text(), /绑定方式已变化/)
    assert.equal(h.bindings.legacyReason.value, '')
  } finally { h.app.unmount() }
})
test('a replayed template write with failed readback reports the saved write and retains input', async () => {
  cleanFixture()
  apiHandlers.saveTemplateCopyEdit = async () => {
    const next = JSON.parse(JSON.stringify(h.bindings.project.value))
    next.project_version++
    next.template_copy_version++
    next.template_copy.version++
    return { data: next, meta: { replayed: true, refresh_required: true } }
  }
  apiHandlers.getProject = async () => { throw new Error('readback unavailable') }
  const h = await mount()
  try {
    await flush()
    await changedAndPreviewed(h)
    await h.bindings.saveTemplateCopy()
    await flush()
    assert.equal(h.bindings.templateCopyDraft.value.fields[0].label, '本地修改')
    assert.equal(h.bindings.templateCopyConflict.value, true)
    assert.match(h.bindings.templateCopyNotice.value, /模板已保存，但重新读取失败/)
    assert.equal(h.bindings.templateCopyPreviewCurrent.value, false)
  } finally { h.app.unmount() }
})
test('in-flight save blocks project switching and a late response cannot replace another project or clear its busy state', async () => {
  const fixture = cleanFixture()
  const response = deferred()
  apiHandlers.saveTemplateCopyEdit = async () => { fixture.countSave(); return response.promise }
  const h = await mount()
  try {
    await flush()
    await changedAndPreviewed(h)
    const saved = h.bindings.saveTemplateCopy()
    assert.equal(h.bindings.templateCopyBusy.value, true)
    assert.equal(await h.bindings.selectProject('other-project'), false)
    assert.equal(h.bindings.project.value.id, draftProject.id)
    const other = structuredClone(draftProject)
    other.id = 'other-project'
    h.bindings.project.value = other
    h.bindings.templateCopyDraft.value.fields[0].label = '其他课题的输入'
    h.bindings.templateCopyBusy.value = true
    const old = structuredClone(draftProject)
    old.project_version = 3
    old.template_copy_version = 2
    response.resolve({ data: old })
    await saved
    assert.equal(h.bindings.project.value.id, 'other-project')
    assert.equal(h.bindings.templateCopyDraft.value.fields[0].label, '其他课题的输入')
    assert.equal(h.bindings.templateCopyBusy.value, true)
  } finally { h.app.unmount() }
})
test('409 invalidates preview and reloads unchanged server template without losing draft', async () => {
  const fixture = cleanFixture()
  apiHandlers.saveTemplateCopyEdit = async () => { fixture.countSave(); throw { status: 409, error: { code: 'version_conflict' } } }
  const latest = structuredClone(draftProject)
  latest.project_version = 3
  apiHandlers.getProject = async () => ({ data: latest })
  const h = await mount()
  try {
    await flush()
    await changedAndPreviewed(h)
    await h.bindings.saveTemplateCopy()
    assert.equal(h.bindings.templateCopyConflict.value, true)
    assert.equal(h.bindings.templateCopyPreviewCurrent.value, false)
    assert.equal(h.bindings.templateCopyDraft.value.fields[0].label, '本地修改')
    fixture.setVersion(3)
    await h.bindings.reloadTemplateCopyPreservingDraft()
    await flush()
    assert.equal(h.bindings.project.value.project_version, 3)
    assert.equal(h.bindings.templateCopyDraft.value.fields[0].label, '本地修改')
    assert.equal(h.bindings.templateCopyConflict.value, false)
    assert.equal(h.bindings.templateCopyPreviewCurrent.value, false)
  } finally { h.app.unmount() }
})
test('concurrently changed template stays blocked until an explicit comparison acknowledgement', async () => {
  const fixture = cleanFixture()
  apiHandlers.saveTemplateCopyEdit = async () => { throw { status: 409, error: { code: 'version_conflict' } } }
  const latest = structuredClone(draftProject)
  latest.project_version = 3
  latest.template_copy.version = 2
  latest.template_copy.definition.fields[0].label = '别人的修改'
  latest.template_copy.definition.terms = [{ term_id: 'term-1', preferred: '服务器术语', variants: ['服务器变体'] }]
  latest.template_copy.definition.rules = [{ rule_id: 'rule-1', kind: 'presence', severity: 'warning', evaluator: 'required', message: '服务器规则', parameters: { target_id: 'server-field' } }]
  apiHandlers.getProject = async () => ({ data: latest })
  const h = await mount()
  try {
    await flush()
    h.bindings.templateCopyDraft.value.terms = [{ term_id: 'term-1', preferred: '本地术语', variants: ['本地变体'] }]
    h.bindings.templateCopyDraft.value.rules = [{ rule_id: 'rule-1', kind: 'presence', severity: 'warning', evaluator: 'required', message: '本地规则', parameters: { target_id: 'local-field' } }]
    await changedAndPreviewed(h)
    await h.bindings.saveTemplateCopy()
    fixture.setVersion(3)
    await h.bindings.reloadTemplateCopyPreservingDraft()
    await flush()
    assert.equal(h.bindings.templateCopyDraft.value.fields[0].label, '本地修改')
    assert.equal(h.bindings.templateCopyConflict.value, true)
    assert.equal(h.bindings.templateCopyLatestLoaded.value, true)
    for (const visible of ['服务器术语', '服务器变体', '本地术语', '本地变体', '服务器规则', '本地规则', 'server-field', 'local-field'])
      assert.match(h.text(), new RegExp(visible), `comparison must show ${visible}`)
    const previous = fixture.previews()
    await h.bindings.previewTemplateCopy()
    assert.equal(fixture.previews(), previous)
    h.bindings.acknowledgeTemplateCopyConflict()
    assert.equal(h.bindings.templateCopyConflict.value, false)
    await h.bindings.previewTemplateCopy()
    assert.equal(h.bindings.templateCopyPreviewCurrent.value, true)
  } finally { h.app.unmount() }
})
test('denied or unavailable effective permission disables editing and retains draft', async () => {
  const fixture = cleanFixture()
  const h = await mount()
  try {
    await flush()
    h.bindings.templateCopyDraft.value.fields[0].label = '保留此输入'
    fixture.deny()
    await h.bindings.refreshTemplateCopyPermissions()
    await flush()
    assert.equal(h.bindings.templateCopyCanEdit.value, false)
    await h.bindings.previewTemplateCopy()
    assert.equal(fixture.previews(), 0)
    assert.equal(h.bindings.templateCopyDraft.value.fields[0].label, '保留此输入')
    assert.match(h.text(), /没有模板编辑权限/)
    const editor = find(h.root, node => node.type === 'fieldset' && textOf(node).includes('项目字段'))
    assert.equal(editor.props.disabled, true)
    fixture.unavailable()
    await h.bindings.refreshTemplateCopyPermissions()
    await flush()
    assert.equal(h.bindings.templateCopyEditPermission.value, 'unknown')
    assert.equal(h.bindings.templateCopyDraft.value.fields[0].label, '保留此输入')
    assert.match(h.text(), /无法确认模板编辑权限/)
  } finally { h.app.unmount() }
})
test('successful upgrade followed by failed project reload is not reported as a failed write', async () => {
  const fixture = cleanFixture()
  apiHandlers.applyChangeSet = async () => ({ data: { id: 'change-1' } })
  apiHandlers.getProject = async () => { throw new Error('read unavailable') }
  const h = await mount()
  try {
    await flush()
    const active = structuredClone(draftProject)
    active.status = 'active'
    h.bindings.project.value = active
    await flush()
    h.bindings.templateCopyDraft.value.fields[0].label = '升级后字段'
    h.bindings.templateUpgradeReason.value = '修改字段'
    h.bindings.pendingChangeSet.value = { id: 'change-1', status: 'assessed', impacts: [], template_upgrade: {} }
    h.bindings.pendingTemplateUpgrade.value = { fingerprint: JSON.stringify(h.bindings.templateCopyDraft.value), reason: '修改字段', valuesFingerprint: '{}', removalsFingerprint: h.bindings.activeArchiveRemovalsFingerprint.value }
    assert.equal(h.bindings.templateCopyCanApply.value, true)
    assert.equal(h.bindings.pendingTemplateUpgradeMatches.value, true)
    await h.bindings.applyTemplateUpgrade()
    assert.match(h.bindings.templateCopyNotice.value, /已应用.*重新读取未完成/)
    assert.doesNotMatch(h.bindings.templateCopyNotice.value, /第 1 版/)
  } finally { h.app.unmount() }
})


test('a late permission result for another project cannot restore editing', async () => {
  cleanFixture()
  const oldPermission = deferred()
  apiHandlers.getEffectivePermission = async (id, capability) => id === draftProject.id
    ? oldPermission.promise
    : { data: { allowed: false, capability, project_version: 2 } }
  const h = await mount()
  try {
    const other = structuredClone(draftProject)
    other.id = 'new-project'
    h.bindings.project.value = other
    await flush()
    assert.equal(h.bindings.templateCopyEditPermission.value, 'denied')
    oldPermission.resolve({ data: { allowed: true, capability: 'manage', project_version: 2 } })
    await flush()
    assert.equal(h.bindings.templateCopyCanEdit.value, false)
    assert.equal(h.bindings.templateCopyEditPermission.value, 'denied')
  } finally { h.app.unmount() }
})

test('a response for the wrong capability cannot enable template editing', async () => {
  cleanFixture()
  apiHandlers.getEffectivePermission = async () => ({ data: { allowed: true, capability: 'read', project_version: 2 } })
  const h = await mount()
  try {
    await flush()
    assert.equal(h.bindings.templateCopyCanEdit.value, false)
    assert.equal(h.bindings.templateCopyEditPermission.value, 'unknown')
  } finally { h.app.unmount() }
})

test('a project read already in flight cannot commit while template save starts', async () => {
  const fixture = cleanFixture()
  const read = deferred(), saved = deferred()
  apiHandlers.getProject = () => read.promise
  apiHandlers.saveTemplateCopyEdit = async () => { fixture.countSave(); return saved.promise }
  const h = await mount()
  try {
    await flush()
    await changedAndPreviewed(h)
    const switching = h.bindings.selectProject('other-project')
    await flush()
    const saving = h.bindings.saveTemplateCopy()
    assert.equal(h.bindings.templateCopyBusy.value, true)
    const other = structuredClone(draftProject)
    other.id = 'other-project'
    read.resolve({ data: other })
    assert.equal(await switching, false)
    assert.equal(h.bindings.project.value.id, draftProject.id)
    const next = structuredClone(draftProject)
    next.project_version = 3
    next.template_copy_version = 2
    next.template_copy.version = 2
    next.template_copy.definition.fields[0].label = '本地修改'
    fixture.setVersion(3)
    saved.resolve({ data: next })
    await saving
    assert.equal(h.bindings.project.value.id, draftProject.id)
    assert.equal(h.bindings.project.value.template_copy_version, 2)
  } finally { h.app.unmount() }
})


test('bound ruleset snapshot or reference disables legacy rule editing; hash alone does not', async () => {
  cleanFixture()
  const h = await mount()
  try {
    await flush()
    const p = h.bindings.project.value
    p.template_copy.definition.rules = [{ rule_id: 'r1', kind: 'presence', severity: 'warning', evaluator: 'required', message: '规则', parameters: { target_id: 'field-a' } }]
    h.bindings.hydrateTemplateCopyDraft(p)
    const fieldset = () => find(h.root, node => node.type === 'fieldset' && textOf(node).includes('声明式规则'))
    for (const kind of ['ruleset_snapshot', 'ruleset_ref']) {
      p.template_copy.definition[kind] = { ruleset_id: 'bound', version: '1' }
      await flush()
      assert.equal(h.bindings.templateCopyHasBoundRuleset.value, true)
      assert.equal(fieldset().props.disabled, true)
      assert.match(h.text(), /当前副本绑定了规则集/)
      const before = h.bindings.templateCopyDraft.value.rules.length
      h.bindings.addTemplateRule('presence')
      assert.equal(h.bindings.templateCopyDraft.value.rules.length, before)
      delete p.template_copy.definition[kind]
    }
    for (const kind of ['rule_set_snapshot', 'template_version_ruleset_ref']) {
      p.template_copy[kind] = { ruleset_id: 'bound', version: '1' }
      await flush()
      assert.equal(h.bindings.templateCopyHasBoundRuleset.value, true)
      assert.equal(fieldset().props.disabled, true)
      const before = h.bindings.templateCopyDraft.value.rules.length
      h.bindings.addTemplateRule('presence')
      assert.equal(h.bindings.templateCopyDraft.value.rules.length, before)
      delete p.template_copy[kind]
    }
    p.template_copy.ruleset_hash = 'hash-alone-is-not-a-binding'
    await flush()
    assert.equal(h.bindings.templateCopyHasBoundRuleset.value, false)
    assert.equal(fieldset().props.disabled, false)
  } finally { h.app.unmount() }
})


test('409 reload with unchanged editable structure but different ruleset/hash requires explicit comparison', async () => {
  const fixture = cleanFixture()
  apiHandlers.saveTemplateCopyEdit = async () => { throw { status: 409, error: { code: 'version_conflict' } } }
  const h = await mount()
  try {
    await flush()
    const base = h.bindings.project.value
    base.template_copy.id = 'copy-old'
    base.template_copy.source_template_id = 'source-old'
    base.template_copy.source_template_version = '1'
    base.template_copy.content_hash = 'content-old'
    base.template_copy.ruleset_hash = 'rules-old'
    base.template_copy.definition.ruleset_ref = { ruleset_id: 'ruleset-old', version: '1', ruleset_hash: 'rules-old' }
    base.template_copy.definition.ruleset_binding_overrides = [{ rule_id: 'r1', severity: 'warning' }]
    const latest = structuredClone(JSON.parse(JSON.stringify(base)))
    latest.project_version = 3
    latest.template_copy.id = 'copy-new'
    latest.template_copy.version = 2
    latest.template_copy_version = 2
    latest.template_copy.content_hash = 'content-new'
    latest.template_copy.ruleset_hash = 'rules-new'
    latest.template_copy.definition.ruleset_ref = { ruleset_id: 'ruleset-new', version: '2', ruleset_hash: 'rules-new' }
    latest.template_copy.definition.ruleset_binding_overrides = [{ rule_id: 'r1', severity: 'error' }]
    apiHandlers.getProject = async () => ({ data: latest })
    await changedAndPreviewed(h)
    await h.bindings.saveTemplateCopy()
    fixture.setVersion(3)
    await h.bindings.reloadTemplateCopyPreservingDraft()
    await flush()
    assert.equal(h.bindings.templateCopyConflict.value, true)
    assert.equal(h.bindings.templateCopyLatestLoaded.value, true)
    assert.equal(h.bindings.templateCopyDraft.value.fields[0].label, '本地修改')
    for (const visible of ['copy-old', 'copy-new', 'source-old', 'content-old', 'content-new', 'rules-old', 'rules-new', 'ruleset-old', 'ruleset-new', 'warning', 'error'])
      assert.match(h.text(), new RegExp(visible), `comparison must show ${visible}`)
    const count = fixture.previews()
    await h.bindings.previewTemplateCopy()
    assert.equal(fixture.previews(), count)
  } finally { h.app.unmount() }
})


test('409 reload with missing server identity cannot automatically mark the copy unchanged', async () => {
  const fixture = cleanFixture()
  apiHandlers.saveTemplateCopyEdit = async () => { throw { status: 409, error: { code: 'version_conflict' } } }
  const h = await mount()
  try {
    await flush()
    delete h.bindings.project.value.template_copy.ruleset_hash
    const latest = structuredClone(JSON.parse(JSON.stringify(h.bindings.project.value)))
    latest.project_version = 3
    apiHandlers.getProject = async () => ({ data: latest })
    await changedAndPreviewed(h)
    await h.bindings.saveTemplateCopy()
    fixture.setVersion(3)
    await h.bindings.reloadTemplateCopyPreservingDraft()
    await flush()
    assert.equal(h.bindings.templateCopyConflict.value, true)
    assert.equal(h.bindings.templateCopyLatestLoaded.value, true)
    assert.match(h.text(), /服务器信息不足以确认其未变/)
  } finally { h.app.unmount() }
})


test('409 comparison exposes actual top-level policy and source ruleset changes', async () => {
  const fixture = cleanFixture()
  apiHandlers.saveTemplateCopyEdit = async () => { throw { status: 409, error: { code: 'version_conflict' } } }
  const h = await mount()
  try {
    await flush()
    const base = h.bindings.project.value
    base.template_copy.source_ruleset_id = 'source-set-old'
    base.template_copy.source_ruleset_version = '1'
    base.template_copy.source_ruleset_hash = 'source-hash-old'
    base.template_copy.source_policy_hash = 'policy-hash-old'
    base.template_copy.binding_policies = [{ rule_id: 'policy-old', allowed_severities: ['warning'] }]
    base.template_copy.rule_set_snapshot = { ruleset_id: 'snapshot-old', version: '1' }
    base.template_copy.template_version_ruleset_ref = { ruleset_id: 'version-ref-old', version: '1' }
    const latest = structuredClone(JSON.parse(JSON.stringify(base)))
    latest.project_version = 3
    latest.template_copy.source_ruleset_id = 'source-set-new'
    latest.template_copy.source_ruleset_version = '2'
    latest.template_copy.source_ruleset_hash = 'source-hash-new'
    latest.template_copy.source_policy_hash = 'policy-hash-new'
    latest.template_copy.binding_policies = [{ rule_id: 'policy-new', allowed_severities: ['error'] }]
    latest.template_copy.rule_set_snapshot = { ruleset_id: 'snapshot-new', version: '2' }
    latest.template_copy.template_version_ruleset_ref = { ruleset_id: 'version-ref-new', version: '2' }
    apiHandlers.getProject = async () => ({ data: latest })
    await changedAndPreviewed(h)
    await h.bindings.saveTemplateCopy()
    fixture.setVersion(3)
    await h.bindings.reloadTemplateCopyPreservingDraft()
    await flush()
    assert.equal(h.bindings.templateCopyConflict.value, true)
    for (const visible of ['source-set-old', 'source-set-new', 'source-hash-old', 'source-hash-new', 'policy-hash-old', 'policy-hash-new', 'policy-old', 'policy-new', 'snapshot-old', 'snapshot-new', 'version-ref-old', 'version-ref-new'])
      assert.match(h.text(), new RegExp(visible), `comparison must show ${visible}`)
  } finally { h.app.unmount() }
})
