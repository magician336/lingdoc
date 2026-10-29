import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'

const require = createRequire(import.meta.url)
const filename = fileURLToPath(import.meta.url)
const path = require('node:path')
const fs = require('node:fs')
const Module = require('node:module')
const assert = require('node:assert/strict')
const test = require('node:test')
const frontend = path.resolve(path.dirname(filename), '../../..')
const load = Module.createRequire(path.join(frontend, 'package.json'))
const ts = load('typescript')
const compiler = load('@vue/compiler-sfc')
const vue = load('vue')
const { renderToString } = load('@vue/server-renderer')

const clone = value => JSON.parse(JSON.stringify(value))
const envelope = data => ({ data: clone(data) })
let fixture

function result(projectVersion = 4) {
  return {
    project_version: projectVersion,
    status: 'blocked',
    ruleset_hash: 'effective-hash',
    ruleset_ref: { ruleset_id: 'rules-1', version: 'r2', ruleset_hash: 'source-hash' },
    issues: [{
      id: 'i1', rule_id: 'rule-1', ruleset_hash: 'effective-hash', severity: 'blocking',
      target_id: 'chapter-1', target_version: 'chapter-v2',
      evidence: ['固定证据'], evaluator_version: 'engine-v1', message: '必填内容缺失',
    }],
  }
}
function templateResult(projectVersion = 4) {
  return {
    project_version: projectVersion, status: 'blocked', template_copy_version: 3,
    content_hash: 'template-hash', ruleset_hash: 'effective-hash',
    ruleset_ref: { ruleset_id: 'rules-1', version: 'r2', ruleset_hash: 'source-hash' },
    evaluations: [{
      id: 'e1', rule_id: 'rule-1', target_ref: 'chapter-1', target_version: 'chapter-v2',
      ruleset_hash: 'effective-hash', status: 'ISSUE', severity: 'blocking',
      message: '模板内容缺失', evidence: ['模板证据'], evaluator_version: 'engine-v1',
    }],
  }
}
function snapshot() {
  return {
    id: 'snapshot-1', project_id: 'p1', snapshot_digest: 'snapshot-digest',
    frozen_input: {
      project_id: 'p1', project_name: '课题', project_version: 4, spec_revision: 1,
      chapters: [], sources: [], template: { id: 'template-1', version: 'v1' },
      template_copy_version: 3, template_copy_content_hash: 'template-hash',
    },
    check: result(), is_current: true, created_at: '2026-10-27T00:00:00Z',
  }
}
function props() {
  return {
    project: {
      id: 'p1', name: '课题', status: 'draft', project_version: 4,
      template_id: 'template-1', template_version: 'v1', template_copy_version: 3,
      template_copy: { content_hash: 'template-hash', ruleset_hash: 'effective-hash' },
      members: [{ user_id: 'owner', role: 'owner', status: 'active' }],
    },
    chapters: [{ id: 'chapter-1', title: '研究方法', current_version_id: 'chapter-v2', confirmation_valid: true }],
  }
}
function reset() {
  fixture = { calls: [], listReleasesError: null, checkError: null, templateError: null,
    getRelease: null, checkTemplate: null, startExportError: null }
}
const api = {
  listReleases: async () => {
    fixture.calls.push('listReleases')
    if (fixture.listReleasesError) throw fixture.listReleasesError
    return envelope({ items: fixture.releaseItems ?? [snapshot()], truncated: false })
  },
  listExports: async () => envelope({ items: [], truncated: false }),
  getRelease: async (_projectId, id) => fixture.getRelease
    ? fixture.getRelease(id) : envelope(snapshot()),
  checkDelivery: async () => {
    fixture.calls.push('checkDelivery')
    if (fixture.checkError) throw fixture.checkError
    return envelope(result())
  },
  checkTemplate: async () => {
    if (fixture.checkTemplate) return fixture.checkTemplate()
    if (fixture.templateError) throw fixture.templateError
    return envelope(templateResult())
  },
  setIssueDisposition: async (id, issueId, version, action, reason, key) => {
    fixture.calls.push({ action, reason })
    ;(fixture.dispositionKeys ??= []).push({ id, key })
    return fixture.setIssueDisposition ? fixture.setIssueDisposition(id, issueId, version, action, reason, key) : envelope({})
  },
  prepareRelease: async (id, version, key) => { fixture.calls.push('prepareRelease'); (fixture.releaseKeys ??= []).push({ id, key }); return fixture.prepareRelease ? fixture.prepareRelease(id, version, key) : envelope(snapshot()) },
  startExport: async (id, snapshotId, key) => {
    fixture.calls.push('startExport')
    ;(fixture.exportKeys ??= []).push({ id, key })
    if (fixture.startExportError) throw fixture.startExportError
    return fixture.startExport ? fixture.startExport(id, snapshotId, key) : envelope({})
  },
  getExport: async () => envelope({}),
  downloadExport: async () => new Blob(),
  newDeliveryKey: () => 'test-idempotency-key-' + (fixture.keyCounter = (fixture.keyCounter ?? 0) + 1),
}

