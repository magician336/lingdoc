/**
 * 上下文包面板的界面契约。
 *
 * 这一层断言的是**面板看得见的行为**：它把服务端组装好的那一份原样显示出来，
 * 用户的勾选只当意图；预览失败或版本冲突时事实区清空；卡片版本对不上时说
 * 「状态未知（版本已变）」而不是回退到别的状态。
 *
 * 跑法与仓内既有前端测试一致：Node 内置 node:test + node:assert/strict，
 * 没有 vitest、没有 @vue/test-utils。组件从真实的 .vue 源码现场编译，
 * 门面模块（agentContext.ts）现场装进当前 realm，两者之间只隔一个注入的
 * 假客户端——因此整条链路不打网络，也不碰真实后端。
 *
 * 两条断言是**静态源码断言**：面板不许引传输层（agentApi.ts），只许引门面。
 * 它们读 .vue 文件的源码，不依赖运行时形状，所以引传输层这件事在编译期就
 * 被挡住。
 */
import assert from 'node:assert/strict'
import fs from 'node:fs'
import Module, { createRequire } from 'node:module'
import path from 'node:path'
import test from 'node:test'
import vm from 'node:vm'
import { fileURLToPath } from 'node:url'
import type { AgentContextInput, AgentContextPackage, AgentSourceRef, EvidenceCard, VersionRef } from '@/api/lingdoc/agentApi'
import type { ContextFacts, ContextIntent, ContextPackageClient } from '@/api/lingdoc/agentContext'

const require = createRequire(import.meta.url)
const here = path.dirname(fileURLToPath(import.meta.url))
const frontend = path.resolve(here, '../../..')
const frontendRequire = Module.createRequire(path.join(frontend, 'package.json'))
const ts = frontendRequire('typescript')
const compiler = frontendRequire('@vue/compiler-sfc')
const vue = frontendRequire('vue')
const { renderToString } = frontendRequire('@vue/server-renderer')

type FacadeModule = typeof import('@/api/lingdoc/agentContext')

const PANEL_SOURCE = path.join(here, 'ContextPackagePanel.vue')
const AGENT_API_SOURCE = new URL('../../api/lingdoc/agentApi.ts', import.meta.url)
const AGENT_CONTEXT_SOURCE = new URL('../../api/lingdoc/agentContext.ts', import.meta.url)

/**
 * 装一份门面模块进来：传输层的 HTTP 客户端换成记调用的假件，面面自己的
 * 纯函数一个不动。面板只经由注入的假客户端与服务端说话，所以这里的传输层
 * 假件只负责让模块装得起来。
 */
function loadModule(url: URL, dependencies: Record<string, unknown>): Record<string, unknown> {
  const source = fs.readFileSync(url, 'utf8')
  const js = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true },
  }).outputText
  const exports: Record<string, unknown> = {}
  const module_ = { exports }
  const factory = vm.compileFunction(js, ['exports', 'require', 'module'])
  factory(exports, (id: string) => {
    if (id in dependencies) return dependencies[id]
    throw new Error(`unexpected dependency ${id}`)
  }, module_)
  return exports
}

function loadFacade(): FacadeModule {
  const request = {
    get: () => Promise.resolve({ data: [] }),
    post: () => Promise.resolve({ data: {} }),
    put: () => Promise.resolve({ data: {} }),
  }
  const api = loadModule(AGENT_API_SOURCE, { '@/utils/request': request })
  return loadModule(AGENT_CONTEXT_SOURCE, { './agentApi': api }) as unknown as FacadeModule
}

/** Node 的 CommonJS 加载器内部接口：公开类型里没有这几个成员，测试要自己用。 */
type ModuleInternals = {
  paths: string[]
  require: (id: string) => unknown
  _compile: (code: string, filename: string) => void
  exports: unknown
}

/** 把一段源码装成一个 CommonJS 模块：require 由调用方决定。 */
function compileToModule(filename: string, code: string, resolver?: (id: string) => unknown): Record<string, unknown> {
  const m = new Module(filename) as unknown as ModuleInternals
  m.paths = (Module as unknown as { _nodeModulePaths: (from: string) => string[] })._nodeModulePaths(frontend)
  m.require = (resolver ?? frontendRequire) as ModuleInternals['require']
  m._compile(
    ts.transpileModule(code, {
      compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true },
    }).outputText,
    filename,
  )
  return m.exports as Record<string, unknown>
}

const facade = loadFacade()

