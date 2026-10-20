import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
const require = createRequire(import.meta.url)
const __filename = fileURLToPath(import.meta.url)
const __dirname = require('node:path').dirname(__filename)

// These are mounted Vue watcher tests, not SSR. Product SFC setup/watchers and
// local helpers execute unchanged; templates compile but the host render is a
// stub. HTTP, unrelated child views, lifecycle browser IO and non-target API
// ports are explicit fixtures. No network, model call, browser paint or Go.
// Legacy controls mutate source IN MEMORY only, to reproduce the prior bug;
// they never replace product files and are not a full historical checkout.
const test = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const crypto = require('node:crypto')
const vue = require('vue')
const ts = require('typescript')
const sfc = require('@vue/compiler-sfc')
const frontend = path.resolve(__dirname, '../../..')
const transpiled = new Map()
const compiledSFC = new Map()
const jsonClone = value => JSON.parse(JSON.stringify(value))
const response = data => ({ data, meta: { replayed: false, refresh_required: false }, request_id: 'identity-ci' })
const deferred = () => { let resolve; let reject; const promise = new Promise((a, b) => { resolve = a; reject = b }); return { promise, resolve, reject } }
const hash = value => crypto.createHash('sha256').update(value).digest('hex')
const quiet = async () => { for (let index = 0; index < 3; index++) { await vue.nextTick(); await Promise.resolve() } }
function once(source, before, after) {
  assert.equal(source.split(before).length, 2, 'legacy control must match exactly one current source anchor')
  return source.replace(before, after)
}
function evidence(identity_case, variant, observed) {
  console.log(JSON.stringify({ identity_case, variant, observed }))
}
const legacy = {
  tuple: source => once(source, 'watch([()=>props.project.id,()=>props.currentUserId],', 'watch(()=>[props.project.id,props.currentUserId],'),
  policy: source => once(source, 'watch([()=>props.project.id,()=>props.currentUserId],()=>{policyAsset', 'watch(()=>[props.project.id,props.currentUserId],()=>{policyAsset'),
  navigation: source => once(source, ",draftCandidateInputs:draftCandidatePanelRef.value?.inputsSnapshot??''", ''),
  spec: source => once(source, 'const expected = specEditingRevision.value??project.value.spec_revision', 'const expected = project.value.spec_revision'),
  list: source => {
    assert.ok(source.includes('sequence===readSequence'), 'current candidate reader must guard its read sequence')
    return source.replaceAll('&&sequence===readSequence', '')
  },
}
function project(id = 'p', version = 1, specRevision = 1) {
  return {
    id, name: 'synthetic project', status: 'draft', project_version: version, spec_revision: specRevision,
    current_context_revision: 0, spec: { research_subject: 'original subject', research_goal: 'original goal' },
    template_id: 'template-demo', template_version: '1', template_copy_version: 1,
    members: [{ user_id: 'u', role: 'owner', governance_role: 'owner', status: 'active' },
      { user_id: 'writer', role: 'collaborator', governance_role: 'member', function_roles: ['author'], function_scopes: { author: ['chapter'] }, status: 'active' }],
    template_copy: { id: 'copy-' + id, version: 1, status: 'draft', definition: { id: 'template-demo', version: '1',
      fields: [{ field_id: 'research_subject', label: '主题', type: 'string' }, { field_id: 'research_goal', label: '目标', type: 'string' }],
      sections: [], terms: [], rules: [] } },
  }
}
const host = vue.createRenderer({
  createElement: type => ({ type, children: [], parent: null }),
  createText: text => ({ text, children: [], parent: null }),
  createComment: text => ({ text, children: [], parent: null }),
  setElementText(node, text) { node.text = text },
  setText(node, text) { node.text = text },
  insert(node, parent, anchor = null) {
    if (node.parent) node.parent.children.splice(node.parent.children.indexOf(node), 1)
    const at = anchor ? parent.children.indexOf(anchor) : -1
    parent.children.splice(at < 0 ? parent.children.length : at, 0, node); node.parent = parent
  },
  remove(node) { if (node.parent) node.parent.children.splice(node.parent.children.indexOf(node), 1); node.parent = null },
  parentNode: node => node.parent, nextSibling: () => null, patchProp() {},
})
function fixture() {
  const apps = [], calls = [], cache = new Map(), answers = new Map()
  const user = vue.ref({ id: 'u', name: 'original user' }), tenant = vue.ref({ id: 1, name: 'original tenant' })
  const auth = vue.reactive({ currentUserId: vue.computed(() => user.value.id), effectiveTenantId: vue.computed(() => tenant.value.id) })
  const storage = new Map(), prompts = []
  const f = { apps, calls, answers, user, tenant, auth, prompts, currentProject: project(), confirm: () => true, get: null, put: null, post: null, ports: {} }
  global.localStorage = global.sessionStorage = { getItem: key => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value), removeItem: key => storage.delete(key) }
  global.HTMLElement = class {}
  global.document = { activeElement: null, addEventListener() {}, removeEventListener() {} }
  global.window = { matchMedia: () => ({ matches: false, addEventListener() {} }), confirm: message => { prompts.push(message); return f.confirm(message) } }
  const request = {
    get: async (url, config) => {
      calls.push({ method: 'GET', url, config })
      if (answers.has(url)) { const answer = answers.get(url); return typeof answer === 'function' ? answer(config) : answer }
      if (f.get) { const result = f.get(url, config); if (result !== undefined) return result }
      if (url.endsWith('/access-status')) return response(null)
      if (url.endsWith('/permissions')) return response({ allowed: false, project_version: f.currentProject.project_version })
      if (url === '/api/v1/lingdoc/projects') return response({ items: [], truncated: false })
      return response([])
    },
    put: async (url, body, config) => {
      calls.push({ method: 'PUT', url, body: jsonClone(body), config })
      return f.put ? f.put(url, body, config) : response(jsonClone(f.currentProject))
    },
    post: async (url, body, config) => {
      calls.push({ method: 'POST', url, body: jsonClone(body), config })
      return f.post ? f.post(url, body, config) : response({ id: 'saved', project_id: f.currentProject.id, ...body })
    },
  }
  const ports = new Proxy({}, { get: (_, name) => (...args) => {
    calls.push({ method: String(name), args })
    if (f.ports[name]) return f.ports[name](...args)
    if (name === 'newIdempotencyKey') return crypto.randomUUID()
    if (name === 'getCollaborationProject') return Promise.resolve(response(jsonClone(f.currentProject)))
    if (name === 'listKnowledgeBases' || name === 'listReleases') return Promise.resolve(response({ items: [] }))
    return Promise.resolve(response([]))
  } })
  function evaluate(source, filename) {
    const outputKey = filename + hash(source)
    if (!transpiled.has(outputKey)) transpiled.set(outputKey, ts.transpileModule(source, { fileName: filename, compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText)
    const box = { exports: {} }
    function load(id) {
      if (id === 'vue') return { ...vue, onMounted: () => {}, onBeforeUnmount: () => {}, onUnmounted: () => {} }
      if (id === 'vue-router') return { onBeforeRouteLeave: () => {} }
      if (id === '@/stores/auth') return { useAuthStore: () => auth }
      if (id === '@/utils/request') return request
      if (id.endsWith('.vue')) return { __esModule: true, default: { render: () => null } }
      if (['@/api/lingdoc/workspace', '@/api/lingdoc/permissions', '@/api/lingdoc/g1Initialization'].includes(id)) return local(path.join(frontend, 'src', id.slice(2) + '.ts'))
      if (id.startsWith('@/api/')) return ports
      if (id.startsWith('.')) return local(path.resolve(path.dirname(filename), id + (path.extname(id) ? '' : '.ts')))
      return require(id)
    }
    new Function('require', 'module', 'exports', transpiled.get(outputKey))(load, box, box.exports)
    return box.exports
  }
  function local(filename) {
    if (!cache.has(filename)) cache.set(filename, evaluate(fs.readFileSync(filename, 'utf8'), filename))
    return cache.get(filename)
  }
  f.component = (name, mutation) => {
    const filename = path.join(__dirname, name), actual = fs.readFileSync(filename, 'utf8'), source = mutation ? mutation(actual) : actual
    const key = filename + hash(source)
    if (!compiledSFC.has(key)) {
      const parsed = sfc.parse(source, { filename }); assert.deepEqual(parsed.errors, [])
      const script = sfc.compileScript(parsed.descriptor, { id: 'firstpass-identity-ci' })
      const template = sfc.compileTemplate({ source: parsed.descriptor.template.content, filename, id: 'firstpass-identity-ci', compilerOptions: { bindingMetadata: script.bindings } })
      assert.deepEqual(template.errors, [])
      compiledSFC.set(key, script.content)
    }
    return { definition: evaluate(compiledSFC.get(key), filename).default, source, source_hash: hash(actual) }
  }
  f.mount = (name, props, mutation) => {
    const component = f.component(name, mutation); let state
    const wrapped = { ...component.definition, setup(p, context) { state = component.definition.setup(p, context); return state }, render: () => vue.h('identity-test-host') }
    const app = host.createApp({ render: () => vue.h(wrapped, props) }); app.mount({ children: [] }); apps.push(app)
    return { get state() { return state }, props, source_hash: component.source_hash }
  }
  f.workspace = mutation => {
    const component = f.component('Workspace.vue', mutation); let state
    assert.match(component.source, /<DraftCandidatePanel ref="draftCandidatePanelRef"/, 'the production parent must wire the exposed snapshot ref')
    const wrapped = { ...component.definition, setup(p, context) { state = component.definition.setup(p, context); return state }, render: () => vue.h('workspace-test-host') }
    const app = host.createApp(wrapped); app.mount({ children: [] }); apps.push(app)
    const panel = f.component('DraftCandidatePanel.vue'); let panelState
    const wrappedPanel = { ...panel.definition, setup(p, context) { panelState = panel.definition.setup(p, context); return panelState }, render: () => vue.h('candidate-test-host') }
    const panelApp = host.createApp({ render: () => state.project.value ? vue.h(wrappedPanel, {
      project: state.project.value, currentUserId: auth.currentUserId, readonly: false,
      ref: instance => { state.draftCandidatePanelRef.value = instance },
      onDirty: value => { state.draftCandidateDirty.value = value },
    }) : null })
    panelApp.mount({ children: [] }); apps.push(panelApp)
    return { state, get panel() { return panelState }, source_hash: component.source_hash }
  }
  f.install = async (h, p = project()) => {
    f.currentProject = jsonClone(p); h.state.project.value = jsonClone(p)
    h.state.subject.value = p.spec.research_subject; h.state.goal.value = p.spec.research_goal; h.state.extraSpecDraft.value = {}
    h.state.specEditingRevision.value = p.spec_revision; h.state.templateCopyDraft.value = h.state.editableTemplateCopy(p.template_copy.definition)
    h.state.chapters.value = []; await quiet()
  }
  f.close = () => { for (const app of [...apps].reverse()) app.unmount() }
  return f
}

test('candidate same-id project object refresh preserves editable inputs; legacy tuple loses them', async () => {
  for (const mutation of [undefined, legacy.tuple]) {
    const f = fixture()
    try {
      const props = vue.reactive({ project: project(), currentUserId: 'u', readonly: false })
      const h = f.mount('DraftCandidatePanel.vue', props, mutation); await quiet()
      h.state.title.value = 'unsaved title'; h.state.content.value = 'unsaved content'; await quiet()
      props.project = { ...props.project, project_version: 2 }; await quiet()
      const retained = h.state.title.value === 'unsaved title' && h.state.content.value === 'unsaved content'
      evidence('candidate_same_id_refresh', mutation ? 'legacy_control' : 'current', { retained, source_sha256: h.source_hash })
      assert.equal(retained, !mutation)
    } finally { f.close() }
  }
})

test('older candidate list response cannot replace the pool read after a successful save', async () => {
  for (const mutation of [undefined, legacy.list]) {
    const f = fixture(), old = deferred(); let reads = 0
    try {
      f.get = url => url.endsWith('/draft-candidates') ? (++reads === 1 ? old.promise : response([{ id: 'saved', kind: 'manual_note' }])) : undefined
      const h = f.mount('DraftCandidatePanel.vue', vue.reactive({ project: project(), currentUserId: 'u', readonly: false }), mutation); await quiet()
      const loading = h.state.load(); h.state.title.value = 'manual note'; h.state.content.value = 'human text'
      await h.state.save(); assert.deepEqual(h.state.candidates.value.map(row => row.id), ['saved'])
      old.resolve(response([{ id: 'older' }])); await loading
      const ids = h.state.candidates.value.map(row => row.id)
      evidence('candidate_old_list', mutation ? 'legacy_control' : 'current', { ids, source_sha256: h.source_hash })
      assert.deepEqual(ids, mutation ? ['older'] : ['saved'])
      const write = f.calls.find(call => call.method === 'POST')
      assert.equal(write.body.kind, 'manual_note'); assert.equal(Object.hasOwn(write.body, 'provenance'), false)
    } finally { f.close() }
  }
})

test('candidate input typed during project loading requires a second consent before navigation', async () => {
  for (const mutation of [undefined, legacy.navigation]) {
    const f = fixture(), pending = deferred()
    try {
      const h = f.workspace(mutation); await f.install(h)
      h.panel.title.value = 'old'; h.panel.content.value = 'old input'; await quiet()
      f.answers.set('/api/v1/lingdoc/projects/q', pending.promise); f.confirm = () => f.prompts.length === 1
      const switching = h.state.selectProject('q'); await quiet()
      h.panel.title.value = 'new'; h.panel.content.value = 'new input while loading'; await quiet()
      pending.resolve(response(project('q'))); await switching; await quiet()
      const safe = h.state.project.value.id === 'p' && h.panel.content.value === 'new input while loading' && f.prompts.length === 2
      evidence('navigation_snapshot', mutation ? 'legacy_control' : 'current', { safe, prompts: f.prompts.length, project_id: h.state.project.value.id, content: h.panel.content.value, source_sha256: h.source_hash })
      assert.equal(safe, !mutation)
    } finally { f.close() }
  }
})

test('passive spec refresh keeps old CAS; only explicit comparison acknowledgement advances it', async () => {
  for (const mutation of [undefined, legacy.spec]) {
    const f = fixture()
    try {
      const h = f.workspace(mutation); await f.install(h); h.state.subject.value = 'local unsaved subject'
      f.currentProject = project('p', 2, 2); f.currentProject.spec.research_goal = 'collaborator goal'
      f.answers.set('/api/v1/lingdoc/projects/p', response(jsonClone(f.currentProject)))
      f.put = (url, input) => {
        if (input.expected_spec_revision !== f.currentProject.spec_revision) throw Object.assign(new Error('fixture CAS conflict'), { status: 409, code: 'version_conflict' })
        f.currentProject = { ...f.currentProject, spec: { ...input.fields }, spec_revision: f.currentProject.spec_revision + 1, project_version: f.currentProject.project_version + 1 }
        return response(jsonClone(f.currentProject))
      }
      await h.state.refreshFullWorkflowProject(); await h.state.saveConditions()
      const first = f.calls.findLast(call => call.method === 'PUT')
      const preserved = first.body.expected_spec_revision === 1 && h.state.specEditingRevision.value === 1 && h.state.subject.value === 'local unsaved subject' && f.currentProject.spec.research_goal === 'collaborator goal'
      evidence('spec_passive_refresh', mutation ? 'legacy_control' : 'current', { preserved, expected_revision: first.body.expected_spec_revision, server_goal: f.currentProject.spec.research_goal, source_sha256: h.source_hash })
      assert.equal(preserved, !mutation)
      if (!mutation) {
        assert.equal(h.state.specBasisChanged.value, true)
        h.state.acknowledgeCurrentSpecBasis(); await h.state.saveConditions()
        assert.equal(f.calls.findLast(call => call.method === 'PUT').body.expected_spec_revision, 2)
      }
    } finally { f.close() }
  }
})

for (const boundary of ['user', 'tenant']) test('old ' + boundary + ' catalog/inbox/permission responses are rejected after identity change', async () => {
  const f = fixture(), permission = deferred(), catalog = deferred(), inbox = deferred()
  try {
    const h = f.workspace(); await f.install(h)
    f.get = url => url.endsWith('/permissions') ? response({ allowed: true, project_version: 1 }) : undefined
    await h.state.loadChapterPermissions('p', 1, [{ id: 'c' }], 0); assert.equal(h.state.chapterWritePermissions.value.c, true)
    f.get = url => url.endsWith('/permissions') ? permission.promise : undefined
    const oldPermission = h.state.loadChapterPermissions('p', 1, [{ id: 'c' }], 0)
    const oldUser = f.auth.currentUserId, oldTenant = f.auth.effectiveTenantId
    const same = () => f.auth.currentUserId === oldUser && f.auth.effectiveTenantId === oldTenant
    f.ports.listTemplateProfiles = async () => response(same() ? [{ template_id: 'old-tenant-template' }] : [])
    f.ports.listTemplateVersions = () => catalog.promise
    f.ports.listMyProjectInvitations = () => same() ? inbox.promise : Promise.resolve(response([]))
    const oldCatalog = h.state.loadPublishedTemplates(), oldInbox = h.state.loadInvitationInbox(); await quiet()
    if (boundary === 'user') f.user.value = { id: 'new-user', name: 'new' }; else f.tenant.value = { id: 2, name: 'new tenant' }
    await quiet()
    permission.resolve(response({ allowed: true, project_version: 1 }))
    catalog.resolve(response([{ template_id: 'old-tenant-template', version: '1', state: 'published', definition: { name: 'private old template' } }]))
    inbox.resolve(response([{ id: 'old-invitation' }]))
    await Promise.all([oldPermission, oldCatalog, oldInbox]); await quiet()
    assert.equal(h.state.project.value, null); assert.deepEqual(h.state.chapterWritePermissions.value, {})
    assert.deepEqual(h.state.publishedTemplates.value, []); assert.deepEqual(h.state.myInvitations.value, [])
    evidence('late_' + boundary, 'current', { rejected: true, source_sha256: h.source_hash })
  } finally { f.close() }
})

test('same user/tenant value-object refresh and same-project metadata refresh preserve drafts', async () => {
  const f = fixture()
  try {
    const h = f.workspace(); await f.install(h)
    h.state.subject.value = 'unsaved conditions'; h.panel.title.value = 'manual'; h.panel.content.value = 'unsaved candidate'; await quiet()
    f.user.value = { ...f.user.value, name: 'fresh actor DTO' }; f.tenant.value = { ...f.tenant.value, name: 'fresh tenant DTO' }
    h.state.project.value = { ...h.state.project.value, project_version: 2 }; await quiet()
    assert.equal(h.state.project.value.id, 'p'); assert.equal(h.state.subject.value, 'unsaved conditions')
    assert.equal(h.panel.content.value, 'unsaved candidate'); assert.equal(h.state.specEditingRevision.value, 1)
    evidence('same_identity_dto', 'current', { retained: true, source_sha256: h.source_hash })
  } finally { f.close() }
})

test('G2 same-project refresh preserves agent/task input and asset-policy purpose', async () => {
  for (const mutation of [undefined, legacy.policy]) {
    const f = fixture()
    try {
      const props = vue.reactive({ project: { ...project(), status: 'active', current_context_revision: 1 }, currentUserId: 'u', readonly: false, selectedEvidence: [] })
      const h = f.mount('G2ResearchPanel.vue', props, mutation); await quiet()
      h.state.taskForm.goal = 'draft research goal'; h.state.userInput.value = 'draft agent prompt'; h.state.policyForm.purpose = 'draft asset purpose'
      props.project = { ...props.project, project_version: 2 }; await quiet()
      assert.equal(h.state.taskForm.goal, 'draft research goal'); assert.equal(h.state.userInput.value, 'draft agent prompt')
      const retained = h.state.policyForm.purpose === 'draft asset purpose'
      evidence('g2_policy_tuple', mutation ? 'legacy_control' : 'current', { retained, source_sha256: h.source_hash })
      assert.equal(retained, !mutation)
    } finally { f.close() }
  }
})

test('collaboration same-project refresh leaves the assignment drawer open and its draft basis pinned', async () => {
  for (const mutation of [undefined, legacy.tuple]) {
    const f = fixture()
    try {
      const props = vue.reactive({ project: project(), currentUserId: 'u', readonly: false, chapters: [], assets: [] })
      const h = f.mount('ProjectCollaborationPanel.vue', props, mutation); await quiet()
      h.state.openAssignment(props.project.members[1]); h.state.editor.value.reason = 'unsaved assignment reason'; await quiet()
      f.currentProject = project('p', 2); props.project = jsonClone(f.currentProject); await quiet()
      assert.equal(h.state.editor.value.reason, 'unsaved assignment reason')
      assert.equal(h.state.editor.value.basedOnVersion, 1); assert.equal(h.state.localProject.value.project_version, 2)
      const retained = h.state.drawer.value === 'assignment'
      evidence('g5_drawer_tuple', mutation ? 'legacy_control' : 'current', { retained, cached_reason_retained: true, source_sha256: h.source_hash })
      assert.equal(retained, !mutation)
    } finally { f.close() }
  }
})

test('impact panel same-project object refresh preserves pending proposed value and reason', async () => {
  const f = fixture()
  try {
    const props = vue.reactive({ project: { ...project(), status: 'active', current_context_revision: 1 }, currentUserId: 'u', readonly: false, preflightReceiptId: '' })
    const h = f.mount('ImpactOpsPanel.vue', props); await quiet()
    h.state.draftValue.value = 'unsaved proposed value'; h.state.reason.value = 'unsaved proposal reason'
    props.project = { ...props.project, project_version: 2 }; await quiet()
    assert.equal(h.state.draftValue.value, 'unsaved proposed value'); assert.equal(h.state.reason.value, 'unsaved proposal reason')
    evidence('impact_metadata_refresh', 'current', { retained: true, source_sha256: h.source_hash })
  } finally { f.close() }
})


test('failed staged project read retains review input; successful consent clears it only at commit', async () => {
  const f = fixture()
  try {
    const h = f.workspace(); await f.install(h); h.state.resetConditionsInputs('p')
    h.state.reviewDrafts.value = { 'chapter:r': { disposition: 'resolved', reason: 'unsaved human reasoning', explicitlyChosen: true } }
    h.state.savedReviewDrafts.value = {}
    f.answers.set('/api/v1/lingdoc/projects/q', response(project('q')))
    f.confirm = () => true
    f.answers.set('/api/v1/lingdoc/projects/q/assets', () => { throw new Error('synthetic staged read failed') })
    await h.state.selectProject('q'); await quiet()
    assert.equal(h.state.project.value.id, 'p')
    assert.equal(h.state.reviewDrafts.value['chapter:r'].reason, 'unsaved human reasoning')
    f.answers.delete('/api/v1/lingdoc/projects/q/assets')
    await h.state.selectProject('q'); await quiet()
    assert.equal(h.state.project.value.id, 'q')
    assert.deepEqual(h.state.reviewDrafts.value, {})
    assert.deepEqual(h.state.savedReviewDrafts.value, {})
  } finally { f.close() }
})

test('handled spec receipt cannot release a changed custom field or an editor draft', async () => {
  const f = fixture()
  try {
    const h = f.workspace(); await f.install(h); h.state.resetConditionsInputs('p')
    h.state.extraSpecDraft.value = { custom: 'submitted custom value' }
    const handled = { subject: h.state.subject.value, goal: h.state.goal.value,
      reason: '', chapterIds: [], fieldsFingerprint: JSON.stringify(h.state.specDraftFields.value) }
    assert.equal(h.state.navigationInputsAreDirty(handled), false)
    h.state.extraSpecDraft.value = { custom: 'new input after request' }
    assert.equal(h.state.navigationInputsAreDirty(handled), true)
    h.state.extraSpecDraft.value = { custom: 'submitted custom value' }
    h.state.editorDirtyAny.value = true
    assert.equal(h.state.navigationInputsAreDirty(handled), true)
  } finally { f.close() }
})