function compile(filename, source, resolver = load) {
  const module = new Module(filename)
  module.paths = Module._nodeModulePaths(frontend)
  module.require = resolver
  module._compile(ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true },
  }).outputText, filename)
  return module.exports
}

const statePath = path.join(path.dirname(filename), 'deliveryState.ts')
const state = compile(statePath, fs.readFileSync(statePath, 'utf8'))
const panelPath = path.join(path.dirname(filename), 'DeliveryPanel.vue')
const parsed = compiler.parse(fs.readFileSync(panelPath, 'utf8'))
assert.deepEqual(parsed.errors, [])
const script = compiler.compileScript(parsed.descriptor, { id: 'issue-686-delivery' })
const template = compiler.compileTemplate({
  source: parsed.descriptor.template.content, filename: panelPath, id: 'issue-686-delivery',
  compilerOptions: { bindingMetadata: script.bindings }, isTS: true,
})
assert.deepEqual(template.errors, [])
const panel = compile(panelPath, script.content, id => {
  if (id === '@/api/lingdoc/delivery') return api
  if (id === './deliveryState') return state
  return load(id)
}).default
panel.render = compile(panelPath + '.template', template.code).render

async function controller() {
  reset()
  const input = vue.reactive(props())
  const events = []
  const scope = vue.effectScope()
  const state = scope.run(() => panel.setup(input, {
    expose: () => {},
    emit: (event, ...args) => {
      events.push([event, ...args])
      if (event === 'refreshProject') args[0]?.(true)
    },
  }))
  await state.load()
  return { state, input, scope, events }
}

test('the real template renders service rule evidence and the frozen Preflight basis', async () => {
  reset()
  const wrapper = {
    ...panel,
    async setup(input, context) {
      const state = panel.setup(input, context)
      await state.load()
      await state.runCheck()
      await state.runTemplateCheck()
      return state
    },
  }
  const html = await renderToString(vue.createSSRApp(wrapper, props()))
  for (const text of ['Template Check', '冻结时的交付检查', 'rules-1 / r2', 'template-hash',
    'rule-1', 'chapter-v2', '固定证据', '模板证据', 'engine-v1', '当前内容']) {
    assert.ok(html.includes(text), 'missing rendered evidence: ' + text)
  }
})

test('a late template check from an older project basis cannot appear current', async () => {
  const { state, input, scope } = await controller()
  let finish
  fixture.checkTemplate = () => new Promise(resolve => { finish = resolve })
  const pending = state.runTemplateCheck()
  input.project.project_version = 5
  await vue.nextTick()
  finish(envelope(templateResult(4)))
  await pending
  assert.equal(state.templateChecks.value, null)
  assert.match(state.templateCheckNotice.value, /已过期/)
  scope.stop()
})