const panelSourceText = fs.readFileSync(PANEL_SOURCE, 'utf8')
const parsed = compiler.parse(panelSourceText, { filename: PANEL_SOURCE }) as {
  errors: unknown[]
  descriptor: { template: { content: string }; styles: Array<{ content: string }> }
}
assert.deepEqual(parsed.errors, [])
const script = compiler.compileScript(parsed.descriptor, { id: 'context-package-panel-test' })
const template = compiler.compileTemplate({
  source: parsed.descriptor.template.content,
  filename: PANEL_SOURCE,
  id: 'context-package-panel-test',
  compilerOptions: { bindingMetadata: script.bindings },
  isTS: true,
})
assert.deepEqual(template.errors, [])

/**
 * 面板只许引门面与 vue。
 *
 * 传输层这里也给出件（而不是直接报错）：引了传输层这件事必须由静态源码断言
 * 抓住并说清楚「为什么」——模块加载时炸掉只会让人看见一个栈，看不见那条规矩。
 */
function resolvePanelDependency(id: string): unknown {
  if (id === 'vue') return vue
  if (id === '@/api/lingdoc/agentContext') return facade
  if (id === '@/api/lingdoc/agentApi') return { previewAgentContext: () => Promise.resolve({ data: {} }) }
  throw new Error(`unexpected import ${id}`)
}

const panelModule = compileToModule(PANEL_SOURCE, script.content, resolvePanelDependency)
const panel = panelModule.default as { setup: (props: unknown, context: unknown) => unknown; render?: unknown }
panel.render = (compileToModule(PANEL_SOURCE + '.template', template.code) as { render: unknown }).render

/** 渲染一次面板：props 进，HTML 出。 */
async function renderPanel(props: Record<string, unknown>): Promise<string> {
  const app = vue.createSSRApp(panel, props)
  return await renderToString(app)
}

type PanelState = {
  intent: { value: ContextIntent }
  facts: { value: ContextFacts | null }
  errorMessage: { value: string }
  busy: { value: boolean }
  addCard: (card: VersionRef) => Promise<void>
  removeCard: (cardId: string) => Promise<void>
  preview: () => Promise<void>
  settle: () => Promise<void>
}

/** 打开一个面板实例：观察它的状态，并收下它发出的事件。 */
function openPanel(props: Record<string, unknown>): { state: PanelState; emitted: Array<{ event: string; payload: unknown }> } {
  const emitted: Array<{ event: string; payload: unknown }> = []
  let exposed: Record<string, unknown> = {}
  const returned = panel.setup(props, {
    emit: (event: string, payload: unknown) => { emitted.push({ event, payload }) },
    expose: (value: unknown) => { exposed = value as Record<string, unknown> },
  })
  const state = { ...(returned as Record<string, unknown>), ...exposed } as unknown as PanelState
  return { state, emitted }
}

/** 这一轮的意图起点：版本基线 + 已勾选的卡 + 已带的来源。 */
function intentOf(overrides: Partial<ContextIntent> = {}): ContextIntent {
  return {
    expected_project_version: 7,
    expected_context_revision: 4,
    expected_version: 2,
    source_ids: [],
    card_refs: [],
    ...overrides,
  }
}

/** 一份合成的上下文包：服务端组装好的那一份。 */
function contextPackage(overrides: Partial<AgentContextPackage> = {}): AgentContextPackage {
  return {
    project_id: 'project-1',
    project_version: 7,
    spec_revision: 3,
    context_revision: 4,
    chapter_id: 'chapter-1',
    chapter_version_id: 'chapter-1@v4',
    task_id: 'task-1',
    task_version: 6,
    template_id: 'template-1',
    template_version: '2',
    source_refs: [],
    card_refs: [],
    selection_reasons: [],
    authorization_state: 'granted',
    output_constraints: [],
    correlation_id: 'corr-1',
    ...overrides,
  }
}

/** 一张合成证据卡。 */
function evidenceCard(id: string, version: number, status: EvidenceCard['status']): EvidenceCard {
  return {
    id,
    project_id: 'project-1',
    version,
    anchor_refs: [],
    source_ids: [],
    support_type: 'supports',
    scope: '第三章 研究方法',
    scope_chapter_ids: ['ch-3'],
    limitations: '',
    gaps: '',
    alternative_explanation: '',
    status,
    basis: {
      project_version: 7,
      spec_revision: 3,
      context_revision: 4,
      asset_versions: [],
      source_catalog_version: 'catalog-v1',
    },
    created_by: 'owner',
    created_at: '2026-10-23T00:00:00Z',
  }
}

