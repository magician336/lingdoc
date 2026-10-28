import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import { createSSRApp } from 'vue'
import { renderToString } from '@vue/server-renderer'
import ts from 'typescript'

// Spec #141 TD-05 的前端面：建议标识、上下文范围、接受/拒绝确认、失败恢复
// 与 stale 提示。夹具编译真实 SFC 并注入合成 API 模块，只证明面板状态机，
// 不证明浏览器或服务端行为（与 TemplateGovernancePanel.test.ts 同一形态）。

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./RuleSuggestionPanel.vue', import.meta.url))
const { descriptor, errors } = parse(readFileSync(filename, 'utf8'), { filename })
assert.deepEqual(errors, [])
const compiled = ts.transpileModule(compileScript(descriptor, { id: 'suggestion-panel-test' }).content, {
 compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

// #742 的教训：render: () => null 的夹具绕过模板，按钮 disabled 绑定错了测试照样绿。
// 这份 SSR 模板夹具用真实模板渲染 HTML，把「状态 → 按钮 disabled 投影」钉进测试。
const templateCompiled = compileTemplate({
 source: descriptor.template!.content,
 filename,
 id: 'suggestion-panel-test',
 ssr: true,
})
assert.equal(templateCompiled.errors.length, 0)
const templateJs = ts.transpileModule(templateCompiled.code, {
 compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

function syntheticRun(overrides: Record<string, any> = {}) {
 return {
  id: 'run-1',
  project_id: 'synthetic-project',
  actor_id: 'owner',
  revision: 3,
  status: 'completed',
  scope: {
   project_id: 'synthetic-project',
   template_id: 'template-demo',
   template_version: '1',
   catalog_revision: 7,
   ruleset: { id: 'grant-rules', version: 'v1' },
   target_kind: 'chapter',
   target_id: 'chapter-1',
   source_refs: [],
  },
  output: { proposals: [{ rule_id: 'goal-required' }], explanation: '研究目标缺失必填校验' },
  model_id: 'synthetic-model',
  run_mode: 'fake',
  prompt_version: 'p1',
  tool_names: [],
  input_tokens: 0,
  ...overrides,
 }
}

async function fixture(api: Record<string, any> = {}) {
 const exports: any = {}
 runInNewContext(compiled, {
  exports,
  require(name: string) {
   if (name === 'vue') return require('vue')
   if (name === '@/api/lingdoc/candidateAdoption') return { newIdempotencyKey: () => 'synthetic-key' }
   if (name === '@/api/lingdoc/templateGovernance') return api
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
  canManageSuggestions: true,
 }))
 return controller
}

// 与 fixture 同一合成 API,但用真实模板渲染:ctx 由控制器解包视图构造,
// 不经 Vue 实例装配 —— 测的就是「状态 → 按钮 disabled」这条投影(#742 的断点)。
async function renderedFixture(api: Record<string, any> = {}) {
 const exports: any = {}
 const injectRequire = (name: string) => {
  if (name === 'vue') return require('vue')
  if (name === 'vue/server-renderer') return require('vue/server-renderer')
  if (name === '@/api/lingdoc/candidateAdoption') return { newIdempotencyKey: () => 'synthetic-key' }
  if (name === '@/api/lingdoc/templateGovernance') return api
  throw new Error(`Unexpected test dependency: ${name}`)
 }
 runInNewContext(compiled, { exports, require: injectRequire })
 const templateExports: any = {}
 runInNewContext(templateJs, { exports: templateExports, require: injectRequire })
 let controller: any
 const component = {
  ...exports.default,
  setup(props: any, context: any) { controller = exports.default.setup(props, context); return controller },
  render: () => null,
 }
 await renderToString(createSSRApp(component, {
  projectId: 'synthetic-project',
  canManageSuggestions: true,
 }))
 const html = async () => {
  const ctx = {
   projectId: 'synthetic-project',
   canManageSuggestions: true,
   runs: controller.runs.value,
   failure: controller.failure.value,
   loading: controller.loading.value,
   busy: controller.busy.value,
   pendingAction: controller.pendingAction.value,
   pendingRunId: controller.pendingRunId.value,
   isPending: controller.isPending,
   isStale: controller.isStale,
   staleCount: controller.staleCount.value,
   suggestionCount: controller.suggestionCount.value,
   cancelPending: controller.cancelPending,
   act: controller.act,
  }
  let out = ''
  templateExports.ssrRender(ctx, (chunk: string) => { out += chunk }, null, {})
  return out
 }
 return { controller, html }
}


function buttonTag(page: string, action: string): string {
 return page.match(new RegExp(`<button[^>]*data-action="${action}"[^>]*>`))?.[0] ?? ''
}

test('面板加载后展示建议标识与建议边界（模板/RuleSet/目标）', async () => {
 const calls: string[] = []
 const controller = await fixture({
  listRuleSuggestions: async () => { calls.push('list'); return { data: [syntheticRun()] } },
 })
 assert.deepEqual(calls, [])
 await controller.load()
 assert.deepEqual(calls, ['list'])
 assert.equal(controller.suggestionCount.value, 1)
 assert.equal(controller.runs.value[0].status, 'completed')
 assert.match(controller.runs.value[0].scope.template_id, /template-demo/)
 assert.match(controller.runs.value[0].scope.ruleset.id, /grant-rules/)
})

test('接受必须经过显式确认：未确认只置位不发请求，确认后才落草稿', async () => {
 const accepted: any[] = []
 let run = syntheticRun()
 const controller = await fixture({
  listRuleSuggestions: async () => ({ data: [run] }),
  acceptRuleSuggestion: async (_projectId: string, _runId: string, input: any) => {
   accepted.push(input)
   run = { ...run, status: 'accepted' }
   return { data: run }
  },
 })
 await controller.load()
 await controller.act(controller.runs.value[0], 'accept', {})
 assert.equal(accepted.length, 0, '未确认时不得发请求')
 assert.equal(controller.pendingAction.value, 'accept')
 await controller.act(controller.runs.value[0], 'accept', { bindings: [{ rule_id: 'goal-required' }] })
 assert.equal(accepted.length, 1)
 assert.equal(accepted[0].expected_revision, 3)
 assert.equal(controller.runs.value[0].status, 'accepted')
 assert.equal(controller.pendingAction.value, '', '执行后确认态清除')
})

test('取消清空确认态且不发任何请求', async () => {
 const accepted: any[] = []
 const controller = await fixture({
  listRuleSuggestions: async () => ({ data: [syntheticRun()] }),
  acceptRuleSuggestion: async () => { accepted.push('called') },
 })
 await controller.load()
 await controller.act(controller.runs.value[0], 'accept', {})
 controller.cancelPending()
 assert.equal(controller.pendingAction.value, '')
 await controller.act(controller.runs.value[0], 'accept', { bindings: [] })
 assert.deepEqual(accepted, [], '取消后重新置位，仍需再次确认才发请求')
})

test('失败原因保留并可重试：加载失败后重试成功即恢复', async () => {
 let fail = true
 const controller = await fixture({
  listRuleSuggestions: async () => {
   if (fail) throw { error: { code: 'suggestion_unavailable', message: '建议服务暂不可用' } }
   return { data: [syntheticRun()] }
  },
 })
 await controller.load()
 assert.match(controller.failure.value, /建议服务暂不可用/)
 assert.equal(controller.suggestionCount.value, 0, '失败不清空面板状态')
 fail = false
 await controller.load()
 assert.equal(controller.failure.value, '')
 assert.equal(controller.suggestionCount.value, 1)
})

test('接受失败时保留失败原因，run 状态不被改写', async () => {
 const controller = await fixture({
  listRuleSuggestions: async () => ({ data: [syntheticRun()] }),
  acceptRuleSuggestion: async () => { throw { error: { message: '规则校验未通过' } } },
 })
 await controller.load()
 await controller.act(controller.runs.value[0], 'accept', {})
 await controller.act(controller.runs.value[0], 'accept', { bindings: [] })
 assert.match(controller.failure.value, /规则校验未通过/)
 assert.equal(controller.runs.value[0].status, 'completed', '失败的接受不得改写本地状态')
})

test('stale 建议显示过期提示且接受/拒绝被阻止', async () => {
 const accepted: any[] = []
 const controller = await fixture({
  listRuleSuggestions: async () => ({ data: [syntheticRun({ status: 'stale' })] }),
  acceptRuleSuggestion: async () => { accepted.push('called') },
  rejectRuleSuggestion: async () => { accepted.push('called') },
 })
 await controller.load()
 assert.equal(controller.staleCount.value, 1)
 assert.equal(controller.isStale(controller.runs.value[0]), true)
 await controller.act(controller.runs.value[0], 'accept', {})
 await controller.act(controller.runs.value[0], 'reject', {})
 assert.equal(accepted.length, 0, 'stale 建议不得进入确认流程')
 assert.match(controller.failure.value, /已过期/)
})

test('确认态经真实模板渲染：当前动作保持可点，对侧禁用（#742 回归）', async () => {
 const accepted: any[] = []
 const { controller, html } = await renderedFixture({
  listRuleSuggestions: async () => ({ data: [syntheticRun()] }),
  acceptRuleSuggestion: async (_projectId: string, _runId: string, input: any) => {
   accepted.push(input)
   return { data: syntheticRun({ status: 'accepted' }) }
  },
 })
 await controller.load()

 const initial = await html()
 assert.ok(buttonTag(initial, 'accept') && !/disabled/.test(buttonTag(initial, 'accept')),
  '初始接受按钮必须可用')
 assert.ok(buttonTag(initial, 'reject') && !/disabled/.test(buttonTag(initial, 'reject')),
  '初始拒绝按钮必须可用')
 assert.ok(!/data-action="cancel"/.test(initial), '初始无取消入口')

 await controller.act(controller.runs.value[0], 'accept', {})
 const pending = await html()
 const acceptTag = buttonTag(pending, 'accept')
 assert.ok(acceptTag && !/disabled/.test(acceptTag),
  '确认态下当前动作（接受）必须保持可点，否则第二次确认无法点击——#742')
 assert.ok(/disabled/.test(buttonTag(pending, 'reject')),
  '确认态下对侧动作（拒绝）必须禁用')
 assert.match(pending, /确认接受（落草稿）/)
 assert.ok(/data-action="cancel"/.test(pending), '确认态出现取消入口')

 await controller.act(controller.runs.value[0], 'accept', { bindings: [{ rule_id: 'goal-required' }] })
 assert.equal(accepted.length, 1, '确认态下二次点击必须能到达 API')
 const done = await html()
 assert.ok(!/确认接受（落草稿）/.test(done), '执行后文案回退')
 assert.ok(!/data-action="cancel"/.test(done), '执行后取消入口消失')
 assert.ok(buttonTag(done, 'reject') && !/disabled/.test(buttonTag(done, 'reject')),
  '执行后对侧按钮恢复可用')
})