test('late history and currency responses cannot overwrite the newly read basis', async () => {
  const { state, input, scope } = await controller()
  let finishOld
  let count = 0
  fixture.getRelease = () => {
    count++
    if (count === 1) return new Promise(resolve => { finishOld = resolve })
    return Promise.resolve(envelope({ ...snapshot(), is_current: false }))
  }
  const oldLoad = state.load()
  await new Promise(resolve => setImmediate(resolve))
  input.project.project_version = 5
  await state.load()
  finishOld(envelope(snapshot()))
  await oldLoad
  assert.equal(state.currencyReads.value['snapshot-1'].is_current, false)
  scope.stop()
})

test('recording a disposition reports success even if its follow-up check fails', async () => {
  const { state, scope } = await controller()
  state.checks.value = result()
  state.dispositionReasons.value.i1 = '已修复'
  fixture.checkError = new Error('follow-up check unavailable')
  await state.submitDisposition(result().issues[0])
  assert.match(state.operationNotice.value, /处置已记录/)
  assert.doesNotMatch(state.errorMessage.value, /提交问题处置失败/)
  assert.equal(state.checks.value, null)
  assert.deepEqual(fixture.calls.find(item => typeof item === 'object'), { action: 'resolve', reason: '已修复' })
  scope.stop()
})

test('a successful freeze with failed history readback is not reported as a failed write', async () => {
  const { state, scope } = await controller()
  fixture.listReleasesError = new Error('history unavailable')
  await state.freeze()
  assert.match(state.operationNotice.value, /已冻结.*历史尚未确认/)
  assert.doesNotMatch(state.errorMessage.value, /冻结交付快照失败/)
  assert.equal(fixture.calls.filter(call => call === 'prepareRelease').length, 1)
  scope.stop()
})

test('a permission denial removes prior checks and snapshot currentness', async () => {
  const { state, scope } = await controller()
  state.checks.value = result()
  fixture.templateError = { $httpStatus: 403 }
  await state.runTemplateCheck()
  assert.equal(state.checks.value, null)
  assert.deepEqual(state.currencyReads.value, {})
  assert.equal(state.snapshots.value.length, 0)
  assert.match(state.errorMessage.value, /无权/)
  scope.stop()
})

test('HTTP payload version conflicts trigger project reload without repeating the write', async () => {
  const { state, scope, events } = await controller()
  fixture.checkError = { $httpStatus: 409, error: { code: 'version_conflict' } }
  await state.runCheck()
  assert.ok(events.some(event => event[0] === 'refreshProject'))
  assert.match(state.errorMessage.value, /已重新读取当前版本/)
  assert.equal(fixture.calls.filter(call => call === 'checkDelivery').length, 1)
  scope.stop()
})

test('a background permission denial invalidates an in-flight check even without a new project version', async () => {
  const { state, scope } = await controller()
  let finish
  fixture.checkTemplate = () => new Promise(resolve => { finish = resolve })
  const pending = state.runTemplateCheck()
  fixture.getRelease = () => Promise.reject({ $httpStatus: 403 })
  await state.load()
  finish(envelope(templateResult()))
  await pending
  assert.equal(state.templateChecks.value, null)
  assert.match(state.errorMessage.value, /无权/)
  scope.stop()
})

test('a successful export with failed history readback is not presented as a failed export', async () => {
  const { state, scope } = await controller()
  const item = snapshot()
  item.check.status = 'passed'
  state.currencyReads.value[item.id] = { ...item, is_current: true }
  fixture.listReleasesError = new Error('history unavailable')
  await state.exportSnapshot(item)
  assert.match(state.operationNotice.value, /导出请求已完成.*记录尚未确认/)
  assert.doesNotMatch(state.errorMessage.value, /生成交付文件失败/)
  assert.equal(fixture.calls.filter(call => call === 'startExport').length, 1)
  scope.stop()
})

test('failed history refresh makes previously current snapshots unknown', async () => {
  const { state, scope } = await controller()
  assert.equal(state.currencyReads.value['snapshot-1'].is_current, true)
  fixture.listReleasesError = new Error('history unavailable')
  await state.load()
  assert.equal(state.currencyReads.value['snapshot-1'], undefined)
  scope.stop()
})