/** 一条合成来源引用。 */
function sourceRef(sourceId: string, modelUseAllowed = true): AgentSourceRef {
  return {
    source_id: sourceId,
    asset_id: `asset-${sourceId}`,
    asset_revision: 1,
    quoted_text_hash: `hash-${sourceId}`,
    locator: `合成原文 ${sourceId}`,
    model_use_allowed: modelUseAllowed,
  }
}

/** 一份可变的假服务端：面板经由它说话。 */
function makeServer() {
  const state = {
    pkg: contextPackage(),
    cards: [] as EvidenceCard[],
    failure: null as unknown,
    cardFailure: null as unknown,
    previewCalls: 0,
    cardCalls: 0,
    requests: [] as AgentContextInput[],
  }
  const client: ContextPackageClient = {
    previewAgentContext: async (_projectId: string, input: AgentContextInput) => {
      state.previewCalls += 1
      state.requests.push({ ...input })
      if (state.failure) throw state.failure
      return { data: state.pkg }
    },
    listEvidenceCards: async () => {
      state.cardCalls += 1
      if (state.cardFailure) throw state.cardFailure
      return { data: state.cards }
    },
  }
  return { client, state }
}

function panelProps(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    projectId: 'project-1',
    intent: intentOf(),
    availableCards: [],
    ...overrides,
  }
}

test('面板显示服务端组装的那一份：卡片、理由、来源许可、版本基线、授权结论与输出约束', async () => {
  const { client, state } = makeServer()
  state.pkg = contextPackage({
    card_refs: [{ id: 'card-1', version: 3 }],
    source_refs: [sourceRef('source-a')],
    selection_reasons: ['用户勾选了证据卡 card-1'],
    authorization_state: 'requires_confirmation',
    output_constraints: ['不得编造出处'],
  })
  state.cards = [evidenceCard('card-1', 3, 'CONFIRMED')]

  const html = await renderPanel(panelProps({ client }))

  assert.match(html, /card-1/, '服务端带的卡片要显示出来')
  assert.match(html, /已人工确认/, '卡片状态要显示出来')
  assert.match(html, /用户勾选了证据卡 card-1/, '选择理由按服务端原文显示')
  assert.match(html, /模型使用已许可/, '逐来源的模型使用许可要显示出来')
  assert.match(html, /requires_confirmation/, '授权结论按服务端原值显示')
  assert.match(html, /不得编造出处/, '输出约束要显示出来')
  assert.match(html, /项目版本 v7/, '版本基线要显示出来')
  assert.match(html, /上下文修订 4/, '版本基线要显示出来')
})

test('意图里多勾了一张卡、服务端包里没有它 ⇒ 事实区不显示它', async () => {
  const { client, state } = makeServer()
  state.pkg = contextPackage({ card_refs: [{ id: 'card-1', version: 3 }] })
  state.cards = [evidenceCard('card-1', 3, 'CONFIRMED'), evidenceCard('card-2', 1, 'CONFIRMED')]
  const intent = intentOf({ card_refs: [{ id: 'card-1', version: 3 }, { id: 'card-2', version: 1 }] })

  const html = await renderPanel(panelProps({ client, intent }))

  assert.match(html, /data-fact-card="card-1"/, '服务端包里的卡要显示出来')
  assert.doesNotMatch(html, /data-fact-card="card-2"/, '服务端包里没有的卡不得出现在事实区')
  assert.match(html, /data-intent-card="card-2"/, '意图区仍然显示它：那是用户的勾选，不是事实')

  const { state: panelState } = openPanel(panelProps({ client, intent }))
  await panelState.settle()
  assert.deepEqual(
    panelState.facts.value?.groups.cards.map(row => row.ref.id),
    ['card-1'],
    '事实区的卡片只来自服务端包，不掺意图',
  )
})

test('预览失败 ⇒ 事实区清空并提示错误，上一次成功的包不留下来', async () => {
  const { client, state } = makeServer()
  state.pkg = contextPackage({ card_refs: [{ id: 'card-1', version: 3 }] })
  state.cards = [evidenceCard('card-1', 3, 'CONFIRMED')]
  const intent = intentOf({ card_refs: [{ id: 'card-1', version: 3 }] })

  const { state: panelState } = openPanel(panelProps({ client, intent }))
  await panelState.settle()
  assert.equal(panelState.facts.value?.groups.cards.length, 1, '先成功一次，才有「残留」可言')

  state.failure = { message: '服务端拒绝了这次预览' }
  await panelState.removeCard('card-1')

  assert.equal(panelState.facts.value, null, '预览失败后事实区必须清空，不留上一次成功的包')
  assert.match(panelState.errorMessage.value, /预览失败/)

  const html = await renderPanel(panelProps({ client, intent }))
  assert.match(html, /预览失败/)
  assert.doesNotMatch(html, /data-fact-card=/, '失败后界面上不得还摆着上一次成功的卡')
})

