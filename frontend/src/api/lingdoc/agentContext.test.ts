import assert from 'node:assert/strict'
import fs, { existsSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import { fileURLToPath } from 'node:url'
import type { AgentContextPackage, AgentSourceRef, EvidenceCard } from './agentApi'
import type { ContextFacts, ContextIntent, ContextPackageClient } from './agentContext'

/**
 * 上下文包门面的用例契约。
 *
 * 这一层分两半：一半是**看得见的行为**——意图的增删、请求体的字段、卡片状态
 * 的对齐、事实的分组、预览的编排；另一半是**结构**——契约类型由传输层拥有、
 * 理由按原文透传、模块不自写 HTTP 客户端、层次方向单向。
 *
 * 模块本身引着传输层（agentApi.ts → @/utils/request），这条测试的运行环境解析
 * 不了那个路径别名，所以模块在这里被转译后装进当前 realm 跑：对象仍是本 realm
 * 的对象，断言可以直接用 deepEqual 比较。假客户端注入在编排入口上，纯函数层
 * 因此完全不打网络。
 */

/**
 * 类型层的互斥断言：意图与事实的字段集必须不相交。
 *
 * 静态断言在运行时检查类型检查器读到的字段集；这一条是同一件事的编译期版本——
 * `vue-tsc` 会在类型检查阶段挡住混用，比任何运行时断言都早一步。
 */
type Assert<T extends true> = T
export type IntentAndFactsFieldsAreDisjoint = Assert<
  Extract<keyof ContextIntent, keyof ContextFacts> extends never ? true : false
>

// 对外契约在仓库根下，前端目录可能被单独 checkout，所以读不到时显式 skip。
const CONTRACT = new URL('../../../../docs/08-本轮实施方案/contracts/openapi.json', import.meta.url)
const CONTRACT_AVAILABLE = existsSync(CONTRACT)

function contractInputProperties(): string[] {
  const spec = JSON.parse(fs.readFileSync(CONTRACT, 'utf8'))
  const schema = spec?.components?.schemas?.AgentContextInput
  if (!schema) throw new Error('契约里没有 AgentContextInput schema')
  return Object.keys(schema.properties ?? {})
}

type AgentContextModule = typeof import('./agentContext')

const AGENT_API_SOURCE = new URL('./agentApi.ts', import.meta.url)
const AGENT_CONTEXT_SOURCE = new URL('./agentContext.ts', import.meta.url)

type RequestCall = { method: string; url: string; body?: unknown }

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

/** 装一份门面模块进来；HTTP 层是记调用的假件，服务端给什么由 responses 决定。 */
function loadAgentContext(responses: { preview?: unknown; cards?: unknown } = {}) {
  const calls: RequestCall[] = []
  const request = {
    get: (url: string) => {
      calls.push({ method: 'get', url })
      return Promise.resolve({ data: responses.cards ?? [] })
    },
    post: (url: string, body?: unknown) => {
      calls.push({ method: 'post', url, body })
      return Promise.resolve({ data: responses.preview ?? {} })
    },
    put: (url: string, body?: unknown) => {
      calls.push({ method: 'put', url, body })
      return Promise.resolve({ data: {} })
    },
  }
  const api = loadModule(AGENT_API_SOURCE, { '@/utils/request': request })
  const context = loadModule(AGENT_CONTEXT_SOURCE, { './agentApi': api })
  return { api, context: context as unknown as AgentContextModule, calls }
}

const BASIN = { expected_project_version: 7, expected_context_revision: 4, expected_version: 2 }

/** 一张合成证据卡：只有 id、版本与业务状态是这里真正用到的。 */
function evidenceCard(id: string, version: number, status: EvidenceCard['status']): EvidenceCard {
  return {
    id,
    project_id: 'project-1',
    version,
    anchor_refs: [],
    source_ids: [],
    support_type: 'supports',
    scope: 'project',
    scope_chapter_ids: [],
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
function sourceRef(sourceId: string): AgentSourceRef {
  return {
    source_id: sourceId,
    asset_id: `asset-${sourceId}`,
    asset_revision: 1,
    quoted_text_hash: `hash-${sourceId}`,
    locator: '合成原文，第 0 块',
    model_use_allowed: true,
  }
}

/** 一份合成的上下文包：服务端组装好的那一份。 */
function contextPackage(overrides: Partial<AgentContextPackage> = {}): AgentContextPackage {
  return {
    project_id: 'project-1',
    project_version: 7,
    spec_revision: 3,
    context_revision: 4,
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

test('勾卡与去卡返回新意图，原意图不被就地修改', () => {
  const { context } = loadAgentContext()
  const intent = context.createIntent(BASIN)
  const before = structuredClone(intent)

  const added = context.withCard(intent, { id: 'card-1', version: 3 })
  assert.deepEqual(intent, before, '原意图不能被就地修改')
  assert.notEqual(added, intent, '增删必须返回新意图')
  assert.deepEqual(added.card_refs, [{ id: 'card-1', version: 3 }])

  const removed = context.withoutCard(added, 'card-1')
  assert.deepEqual(removed.card_refs, [])
  assert.deepEqual(added.card_refs, [{ id: 'card-1', version: 3 }], '被删的那份也不该被就地修改')
})

test('重复加入同一张卡不改变结果', () => {
  const { context } = loadAgentContext()
  const once = context.withCard(context.createIntent(BASIN), { id: 'card-1', version: 3 })
  const twice = context.withCard(once, { id: 'card-1', version: 3 })
  assert.deepEqual(twice, once, '重复加入同一张卡不改变结果')
  assert.equal(twice.card_refs.length, 1)
})

test('加来源与去来源返回新意图，重复加入不改变结果', () => {
  const { context } = loadAgentContext()
  const intent = context.createIntent(BASIN)
  const once = context.withSource(intent, 'source-1')
  const twice = context.withSource(once, 'source-1')

  assert.deepEqual(intent.source_ids, [], '原意图不被就地修改')
  assert.deepEqual(twice.source_ids, ['source-1'])
  assert.deepEqual(twice, once, '重复加入同一来源不改变结果')

  const removed = context.withoutSource(twice, 'source-1')
  assert.deepEqual(removed.source_ids, [])
  assert.deepEqual(twice.source_ids, ['source-1'])
})

test('请求体按服务端契约逐字段构造，敏感上下文不由面板代确认', (t) => {
  const { context } = loadAgentContext()
  const intent = context.withCard(
    context.withSource(context.createIntent({ ...BASIN, chapter_id: 'chapter-1' }), 'source-1'),
    { id: 'card-1', version: 3 },
  )
  const request = context.buildContextRequest(intent)

  assert.equal(request.expected_project_version, 7)
  assert.equal(request.expected_context_revision, 4)
  assert.equal(request.expected_version, 2)
  assert.equal(request.chapter_id, 'chapter-1')
  assert.deepEqual(request.source_ids, ['source-1'])
  assert.deepEqual(request.card_refs, [{ id: 'card-1', version: 3 }])
  assert.equal(request.sensitive_context_confirmed, false, '面板不代用户确认敏感上下文')

  if (!CONTRACT_AVAILABLE) {
    t.skip(`读不到对外契约（${CONTRACT.pathname}），跳过字段名对照`)
    return
  }
  assert.deepEqual(
    Object.keys(request).sort(),
    contractInputProperties().sort(),
    '请求体的字段名必须与服务端契约逐字对齐',
  )
})

test('请求体是意图的快照：改了意图，已经构造好的请求不动', () => {
  const { context } = loadAgentContext()
  const intent = context.withCard(context.createIntent(BASIN), { id: 'card-1', version: 3 })
  const request = context.buildContextRequest(intent)

  intent.card_refs.push({ id: 'card-2', version: 1 })
  intent.source_ids.push('source-1')

  assert.deepEqual(request.card_refs, [{ id: 'card-1', version: 3 }])
  assert.deepEqual(request.source_ids, [])
})

test('卡片状态按 id 且 version 双重对齐，版本不等就是状态未知', () => {
  const { context } = loadAgentContext()
  const cards = [evidenceCard('card-1', 3, 'CONFIRMED'), evidenceCard('card-2', 1, 'NEEDS_REVIEW')]

  const matched = context.alignCardStatuses([{ id: 'card-1', version: 3 }], cards)
  assert.equal(matched.length, 1)
  assert.equal(matched[0].state, 'known')
  assert.equal(matched[0].status, 'CONFIRMED')
  assert.deepEqual(matched[0].ref, { id: 'card-1', version: 3 })

  const stale = context.alignCardStatuses([{ id: 'card-1', version: 2 }], cards)
  assert.equal(stale[0].state, 'unknown', '版本不等不得回退到同名卡的别的版本')
  assert.equal(stale[0].status, undefined, '状态未知时不给任何状态值')
  assert.deepEqual(stale[0].ref, { id: 'card-1', version: 2 })

  const missing = context.alignCardStatuses([{ id: 'card-3', version: 1 }], cards)
  assert.equal(missing[0].state, 'unknown', '清单里没有这张卡也是状态未知')
  assert.equal(missing[0].status, undefined)
})

test('状态未知分得清是版本变了，还是清单里没有这张卡', () => {
  const { context } = loadAgentContext()
  const cards = [evidenceCard('card-1', 3, 'CONFIRMED')]

  const moved = context.alignCardStatuses([{ id: 'card-1', version: 2 }], cards)
  assert.equal(moved[0].state, 'unknown')
  assert.equal(moved[0].reason, 'version-moved', '清单里有这张卡，但版本不是包里那个')
  assert.equal(moved[0].status, undefined, '版本变了也不回退到别的版本的状态')

  const missing = context.alignCardStatuses([{ id: 'card-9', version: 1 }], cards)
  assert.equal(missing[0].state, 'unknown')
  assert.equal(missing[0].reason, 'not-found', '清单里根本没有这张卡')
  assert.equal(missing[0].status, undefined)
})

test('对齐按包里的卡片顺序来，每张卡各占一行', () => {
  const { context } = loadAgentContext()
  const cards = [evidenceCard('card-1', 3, 'CONFIRMED'), evidenceCard('card-2', 1, 'STALE')]
  const rows = context.alignCardStatuses(
    [{ id: 'card-2', version: 1 }, { id: 'card-1', version: 3 }, { id: 'card-1', version: 9 }],
    cards,
  )
  assert.deepEqual(rows.map(row => row.state), ['known', 'known', 'unknown'])
  assert.deepEqual(rows.map(row => row.status), ['STALE', 'CONFIRMED', undefined])
})

test('事实分成卡片、来源、理由、版本基线、权限五组', () => {
  const { context } = loadAgentContext()
  const pkg = contextPackage({
    card_refs: [{ id: 'card-1', version: 3 }],
    source_refs: [sourceRef('source-b'), sourceRef('source-a')],
    selection_reasons: ['用户勾选了证据卡 card-1', 'card-2: 由助手推测'],
    authorization_state: 'granted',
    output_constraints: ['不得编造出处'],
  })
  const facts = context.groupContextFacts(pkg, [evidenceCard('card-1', 3, 'CONFIRMED')])

  assert.deepEqual(
    Object.keys(facts.groups).sort(),
    ['authorization', 'cards', 'reasons', 'sources', 'version_baseline'],
  )
  assert.equal(facts.groups.cards.length, 1)
  assert.equal(facts.groups.cards[0].status, 'CONFIRMED')
  assert.equal(facts.groups.version_baseline.project_version, 7)
  assert.equal(facts.groups.version_baseline.spec_revision, 3)
  assert.equal(facts.groups.version_baseline.context_revision, 4)
  assert.equal(facts.groups.version_baseline.template_id, 'template-1')
  assert.equal(facts.groups.version_baseline.template_version, '2')
  assert.equal(facts.groups.authorization.authorization_state, 'granted')
  assert.deepEqual(facts.groups.authorization.output_constraints, ['不得编造出处'])
})

test('版本基线给出项目版本、规范修订、上下文修订、章节版本、任务版本与模板版本', () => {
  const { context } = loadAgentContext()
  const pkg = contextPackage({
    chapter_id: 'chapter-1',
    chapter_version_id: 'chapter-1@v4',
    task_id: 'task-1',
    task_version: 6,
  })
  const facts = context.groupContextFacts(pkg, [])
  const baseline = facts.groups.version_baseline

  assert.equal(baseline.project_version, 7)
  assert.equal(baseline.spec_revision, 3)
  assert.equal(baseline.context_revision, 4)
  assert.equal(baseline.chapter_version_id, 'chapter-1@v4')
  assert.equal(baseline.task_version, 6)
  assert.equal(baseline.template_id, 'template-1')
  assert.equal(baseline.template_version, '2')
})

test('来源保持服务端给的顺序，理由保持服务端给的条数与顺序', () => {
  const { context } = loadAgentContext()
  const pkg = contextPackage({
    source_refs: [sourceRef('source-b'), sourceRef('source-a'), sourceRef('source-c')],
    selection_reasons: ['第三条理由', '第一条理由', '第二条理由'],
  })
  const facts = context.groupContextFacts(pkg, [])

  assert.deepEqual(facts.groups.sources.map(item => item.source_id), ['source-b', 'source-a', 'source-c'])
  assert.deepEqual(facts.groups.reasons, ['第三条理由', '第一条理由', '第二条理由'])
  assert.equal(facts.groups.reasons.length, 3)
})

test('理由是不透明字符串：看着像条目的理由也原样透传，不解析成对应关系', () => {
  const { context } = loadAgentContext()
  // 理由里带着冒号：任何按分隔符拆条目的做法都会在这里露馅。
  const reasons = ['card-1: 用户勾选', 'card-2：助手推测', '与卡片无关的一句话']
  const pkg = contextPackage({
    card_refs: [{ id: 'card-1', version: 3 }],
    selection_reasons: reasons,
  })
  const facts = context.groupContextFacts(pkg, [evidenceCard('card-1', 3, 'CONFIRMED')])

  assert.deepEqual(facts.groups.reasons, reasons)
  assert.equal(facts.groups.cards.length, 1, '理由的文本不得决定带哪些卡')
  assert.deepEqual(facts.groups.cards.map(row => row.ref.id), ['card-1'])
})

test('预览编排调用既有的预览与证据卡接口，自己不写客户端', async () => {
  const { context, calls } = loadAgentContext({
    preview: contextPackage({
      card_refs: [{ id: 'card-1', version: 3 }],
      source_refs: [sourceRef('source-a')],
      selection_reasons: ['用户勾选了证据卡 card-1'],
    }),
    cards: [evidenceCard('card-1', 3, 'CONFIRMED')],
  })
  const intent = context.withCard(context.createIntent(BASIN), { id: 'card-1', version: 3 })

  const facts = await context.previewContextPackage('project-1', intent)

  assert.deepEqual(
    calls.map(call => [call.method, call.url]),
    [
      ['post', '/api/v1/lingdoc/projects/project-1/agent-context'],
      ['get', '/api/v1/lingdoc/projects/project-1/evidence-cards'],
    ],
  )
  const sent = calls[0].body as Record<string, unknown>
  assert.equal(sent.sensitive_context_confirmed, false)
  assert.deepEqual(sent.card_refs, [{ id: 'card-1', version: 3 }])
  assert.equal(sent.expected_project_version, 7)

  assert.deepEqual(facts.groups.cards.map(row => row.ref.id), ['card-1'])
  assert.equal(facts.groups.cards[0].status, 'CONFIRMED')
  assert.deepEqual(facts.groups.reasons, ['用户勾选了证据卡 card-1'])
})

test('事实区只由这一次预览的结果刷新：上一次的不留、不合并', async () => {
  const { context } = loadAgentContext()
  let current = contextPackage({
    card_refs: [{ id: 'card-1', version: 3 }],
    source_refs: [sourceRef('source-a')],
    selection_reasons: ['第一条理由'],
  })
  const client: ContextPackageClient = {
    previewAgentContext: async () => ({ data: current }),
    listEvidenceCards: async () => ({ data: [] }),
  }

  const before = await context.previewContextPackage('project-1', context.createIntent(BASIN), client)
  current = contextPackage({
    card_refs: [{ id: 'card-2', version: 1 }],
    source_refs: [sourceRef('source-b')],
    selection_reasons: ['第二条理由'],
  })
  const after = await context.previewContextPackage('project-1', context.createIntent(BASIN), client)

  assert.deepEqual(before.groups.sources.map(item => item.source_id), ['source-a'])
  assert.deepEqual(after.groups.sources.map(item => item.source_id), ['source-b'])
  assert.deepEqual(after.groups.reasons, ['第二条理由'])
  assert.deepEqual(after.groups.cards.map(row => row.ref.id), ['card-2'])
})

test('事实区不掺意图：意图里勾的卡没进包，事实里就没有', async () => {
  const { context } = loadAgentContext()
  const client: ContextPackageClient = {
    previewAgentContext: async () => ({ data: contextPackage({ card_refs: [] }) }),
    listEvidenceCards: async () => ({ data: [evidenceCard('card-9', 1, 'CONFIRMED')] }),
  }
  const intent = context.withCard(context.createIntent(BASIN), { id: 'card-1', version: 3 })

  const facts = await context.previewContextPackage('project-1', intent, client)

  assert.deepEqual(facts.groups.cards, [])
  assert.deepEqual(facts.groups.sources, [])
  assert.deepEqual(facts.groups.reasons, [])
})

test('清单读不到时预览照常成功：范围在，卡片状态未知', async () => {
  const { context } = loadAgentContext()
  const client: ContextPackageClient = {
    previewAgentContext: async () => ({
      data: contextPackage({
        card_refs: [{ id: 'card-1', version: 3 }],
        source_refs: [sourceRef('source-a')],
        selection_reasons: ['用户勾选了证据卡 card-1'],
        authorization_state: 'granted',
        output_constraints: ['不得编造出处'],
      }),
    }),
    listEvidenceCards: async () => {
      throw new Error('证据卡清单读不到')
    },
  }

  const facts = await context.previewContextPackage('project-1', context.createIntent(BASIN), client)

  assert.equal(facts.groups.cards.length, 1)
  assert.equal(facts.groups.cards[0].state, 'unknown')
  assert.equal(facts.groups.cards[0].reason, 'unavailable', '清单没读到，无从判断是版本变了还是没有这张卡')
  assert.equal(facts.groups.cards[0].status, undefined)

  assert.deepEqual(facts.groups.sources.map(item => item.source_id), ['source-a'])
  assert.deepEqual(facts.groups.reasons, ['用户勾选了证据卡 card-1'])
  assert.equal(facts.groups.version_baseline.project_version, 7)
  assert.equal(facts.groups.authorization.authorization_state, 'granted')
  assert.deepEqual(facts.groups.authorization.output_constraints, ['不得编造出处'])
})

test('预览失败时整体失败：调用方据此清空事实区', async () => {
  const { context } = loadAgentContext()
  const client: ContextPackageClient = {
    previewAgentContext: async () => {
      throw new Error('预览被服务端拒绝')
    },
    listEvidenceCards: async () => ({ data: [] }),
  }

  await assert.rejects(
    () => context.previewContextPackage('project-1', context.createIntent(BASIN), client),
    /预览被服务端拒绝/,
  )
})

/**
 * 结构守护：这一组断言看的不是行为，而是**模块的形状**。
 *
 * 这些形状是这票的地基：契约类型归传输层所有、模块不导出它们、理由不被解析、
 * 模块不自写 HTTP 客户端、层次方向单向、意图与事实字段集不相交。形状一旦漂了，
 * 行为用例还可能全绿——所以它们各有一条静态断言。
 */

/** 契约类型由传输层拥有：门面既不能重新声明它们，也不能把它们再导出去。 */
const CONTRACT_TYPE_NAMES = ['AgentContextPackage', 'AgentContextInput', 'VersionRef', 'AgentSourceRef', 'EvidenceCard']

const FRONTEND_DIR = fileURLToPath(new URL('../../../', import.meta.url))
const AGENT_CONTEXT_FILE = fileURLToPath(AGENT_CONTEXT_SOURCE)
const AGENT_API_FILE = fileURLToPath(AGENT_API_SOURCE)

function parseModule(file: string): ts.SourceFile {
  return ts.createSourceFile(file, fs.readFileSync(file, 'utf8'), ts.ScriptTarget.ES2022, true)
}

function hasExportModifier(node: ts.Node): boolean {
  const modifiers = ts.canHaveModifiers(node) ? ts.getModifiers(node) : undefined
  return Boolean(modifiers?.some(modifier => modifier.kind === ts.SyntaxKind.ExportKeyword))
}

/** 模块自己声明的类型名。 */
function declaredTypeNames(sourceFile: ts.SourceFile): string[] {
  const names: string[] = []
  const visit = (node: ts.Node) => {
    if (
      ts.isInterfaceDeclaration(node) ||
      ts.isTypeAliasDeclaration(node) ||
      ts.isClassDeclaration(node) ||
      ts.isEnumDeclaration(node)
    ) {
      if (node.name) names.push(node.name.text)
    }
    ts.forEachChild(node, visit)
  }
  visit(sourceFile)
  return names
}

/** 模块导出的名字。 */
function exportedNames(sourceFile: ts.SourceFile): string[] {
  const names: string[] = []
  const visit = (node: ts.Node) => {
    if (ts.isExportDeclaration(node)) {
      const clause = node.exportClause
      if (clause && ts.isNamedExports(clause)) {
        for (const element of clause.elements) names.push(element.name.text)
      }
    }
    if (
      (ts.isInterfaceDeclaration(node) ||
        ts.isTypeAliasDeclaration(node) ||
        ts.isClassDeclaration(node) ||
        ts.isEnumDeclaration(node)) &&
      hasExportModifier(node)
    ) {
      if (node.name) names.push(node.name.text)
    }
    if (ts.isVariableStatement(node) && hasExportModifier(node)) {
      for (const declaration of node.declarationList.declarations) {
        if (ts.isIdentifier(declaration.name)) names.push(declaration.name.text)
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(sourceFile)
  return names
}

/** 模块里 `export * from ...` 的出现：星号导出会把契约类型一并带走。 */
function starExports(sourceFile: ts.SourceFile): string[] {
  const found: string[] = []
  const visit = (node: ts.Node) => {
    if (ts.isExportDeclaration(node) && node.exportClause === undefined) {
      found.push(node.getText(sourceFile))
    }
    ts.forEachChild(node, visit)
  }
  visit(sourceFile)
  return found
}

/** 模块的 import 来源。 */
function importSpecifiers(sourceFile: ts.SourceFile): string[] {
  return sourceFile.statements
    .filter(ts.isImportDeclaration)
    .map(statement => (ts.isStringLiteral(statement.moduleSpecifier) ? statement.moduleSpecifier.text : ''))
}

/** 模块从某处引入的名字。`typeOnly` 区分「引的是类型」与「引的是值」。 */
function importedNames(sourceFile: ts.SourceFile, specifier: string, typeOnly: boolean): string[] {
  const names: string[] = []
  for (const statement of sourceFile.statements) {
    if (!ts.isImportDeclaration(statement)) continue
    if (!ts.isStringLiteral(statement.moduleSpecifier) || statement.moduleSpecifier.text !== specifier) continue
    if (Boolean(statement.importClause?.isTypeOnly) !== typeOnly) continue
    const bindings = statement.importClause?.namedBindings
    if (bindings && ts.isNamedImports(bindings)) {
      for (const element of bindings.elements) names.push(element.name.text)
    }
  }
  return names
}

/** 出现在模块里的标识符。 */
function identifiersNamed(sourceFile: ts.SourceFile, names: Set<string>): string[] {
  const found: string[] = []
  const visit = (node: ts.Node) => {
    if (ts.isIdentifier(node) && names.has(node.text)) found.push(node.text)
    ts.forEachChild(node, visit)
  }
  visit(sourceFile)
  return found
}

const MATCHING_METHODS = new Set([
  'includes', 'startsWith', 'endsWith', 'indexOf', 'lastIndexOf',
  'match', 'search', 'split', 'replace', 'replaceAll', 'test', 'exec',
])

function enclosingStatement(node: ts.Node): ts.Statement | undefined {
  let current: ts.Node | undefined = node
  while (current) {
    if (ts.isStatement(current)) return current
    current = current.parent
  }
  return undefined
}

/** 针对理由文本做匹配或解析的调用点。 */
function reasonStringMatchingCalls(sourceFile: ts.SourceFile): string[] {
  const found: string[] = []
  const visit = (node: ts.Node) => {
    if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression)) {
      const method = node.expression.name.text
      if (MATCHING_METHODS.has(method)) {
        const statement = enclosingStatement(node)
        const text = statement?.getText(sourceFile) ?? ''
        if (/reason/i.test(text)) found.push(text.replace(/\s+/g, ' ').slice(0, 120))
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(sourceFile)
  return found
}

test('契约类型由传输层拥有：门面不重新声明它们', () => {
  const declared = declaredTypeNames(parseModule(AGENT_CONTEXT_FILE))
  for (const name of CONTRACT_TYPE_NAMES) {
    assert.equal(declared.includes(name), false, `门面不得重新声明 ${name}`)
  }
  const imported = importedNames(parseModule(AGENT_CONTEXT_FILE), './agentApi', true)
  for (const name of CONTRACT_TYPE_NAMES) {
    assert.equal(imported.includes(name), true, `${name} 必须从传输层引入，不另立一份`)
  }
})

test('门面不导出契约类型本身', () => {
  const sourceFile = parseModule(AGENT_CONTEXT_FILE)
  const exported = exportedNames(sourceFile)
  for (const name of CONTRACT_TYPE_NAMES) {
    assert.equal(exported.includes(name), false, `门面不得导出 ${name}`)
  }
  assert.deepEqual(starExports(sourceFile), [], '星号导出会把传输层的契约类型一并带走')
})

test('选择理由不做字符串匹配：模块里没有针对理由文本的匹配或解析', () => {
  assert.deepEqual(reasonStringMatchingCalls(parseModule(AGENT_CONTEXT_FILE)), [])
})

test('门面不自写 HTTP 客户端，只用传输层已有的那两组函数', () => {
  const sourceFile = parseModule(AGENT_CONTEXT_FILE)
  const specifiers = importSpecifiers(sourceFile)
  assert.equal(specifiers.includes('@/utils/request'), false, '门面不得直接引 HTTP 层')
  assert.equal(specifiers.includes('axios'), false)
  assert.deepEqual(
    identifiersNamed(sourceFile, new Set(['fetch', 'XMLHttpRequest', 'axios'])),
    [],
    '门面不得自己发请求',
  )
  assert.deepEqual(
    importedNames(sourceFile, './agentApi', false).sort(),
    ['listEvidenceCards', 'previewAgentContext'].sort(),
    '编排必须用传输层已有的预览与证据卡函数',
  )
})

test('层次方向单向：传输层不反过来依赖门面', () => {
  const specifiers = importSpecifiers(parseModule(AGENT_API_FILE))
  assert.equal(
    specifiers.some(specifier => specifier.includes('agentContext')),
    false,
    'agentApi.ts 不得反向依赖 agentContext.ts',
  )
})

/** 模块里某个类型的声明。 */
function findTypeDeclaration(sourceFile: ts.SourceFile, typeName: string): ts.Declaration | undefined {
  let found: ts.Declaration | undefined
  const visit = (node: ts.Node) => {
    if (
      (ts.isInterfaceDeclaration(node) || ts.isTypeAliasDeclaration(node)) &&
      node.name.text === typeName
    ) {
      found = node
    }
    ts.forEachChild(node, visit)
  }
  visit(sourceFile)
  return found
}

// 类型检查器建一次程序就够：读门面模块的类型要用它。
let typeProgram: ts.Program | undefined

function moduleTypeChecker(): { checker: ts.TypeChecker; moduleFile: ts.SourceFile } {
  if (!typeProgram) {
    typeProgram = ts.createProgram([AGENT_CONTEXT_FILE], {
      strict: true,
      target: ts.ScriptTarget.ES2022,
      module: ts.ModuleKind.ESNext,
      moduleResolution: ts.ModuleResolutionKind.Bundler,
      skipLibCheck: true,
      baseUrl: FRONTEND_DIR,
      paths: { '@/*': ['./src/*'] },
    })
  }
  const checker = typeProgram.getTypeChecker()
  const moduleFile = typeProgram
    .getSourceFiles()
    .find(file => file.fileName.replace(/\\/g, '/').endsWith('/agentContext.ts'))
  assert.ok(moduleFile, '类型检查器必须读得到门面模块')
  return { checker, moduleFile }
}

/** 类型检查器读到的某个类型的字段名。 */
function typeFieldNames(typeName: string): string[] {
  const { checker, moduleFile } = moduleTypeChecker()
  const declaration = findTypeDeclaration(moduleFile, typeName)
  assert.ok(declaration, `模块里找不到类型 ${typeName}`)
  const type = checker.getTypeAtLocation(declaration)
  return checker.getPropertiesOfType(type).map(symbol => symbol.getName()).sort()
}

test('意图的形状由本模块拥有：显式声明，不从传输层契约派生', () => {
  const sourceFile = parseModule(AGENT_CONTEXT_FILE)
  const declaration = findTypeDeclaration(sourceFile, 'ContextIntent')
  assert.ok(declaration, '模块里必须有 ContextIntent')
  assert.equal(
    ts.isInterfaceDeclaration(declaration),
    true,
    'ContextIntent 必须显式声明自己的字段：意图是本地的知识，形状该由本地说了算',
  )
  assert.equal(
    declaration.getText(sourceFile).includes('AgentContextInput'),
    false,
    '意图的形状不得从传输层契约派生：传输层加一个服务端才该有的字段，客户端就会能替它声明',
  )
  assert.deepEqual(
    typeFieldNames('ContextIntent'),
    [
      'card_refs',
      'chapter_id',
      'expected_context_revision',
      'expected_project_version',
      'expected_version',
      'source_ids',
      'task_id',
    ],
    '意图的字段集就是这七条，不多不少',
  )
})

test('意图与事实的字段集不相交：混用在类型层就暴露', () => {
  const intentFields = typeFieldNames('ContextIntent')
  const factsFields = typeFieldNames('ContextFacts')
  assert.ok(intentFields.length > 0)
  assert.ok(factsFields.length > 0)
  const overlap = intentFields.filter(field => factsFields.includes(field))
  assert.deepEqual(overlap, [], `意图与事实共用了这些字段：${overlap.join('、')}`)
})