test('a failed history read is never rendered as an empty snapshot list', async () => {
  reset()
  fixture.listReleasesError = new Error('交付历史读取失败')
  const wrapper = { ...panel, async setup(input, context) {
    const state = panel.setup(input, context)
    await state.load()
    return state
  } }
  const html = await renderToString(vue.createSSRApp(wrapper, props()))
  assert.match(html, /交付历史读取失败/)
  assert.doesNotMatch(html, /还没有冻结过交付快照/)
})

test('a successfully read empty history has a distinct empty state', async () => {
  reset()
  fixture.releaseItems = []
  const wrapper = { ...panel, async setup(input, context) {
    const state = panel.setup(input, context)
    await state.load()
    return state
  } }
  const html = await renderToString(vue.createSSRApp(wrapper, props()))
  assert.match(html, /还没有冻结过交付快照/)
})

test('a 404 on a protected read clears cached results and invalidates an earlier response', async () => {
  const { state, scope } = await controller()
  state.checks.value = result()
  let finish
  fixture.checkTemplate = () => new Promise(resolve => { finish = resolve })
  const pending = state.runTemplateCheck()
  fixture.getRelease = () => Promise.reject({ $httpStatus: 404 })
  await state.load()
  finish(envelope(templateResult()))
  await pending
  assert.equal(state.templateChecks.value, null)
  assert.equal(state.checks.value, null)
  assert.equal(state.snapshots.value.length, 0)
  assert.match(state.errorMessage.value, /不存在.*无法访问/)
  scope.stop()
})

test('a late denial for the same project invalidates newer content and history', async () => {
  const { state, input, scope } = await controller()
  let rejectRequest
  fixture.checkTemplate = () => new Promise((_, reject) => { rejectRequest = reject })
  const oldRequest = state.runTemplateCheck()
  input.project.project_version = 5
  await vue.nextTick()
  await state.load()
  assert.ok(state.snapshots.value.length > 0)
  rejectRequest({ $httpStatus: 404 })
  await oldRequest
  assert.equal(state.snapshots.value.length, 0)
  assert.equal(state.templateChecks.value, null)
  assert.match(state.errorMessage.value, /不存在.*无法访问/)
  scope.stop()
})

test('switching projects releases the old busy state without letting its completion clear a new request', async () => {
  const { state, input, scope } = await controller()
  const callbacks = []
  fixture.checkTemplate = () => new Promise(resolve => callbacks.push(resolve))
  const oldRequest = state.runTemplateCheck()
  assert.equal(state.pending.value, 'template-check')
  input.project = { ...input.project, id: 'p2' }
  await vue.nextTick()
  assert.equal(state.pending.value, '')
  const newRequest = state.runTemplateCheck()
  assert.equal(callbacks.length, 2)
  callbacks[0](envelope(templateResult()))
  await oldRequest
  assert.equal(state.pending.value, 'template-check')
  callbacks[1](envelope(templateResult()))
  await newRequest
  assert.equal(state.pending.value, '')
  scope.stop()
})

test('a late response after A to B to A cannot overwrite the newer check', async () => {
  const { state, input, scope } = await controller()
  const callbacks = []
  fixture.checkTemplate = () => new Promise(resolve => callbacks.push(resolve))
  const oldRequest = state.runTemplateCheck()
  input.project = { ...input.project, id: 'p2' }
  await vue.nextTick()
  input.project = { ...input.project, id: 'p1' }
  await vue.nextTick()
  const newRequest = state.runTemplateCheck()
  const latest = templateResult(); latest.evaluations[0].message = 'new-result'
  callbacks[1](envelope(latest)); await newRequest
  const stale = templateResult(); stale.evaluations[0].message = 'old-result'
  callbacks[0](envelope(stale)); await oldRequest
  assert.equal(state.templateChecks.value.evaluations[0].message, 'new-result')
  scope.stop()
})