test('版本冲突 ⇒ 事实区清空并提示基线已变，不自动重试', async () => {
  const { client, state } = makeServer()
  state.pkg = contextPackage({ card_refs: [{ id: 'card-1', version: 3 }] })
  state.cards = [evidenceCard('card-1', 3, 'CONFIRMED')]
  const intent = intentOf({ card_refs: [{ id: 'card-1', version: 3 }] })

  const { state: panelState } = openPanel(panelProps({ client, intent }))
  await panelState.settle()
  assert.equal(panelState.facts.value?.groups.cards.length, 1, '先成功一次，才有「残留」可言')

  state.failure = { status: 409, error: { code: 'version_conflict' }, message: '内容已变化，请先读取当前版本。' }
  await panelState.removeCard('card-1')

  assert.equal(panelState.facts.value, null, '版本冲突后事实区必须清空')
  assert.match(panelState.errorMessage.value, /基线已变/, '版本冲突要提示基线已变')
  assert.equal(state.previewCalls, 2, '版本冲突不得自动重试：一次都不能多')

  const html = await renderPanel(panelProps({ client, intent }))
  assert.match(html, /基线已变/)
  assert.doesNotMatch(html, /data-fact-card=/)
})

test('卡片版本与上下文包对不上 ⇒ 显示状态未知（版本已变），不回退到别的状态', async () => {
  const { client, state } = makeServer()
  state.pkg = contextPackage({ card_refs: [{ id: 'card-1', version: 3 }] })
  state.cards = [evidenceCard('card-1', 4, 'CONFIRMED')]

  const html = await renderPanel(panelProps({
    client,
    availableCards: [{ id: 'card-1', version: 4, status: 'CONFIRMED' }],
  }))

  assert.match(html, /状态未知（版本已变）/)
  assert.match(html, /data-fact-state="unknown"/)
  assert.match(html, /data-fact-reason="version-moved"/)
  assert.doesNotMatch(html, /data-fact-state="known"/, '版本对不上不得回退到同名卡的别的版本的状态')
  assert.doesNotMatch(html, /已人工确认/, '版本变了也不摆那个版本的状态')
})

test('清单里根本没有这张卡 ⇒ 显示状态未知（清单里没有这张卡）', async () => {
  const { client, state } = makeServer()
  state.pkg = contextPackage({ card_refs: [{ id: 'card-1', version: 3 }] })
  state.cards = [evidenceCard('card-9', 1, 'CONFIRMED')]

  const html = await renderPanel(panelProps({ client }))

  assert.match(html, /状态未知（清单里没有这张卡）/)
  assert.match(html, /data-fact-reason="not-found"/)
  assert.doesNotMatch(html, /data-fact-state="known"/)
})

test('证据卡清单读不到 ⇒ 卡片状态未知（清单读不到），范围照常显示', async () => {
  const { client, state } = makeServer()
  state.pkg = contextPackage({
    card_refs: [{ id: 'card-1', version: 3 }],
    source_refs: [sourceRef('source-a')],
    selection_reasons: ['用户勾选了证据卡 card-1'],
  })
  state.cardFailure = new Error('证据卡清单读不到')

  const html = await renderPanel(panelProps({ client }))

  assert.match(html, /状态未知（清单读不到）/)
  assert.match(html, /data-fact-reason="unavailable"/)
  assert.match(html, /合成原文 source-a/, '范围照常来自上下文包')
  assert.match(html, /用户勾选了证据卡 card-1/)
})

