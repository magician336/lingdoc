import assert from 'node:assert/strict'
import { readFileSync, unlinkSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { beforeEach, test } from 'node:test'
import { pathToFileURL, fileURLToPath } from 'node:url'
import { createRenderer, h, reactive } from 'vue'
import { compileScript, parse } from '@vue/compiler-sfc'
import { transformSync } from 'esbuild'
import type { EvidenceCard, G2VersionRef, ResearchClaim } from '@/api/lingdoc/g2'

// Runtime lifecycle tests against the real component, mounted with Vue's actual
// client renderer on a minimal in-memory host. This gives genuine reactivity:
// parent-driven prop updates flow through the real patch path, watchers fire,
// deferred API promises resolve in controlled order, and buttons dispatch real
// click events into the emit chain. What it cannot prove is browser layout /
// CSS / focus behavior, which remains a browser check.
const here = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(join(here, 'SelectedEvidenceContext.vue'), 'utf8')
const { descriptor, errors } = parse(source, { filename: 'SelectedEvidenceContext.vue' })
assert.deepEqual(errors, [])
const compiled = compileScript(descriptor, { id: 'selected-evidence-runtime-test', inlineTemplate: true })

const stubName = `.selected-evidence-runtime-stub.${process.pid}.mjs`
writeFileSync(join(here, stubName), `export const impl = { deferreds: [], claimWaits: [], cards: [], claims: [], cardCalls: 0, projects: [] }
export const listEvidenceCards = (projectId) => { impl.cardCalls += 1; impl.projects.push(projectId); return new Promise((resolve, reject) => impl.deferreds.push({ resolve, reject })) }
export const listResearchClaims = () => new Promise((resolve, reject) => impl.claimWaits.push({ resolve, reject }))
`)
const withoutTypeImports = compiled.content.split('\n').filter(line => !line.startsWith('import type ')).join('\n')
const rewritten = withoutTypeImports.replace(/from '@\/api\/lingdoc\/g2'/g, `from './${stubName}'`)
const runnable = transformSync(rewritten, { loader: 'ts', format: 'esm' }).code

const tmpFile = join(here, `.selected-evidence-runtime.compiled.${process.pid}.mjs`)
writeFileSync(tmpFile, runnable)
const stub = (await import(pathToFileURL(join(here, stubName)).href)).impl as {
  deferreds: Array<{ resolve: (value: { data: EvidenceCard[] }) => void; reject: (cause: unknown) => void }>
  claimWaits: Array<{ resolve: (value: { data: ResearchClaim[] }) => void; reject: (cause: unknown) => void }>
  cards: EvidenceCard[]; claims: ResearchClaim[]; cardCalls: number; projects: string[]
}
let SelectedEvidenceContext: new (...args: never[]) => unknown
try {
  SelectedEvidenceContext = (await import(pathToFileURL(tmpFile).href)).default
} finally {
  unlinkSync(tmpFile)
  unlinkSync(join(here, stubName))
}

// ---- minimal in-memory host for Vue's client renderer ----
interface FakeNode {
  tag?: string
  text?: string
  comment?: string
  props: Record<string, unknown>
  listeners: Record<string, (event: unknown) => void>
  children: FakeNode[]
  parent: FakeNode | null
}
const createFakeElement = (tag: string): FakeNode => ({ tag, props: {}, listeners: {}, children: [], parent: null })
const cloneNode = (node: FakeNode): FakeNode => ({
  ...node,
  props: { ...node.props },
  listeners: { ...node.listeners },
  children: node.children.map(child => { const copy = cloneNode(child); copy.parent = null as unknown as FakeNode; return copy }),
  parent: null,
})
const renderer = createRenderer<FakeNode, FakeNode>({
  createElement: createFakeElement,
  createText: text => ({ text, props: {}, listeners: {}, children: [], parent: null }),
  createComment: text => ({ comment: text, props: {}, listeners: {}, children: [], parent: null }),
  setText: (node, text) => { node.text = text },
  setElementText: (el, text) => { el.children = [{ text, props: {}, listeners: {}, children: [], parent: el }] },
  insert: (child, parent, anchor) => {
    child.parent = parent
    const at = anchor ? parent.children.indexOf(anchor) : -1
    if (at >= 0) parent.children.splice(at, 0, child)
    else parent.children.push(child)
  },
  remove: child => {
    const parent = child.parent
    if (parent) {
      const index = parent.children.indexOf(child)
      if (index >= 0) parent.children.splice(index, 1)
    }
    child.parent = null
  },
  parentNode: node => node.parent,
  nextSibling: node => {
    const parent = node.parent
    if (!parent) return null
    const index = parent.children.indexOf(node)
    return index >= 0 && index < parent.children.length - 1 ? parent.children[index + 1]! : null
  },
  patchProp: (el, key, _previous, next) => {
    if (key === 'class') {
      if (next == null || next === false) delete el.props.class
      else el.props.class = next
    } else if (key.startsWith('on')) {
      if (typeof next === 'function') el.listeners[key.slice(2).toLowerCase()] = next
      else delete el.listeners[key.slice(2).toLowerCase()]
    } else if (next == null || next === false) {
      delete el.props[key]
    } else {
      el.props[key] = next
    }
  },
  setScopeId: () => undefined,
  cloneNode,
})

const textOf = (node: FakeNode): string => (node.text ?? '') + node.children.map(textOf).join('')
const findAll = (root: FakeNode, predicate: (node: FakeNode) => boolean): FakeNode[] => {
  const out: FakeNode[] = []
  const walk = (node: FakeNode) => { if (predicate(node)) out.push(node); node.children.forEach(walk) }
  walk(root)
  return out
}
const button = (root: FakeNode, attr: string, value: unknown): FakeNode => {
  const found = findAll(root, node => node.tag === 'button' && node.props[attr] === value)[0]
  assert.ok(found, `button ${attr}=${String(value)} should exist`)
  return found
}
const click = (node: FakeNode) => node.listeners.click?.({ type: 'click', preventDefault: () => undefined, stopPropagation: () => undefined })

// ---- fixtures ----
const basis = { project_version: 5, spec_revision: 1, context_revision: 7, asset_versions: [], source_catalog_version: 'cat-1' }
const card = (over: Partial<EvidenceCard> = {}): EvidenceCard => ({
  id: 'card-a', project_id: 'p1', version: 2, anchor_refs: [{ id: 'anchor-1', version: 1 }],
  source_ids: ['src-1'], support_type: 'supports', scope: '第三章 研究方法', scope_chapter_ids: ['ch-3'],
  limitations: '样本量有限', gaps: '', alternative_explanation: '', status: 'CONFIRMED', basis,
  created_by: 'u1', created_at: '2026-10-14T00:00:00Z', claim_ref: { id: 'claim-a', version: 1 }, ...over,
})
const claim = (over: Partial<ResearchClaim> = {}): ResearchClaim => ({
  id: 'claim-a', project_id: 'p1', version: 1, text: '久坐时长与心血管风险显著相关', scope: '成年办公人群',
  status: 'active', basis, reason: 'r', created_by: 'u1', created_at: '2026-10-14T00:00:00Z', ...over,
})
const project = { id: 'p1', name: '久坐与健康', status: 'active', project_version: 5, spec_revision: 1, current_context_revision: 7, spec: {}, template_id: 'tpl-1', template_version: '1', members: [] }
const CLAIM_A = '久坐时长与心血管风险显著相关'
const CLAIM_B = 'B 的主张'

const flush = () => new Promise(resolve => setTimeout(resolve, 0))

function mount(initial: { project: typeof project; currentUserId: string; selected: G2VersionRef[]; onRemove?: (ref: G2VersionRef) => void; onOpen?: (ref: G2VersionRef) => void }) {
  const holder = reactive({ ...initial })
  const root = createFakeElement('root')
  const app = renderer.createApp({ name: 'wrap', setup: () => () => h(SelectedEvidenceContext as never, holder as never) })
  app.mount(root as never)
  return { holder, root, app }
}

const settleNext = async (cards: EvidenceCard[], claims: ResearchClaim[]) => {
  const cardsWait = stub.deferreds.shift()
  const claimsWait = stub.claimWaits.shift()
  assert.ok(cardsWait && claimsWait, 'expected a pending cards+claims request pair')
  cardsWait.resolve({ data: cards })
  claimsWait.resolve({ data: claims })
  await flush()
  await flush()
}

const settleAt = async (index: number, cards: EvidenceCard[], claims: ResearchClaim[]) => {
  stub.cards = cards; stub.claims = claims
  const cardsWait = stub.deferreds[index]
  const claimsWait = stub.claimWaits[index]
  assert.ok(cardsWait && claimsWait, `expected pending request pair at index ${index}`)
  cardsWait.resolve({ data: cards })
  claimsWait.resolve({ data: claims })
  await flush()
}

beforeEach(() => { stub.deferreds = []; stub.claimWaits = []; stub.cards = []; stub.claims = []; stub.cardCalls = 0; stub.projects = [] })

test('R1 场景1：选择 A+B 后移除 B，旧展示立即只剩 A，且不触发无谓重读', async () => {
  const { holder, root } = mount({ project, currentUserId: 'u1', selected: [{ id: 'card-a', version: 2 }, { id: 'card-b', version: 1 }] })
  assert.equal(stub.deferreds.length, 1)
  await settleNext([card(), card({ id: 'card-b', version: 1, claim_ref: { id: 'claim-b', version: 1 } })],
    [claim(), claim({ id: 'claim-b', version: 1, text: CLAIM_B })])
  assert.match(textOf(root), new RegExp(CLAIM_A))
  assert.match(textOf(root), new RegExp(CLAIM_B))
  const callsAfterMount = stub.cardCalls

  holder.selected = [{ id: 'card-a', version: 2 }]
  await flush()
  assert.match(textOf(root), new RegExp(CLAIM_A))
  assert.doesNotMatch(textOf(root), new RegExp(CLAIM_B))
  assert.equal(stub.cardCalls, callsAfterMount)
  assert.equal(stub.deferreds.length, 0)
})

test('R1 场景2：清空全部选择立即清空展示，不发起读取', async () => {
  const { holder, root } = mount({ project, currentUserId: 'u1', selected: [{ id: 'card-a', version: 2 }] })
  await settleNext([card()], [claim()])
  assert.match(textOf(root), new RegExp(CLAIM_A))

  holder.selected = []
  await flush()
  assert.doesNotMatch(textOf(root), new RegExp(CLAIM_A))
  assert.match(textOf(root), /尚未选择用于正文的证据卡/)
  assert.equal(stub.deferreds.length, 0)
  assert.equal(stub.cardCalls, 1)
})

test('R1 场景3：读取在途时取消选择，晚响应不得把已取消的 B 填回', async () => {
  const cards = [card(), card({ id: 'card-b', version: 1, claim_ref: { id: 'claim-b', version: 1 } })]
  const claims = [claim(), claim({ id: 'claim-b', version: 1, text: CLAIM_B })]
  const { holder, root } = mount({ project, currentUserId: 'u1', selected: [{ id: 'card-a', version: 2 }] })
  await settleNext(cards, claims)

  // 加入 B → 在途读取
  holder.selected = [{ id: 'card-a', version: 2 }, { id: 'card-b', version: 1 }]
  await flush()
  assert.equal(stub.deferreds.length, 1)
  assert.match(textOf(root), /正在读取当前证据卡/)

  // 在途时取消 B → 新一代读取取代在途请求
  holder.selected = [{ id: 'card-a', version: 2 }]
  await flush()
  assert.equal(stub.deferreds.length, 2)

  // 晚到的旧响应：B 不得回填，读取中语义由新一代持有
  await settleAt(0, cards, claims)
  assert.doesNotMatch(textOf(root), new RegExp(CLAIM_B))
  assert.match(textOf(root), /正在读取当前证据卡/)

  // 当前响应：只渲染仍选中的 A
  await settleAt(1, cards, claims)
  assert.match(textOf(root), new RegExp(CLAIM_A))
  assert.doesNotMatch(textOf(root), new RegExp(CLAIM_B))
  assert.doesNotMatch(textOf(root), /正在读取当前证据卡/)
})

test('R1 场景4：替换为新版本时旧版本行立即移除，服务端只有旧版则待重新核对且不自动升级', async () => {
  const { holder, root } = mount({ project, currentUserId: 'u1', selected: [{ id: 'card-a', version: 2 }] })
  await settleNext([card()], [claim()])
  assert.match(textOf(root), new RegExp(CLAIM_A))

  holder.selected = [{ id: 'card-a', version: 3 }]
  await flush()
  assert.doesNotMatch(textOf(root), new RegExp(CLAIM_A))
  assert.equal(stub.deferreds.length, 1)

  await settleNext([card()], [claim()]) // 服务端仍只有 v2
  assert.match(textOf(root), /待重新核对/)
  assert.match(textOf(root), /不会自动改用其他版本/)
  assert.match(textOf(root), /v3/)
})

test('R2：请求在途时清空选择，读取中语义立即复位，晚回复不恢复读取中、不回填', async () => {
  const { holder, root } = mount({ project, currentUserId: 'u1', selected: [{ id: 'card-a', version: 2 }] })
  await settleNext([card()], [claim()])

  // 换基线触发一次新的在途读取
  holder.project = { ...project, current_context_revision: 8 }
  await flush()
  assert.equal(stub.deferreds.length, 1)
  assert.match(textOf(root), /正在读取当前证据卡/)

  // 在途时清空全部选择
  holder.selected = []
  await flush()
  assert.doesNotMatch(textOf(root), /正在读取当前证据卡/)
  assert.match(textOf(root), /尚未选择用于正文的证据卡/)

  // 旧请求的晚回复：不得恢复读取中或回填任何内容
  await settleAt(0, [card()], [claim()])
  assert.doesNotMatch(textOf(root), new RegExp(CLAIM_A))
  assert.doesNotMatch(textOf(root), /正在读取当前证据卡/)
})

test('跨课题切换：旧课题的晚响应不回填，新课题读取完成后正常渲染', async () => {
  const { holder, root } = mount({ project, currentUserId: 'u1', selected: [{ id: 'card-a', version: 2 }] })
  assert.equal(stub.deferreds.length, 1)
  assert.match(textOf(root), /正在读取当前证据卡/)

  holder.project = { ...project, id: 'p2' }
  await flush()
  assert.equal(stub.projects.filter(id => id === 'p2').length, 1)
  assert.equal(stub.deferreds.length, 2)

  // p1 的晚响应被代次丢弃
  await settleAt(0, [card()], [])
  assert.doesNotMatch(textOf(root), new RegExp(CLAIM_A))
  assert.match(textOf(root), /正在读取当前证据卡/)

  // p2 的当前响应正常渲染
  await settleAt(1, [card()], [claim()])
  assert.match(textOf(root), new RegExp(CLAIM_A))
  assert.doesNotMatch(textOf(root), /正在读取当前证据卡/)
})

test('读取失败（source_access_denied）清除旧可读内容并提示受限，不回退旧正文', async () => {
  const { holder, root } = mount({ project, currentUserId: 'u1', selected: [{ id: 'card-a', version: 2 }] })
  await settleNext([card()], [claim()])
  assert.match(textOf(root), new RegExp(CLAIM_A))

  holder.project = { ...project, current_context_revision: 9 }
  await flush()
  assert.equal(stub.deferreds.length, 1)
  stub.deferreds[0]!.reject({ code: 'source_access_denied' })
  stub.claimWaits[0]!.reject({ code: 'source_access_denied' })
  await flush()
  assert.doesNotMatch(textOf(root), new RegExp(CLAIM_A))
  assert.match(textOf(root), /当前无法核验：来源访问受限/)
  assert.doesNotMatch(textOf(root), /正在读取当前证据卡/)
})

test('按钮事件：单独移除与回看各自发出原始 ref，组件不自行修改 selected', async () => {
  const removed: G2VersionRef[] = []
  const opened: G2VersionRef[] = []
  const { holder, root } = mount({
    project, currentUserId: 'u1', selected: [{ id: 'card-a', version: 2 }],
    onRemove: (ref: G2VersionRef) => removed.push(ref),
    onOpen: (ref: G2VersionRef) => opened.push(ref),
  })
  await settleNext([card()], [claim()])

  click(button(root, 'data-remove', 'card-a'))
  click(button(root, 'data-open', 'card-a'))
  assert.equal(removed.length, 1)
  assert.deepEqual(removed[0], { id: 'card-a', version: 2 })
  assert.equal(opened.length, 1)
  assert.deepEqual(opened[0], { id: 'card-a', version: 2 })
  assert.equal(holder.selected.length, 1)
})
