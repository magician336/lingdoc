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

// Spec #142 TD-06 的前端面：任务进度、冲突恢复、失败消息、历史/当前标签，
// 以及取消不产生任何写请求。夹具编译真实 SFC 并注入合成 API 模块（与
// TemplateGovernancePanel.test.ts 同一形态），只证明面板状态机。

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./TemplateUpgradeProgressPanel.vue', import.meta.url))
const { descriptor, errors } = parse(readFileSync(filename, 'utf8'), { filename })
assert.deepEqual(errors, [])
const compiled = ts.transpileModule(compileScript(descriptor, { id: 'upgrade-panel-test' }).content, {
 compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

function syntheticChange(overrides: Record<string, any> = {}) {
 return {
  id: 'change-1',
  project_id: 'synthetic-project',
  status: 'applied',
  reason: '模板升级验收',
  template_copy_version: 3,
  template_upgrade: {
   base_template_copy_version: 2,
   preview: { missing_required: [], orphaned: [] },
   impact: { affected_chapter_ids: ['chapter-1', 'chapter-2'], affected_field_ids: ['field-1'] },
  },
  ...overrides,
 }
}

function syntheticTask(overrides: Record<string, any> = {}) {
 return {
  id: 'task-1',
  project_id: 'synthetic-project',
  change_set_id: 'change-1',
  target: { kind: 'ChapterVersion', id: 'chapter-1', version: 'v2' },
  target_revision: 2,
  action: 'REVIEW_REQUIRED',
  severity: 'major',
  responsibility_domain: 'B',
  status: 'OPEN',
  version: 1,
  closure_evidence: [],
  created_at: '2026-10-27T00:00:00Z',
  updated_at: '2026-10-27T00:00:00Z',
  ...overrides,
 }
}

async function fixture(api: Record<string, any> = {}, changeOverrides: Record<string, any> = {}) {
 const exports: any = {}
 runInNewContext(compiled, {
  exports,
  require(name: string) {
   if (name === 'vue') return require('vue')
   if (name === '@/api/lingdoc/mainlineChanges') return api
   if (name === '@/api/lingdoc/candidateAdoption') return { newIdempotencyKey: () => 'synthetic-key' }
   throw new Error(`Unexpected test dependency: ${name}`)
  },
 })
 let controller: any
 const component = {
  ...exports.default,
  setup(props: any, context: any) { controller = exports.default.setup(props, context); return controller },
  render: () => null,
 }
 await renderToString(createSSRApp(component, {
  projectId: 'synthetic-project',
  changeId: 'change-1',
  canManage: true,
 }))
 const unwrap = async () => {
  // load() 之后 refs 已赋值；测试直接经 .value 读取。
  await controller.load()
  return controller
 }
 await unwrap()
 return controller
}

const apiCalls: Record<string, number> = {}
function countingApi(overrides: Record<string, any> = {}) {
 apiCalls.complete = 0
 apiCalls.stabilize = 0
 return {
  listMainlineChanges: async () => ({ data: [syntheticChange(overrides.change)] }),
  listImpactTasks: async () => ({ data: overrides.tasks ?? [
   syntheticTask(),
   syntheticTask({ id: 'task-2', target: { kind: 'ChapterVersion', id: 'chapter-2', version: 'v2' }, status: 'DONE' }),
  ] }),
  actOnImpactTask: async (...args: any[]) => {
   apiCalls.complete += 1
   return overrides.actOnImpactTask
    ? await overrides.actOnImpactTask(...args)
    : { data: syntheticTask({ id: 'task-1', status: 'DONE' }) }
  },
  stabilizeRevision: async (...args: any[]) => {
   apiCalls.stabilize += 1
   return overrides.stabilizeRevision
    ? await overrides.stabilizeRevision(...args)
    : { data: { status: 'STABILIZED', blockers: [] } }
  },
 }
}

test('面板展示任务进度（闭环计数）与历史/当前副本标签', async () => {
 const controller = await fixture(countingApi())
 assert.equal(controller.tasks.value.length, 2)
 assert.match(controller.taskProgressLabel.value, /1\/2 项影响任务已闭环/)
 assert.match(controller.historicalLabel.value, /历史副本 v2/)
 assert.match(controller.currentLabel.value, /当前副本 v3/)
 assert.equal(controller.upgradeDetails.value.base_template_copy_version, 2)
})

test('稳定化 BLOCKED 时展示结构化原因，取消只清本地态不发请求', async () => {
 const controller = await fixture(countingApi({
  stabilizeRevision: async () => ({ data: { status: 'BLOCKED', blockers: [
   { code: 'impact_task_open', target_id: 'task-1', reason: 'REVIEW_REQUIRED' },
  ] } }),
 }))
 await controller.stabilize()
 assert.equal(controller.stabilizeBlocked.value.length, 1)
 assert.match(controller.stabilizeBlocked.value[0], /impact_task_open/)
 const stabilizeCallsAfterBlocked = 1
 controller.cancel()
 assert.equal(controller.stabilizeBlocked.value.length, 0, '取消清空 blocked 展示')
 assert.equal(apiCalls.stabilize, stabilizeCallsAfterBlocked, '取消不发稳定化请求')
})

test('版本冲突置冲突标志，恢复动作重新读取服务端状态', async () => {
 let stabilizeCalls = 0
 const controller = await fixture(countingApi({
  stabilizeRevision: async () => {
   stabilizeCalls += 1
   if (stabilizeCalls === 1) {
    throw { error: { code: 'version_conflict', message: '基线已前移' } }
   }
   return { data: { status: 'STABILIZED', blockers: [] } }
  },
 }))
 await controller.stabilize()
 assert.equal(controller.conflict.value, true)
 assert.match(controller.failure.value, /基线已前移/)
 await controller.load()
 assert.equal(controller.conflict.value, false, '恢复即重新读取，冲突标志清除')
 await controller.stabilize()
 assert.equal(controller.stabilizeBlocked.value.length, 0)
})

test('非冲突失败保留失败消息，面板状态不被误改', async () => {
 const controller = await fixture(countingApi({
  stabilizeRevision: async () => { throw { error: { message: '稳定化服务不可用' } } },
 }))
 await controller.stabilize()
 assert.match(controller.failure.value, /稳定化服务不可用/)
 assert.equal(controller.conflict.value, false)
})

test('任务复核闭环走确认两段式，取消不发请求', async () => {
 const controller = await fixture(countingApi())
 const openTask = controller.tasks.value[0]
 await controller.completeTask(openTask)
 assert.equal(controller.pendingConfirmTaskId.value, openTask.id)
 assert.equal(apiCalls.complete, 0, '未确认不发闭环请求')
 controller.cancel()
 await controller.completeTask(openTask)
 await controller.completeTask(openTask)
 assert.equal(apiCalls.complete, 1, '确认后恰好一次闭环请求')
})