test('加入一张卡与移除一张卡都会重新预览，并把新的意图告诉宿主', async () => {
  const { client, state } = makeServer()
  state.pkg = contextPackage({ card_refs: [{ id: 'card-1', version: 3 }] })
  const { state: panelState, emitted } = openPanel(panelProps({
    client,
    intent: intentOf({ card_refs: [{ id: 'card-1', version: 3 }] }),
    availableCards: [{ id: 'card-2', version: 1, status: 'CONFIRMED' }],
  }))
  await panelState.settle()
  assert.equal(state.previewCalls, 1, '打开面板先预览一次')

  await panelState.addCard({ id: 'card-2', version: 1 })
  assert.equal(state.previewCalls, 2, '加入一张卡之后必须重新预览')
  assert.deepEqual(panelState.intent.value.card_refs, [{ id: 'card-1', version: 3 }, { id: 'card-2', version: 1 }])
  assert.equal(emitted.length, 1, '意图变了要告诉宿主')
  assert.equal(emitted[0].event, 'intentChange')
  assert.deepEqual((emitted[0].payload as ContextIntent).card_refs, [{ id: 'card-1', version: 3 }, { id: 'card-2', version: 1 }])

  await panelState.removeCard('card-1')
  assert.equal(state.previewCalls, 3, '移除一张卡之后必须重新预览')
  assert.deepEqual(panelState.intent.value.card_refs, [{ id: 'card-2', version: 1 }])
  assert.equal(emitted.length, 2)

  const html = await renderPanel(panelProps({
    client,
    intent: intentOf({ card_refs: [{ id: 'card-1', version: 3 }] }),
    availableCards: [{ id: 'card-2', version: 1, status: 'CONFIRMED' }],
  }))
  assert.match(html, /data-remove="card-1"/, '意图区要能移除一张卡')
  assert.match(html, /加入这张卡/, '要能加入一张卡')
  assert.doesNotMatch(html, /type="checkbox"/, '面板不自己发敏感确认勾选')
})

test('撤权后服务端不再返回受限条目 ⇒ 面板不再显示它', async () => {
  const { client, state } = makeServer()
  state.pkg = contextPackage({
    source_refs: [sourceRef('source-a')],
    selection_reasons: ['第一条理由'],
  })

  const { state: panelState } = openPanel(panelProps({ client }))
  await panelState.settle()
  assert.deepEqual(panelState.facts.value?.groups.sources.map(source => source.source_id), ['source-a'])

  state.pkg = contextPackage({ source_refs: [], selection_reasons: [] })
  await panelState.preview()

  assert.deepEqual(panelState.facts.value?.groups.sources, [], '服务端不再返回的条目不得留在面板上')
  assert.deepEqual(panelState.facts.value?.groups.reasons, [])
})

test('宿主更新了版本基线 ⇒ 面板下一次预览就用新基线，不卡在旧的冲突里', async () => {
  const { client, state } = makeServer()
  const props = vue.reactive(panelProps({ client })) as Record<string, any>
  const { state: panelState } = openPanel(props)
  await panelState.settle()
  assert.equal(state.previewCalls, 1)
  assert.equal(state.requests[0].expected_project_version, 7)

  props.intent.expected_project_version = 9
  await vue.nextTick()
  await panelState.settle()

  assert.equal(state.previewCalls, 2, '基线变了要重新预览')
  assert.equal(state.requests[1].expected_project_version, 9, '预览必须带宿主当前的那份基线')
})

test('面板不引传输层：只依赖门面模块', () => {
  const specifiers = [...panelSourceText.matchAll(/from\s+['"]([^'"]+)['"]/g)].map(match => match[1])
  assert.deepEqual(
    specifiers.filter(id => /agentApi/.test(id)),
    [],
    '面板不得 import 传输层模块',
  )
  assert.deepEqual(
    [...new Set(specifiers)].sort(),
    ['@/api/lingdoc/agentContext', 'vue'],
    '面板的依赖集只有 vue 与门面模块',
  )
})

test('组件沿用仓内写法：script setup、类型式 props/emits、顶部接口评论、scoped + BEM + 字面色值、硬编码中文', () => {
  assert.match(panelSourceText, /<script setup lang="ts">/)
  assert.match(panelSourceText, /defineProps</)
  assert.match(panelSourceText, /defineEmits</)
  assert.match(panelSourceText, /<style scoped>/)
  assert.match(panelSourceText, /^\/\*\*|^<!--/, '文件顶部要有接口评论')

  const styles = parsed.descriptor.styles.map(block => block.content).join('\n')
  assert.doesNotMatch(styles, /var\(/, '样式用字面色值，不用 CSS 变量')
  assert.match(styles, /\.context-package-panel__/, '类名用 BEM')

  assert.doesNotMatch(panelSourceText, /vue-i18n|useI18n|\$t\(/, 'lingdoc 工作台硬编码中文，不走 vue-i18n')
  assert.doesNotMatch(panelSourceText, /sensitive_context_confirmed/, '面板不自己发敏感确认')
})