test('a successful old-project freeze does not remove the new-project retry key', async () => {
  const { state, input, scope } = await controller()
  const requests = []
  fixture.prepareRelease = () => new Promise((resolve, reject) => requests.push({ resolve, reject }))
  const first = state.freeze()
  input.project = { ...input.project, id: 'p2' }
  await vue.nextTick()
  const second = state.freeze()
  requests[0].resolve(envelope(snapshot())); await first
  requests[1].reject(new Error('response lost')); await second
  const retry = state.freeze()
  assert.equal(fixture.releaseKeys[1].id, 'p2')
  assert.equal(fixture.releaseKeys[2].id, 'p2')
  assert.equal(fixture.releaseKeys[1].key, fixture.releaseKeys[2].key)
  assert.notEqual(fixture.releaseKeys[0].key, fixture.releaseKeys[1].key)
  requests[2].resolve(envelope(snapshot())); await retry
  scope.stop()
})

test('returning to a project reuses the unresolved freeze key even if the first response is lost', async () => {
  const { state, input, scope } = await controller()
  const requests = []
  fixture.prepareRelease = () => new Promise((resolve, reject) => requests.push({ resolve, reject }))
  const first = state.freeze()
  input.project = { ...input.project, id: 'p2' }; await vue.nextTick()
  input.project = { ...input.project, id: 'p1' }; await vue.nextTick()
  const retry = state.freeze()
  assert.equal(fixture.releaseKeys[0].key, fixture.releaseKeys[1].key)
  requests[0].resolve(envelope(snapshot())); await first
  requests[1].reject(new Error('response lost')); await retry
  const finalRetry = state.freeze()
  assert.equal(fixture.releaseKeys[2].key, fixture.releaseKeys[0].key)
  requests[2].resolve(envelope(snapshot())); await finalRetry
  scope.stop()
})

test('an unresolved export reuses its key after switching away and back', async () => {
  const { state, input, scope } = await controller()
  const requests = []
  fixture.startExport = () => new Promise((resolve, reject) => requests.push({ resolve, reject }))
  const item = snapshot(); item.check.status = 'passed'
  state.currencyReads.value[item.id] = { ...item, is_current: true }
  const first = state.exportSnapshot(item)
  input.project = { ...input.project, id: 'p2' }; await vue.nextTick()
  input.project = { ...input.project, id: 'p1' }; await vue.nextTick()
  state.currencyReads.value[item.id] = { ...item, is_current: true }
  const retry = state.exportSnapshot(item)
  assert.equal(fixture.exportKeys[0].key, fixture.exportKeys[1].key)
  requests[0].resolve(envelope({})); await first
  requests[1].reject(new Error('response lost')); await retry
  state.currencyReads.value[item.id] = { ...item, is_current: true }
  const finalRetry = state.exportSnapshot(item)
  assert.equal(fixture.exportKeys[2].key, fixture.exportKeys[0].key)
  requests[2].resolve(envelope({})); await finalRetry
  scope.stop()
})

test('an unresolved disposition reuses its key after switching away and back', async () => {
  const { state, input, scope } = await controller()
  const requests = []
  fixture.setIssueDisposition = () => new Promise((resolve, reject) => requests.push({ resolve, reject }))
  const issue = result().issues[0]
  state.dispositionReasons.value[issue.id] = '已修复'
  const first = state.submitDisposition(issue)
  input.project = { ...input.project, id: 'p2' }; await vue.nextTick()
  input.project = { ...input.project, id: 'p1' }; await vue.nextTick()
  state.dispositionReasons.value[issue.id] = '已修复'
  const retry = state.submitDisposition(issue)
  assert.equal(fixture.dispositionKeys[0].key, fixture.dispositionKeys[1].key)
  requests[0].resolve(envelope({})); await first
  requests[1].reject(new Error('response lost')); await retry
  const finalRetry = state.submitDisposition(issue)
  assert.equal(fixture.dispositionKeys[2].key, fixture.dispositionKeys[0].key)
  requests[2].resolve(envelope({})); await finalRetry
  scope.stop()
})
