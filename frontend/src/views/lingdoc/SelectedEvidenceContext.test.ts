import assert from 'node:assert/strict'
import { readFileSync, unlinkSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { beforeEach, test } from 'node:test'
import { pathToFileURL, fileURLToPath } from 'node:url'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { compileScript, parse } from '@vue/compiler-sfc'
import { transformSync } from 'esbuild'
import { createRequestEpoch, matchSelectedRows } from './selectedEvidenceContextState'
import type { EvidenceCard, VersionRef, ResearchClaim } from '@/api/lingdoc/agentApi'

// The component under test is compiled from its real .vue source at runtime so
// assertions exercise the same markup a browser receives. Its read-only API is
// stubbed so no network or model is touched. Real click/scroll behavior remains
// a browser check; SSR rendering does not replace browser verification.
const here = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(join(here, 'SelectedEvidenceContext.vue'), 'utf8')
const { descriptor, errors } = parse(source, { filename: 'SelectedEvidenceContext.vue' })
assert.deepEqual(errors, [])
const compiled = compileScript(descriptor, { id: 'selected-evidence-test', inlineTemplate: true })

const stubName = `.selected-evidence-api-stub.${process.pid}.mjs`
const stubSource = `export const impl = { cards: [], claims: [], cardError: null, claimError: null, cardCalls: 0 }
export const listEvidenceCards = async () => { impl.cardCalls += 1; if (impl.cardError) throw impl.cardError; return { data: impl.cards } }
export const listResearchClaims = async () => { if (impl.claimError) throw impl.claimError; return { data: impl.claims } }
`
writeFileSync(join(here, stubName), stubSource)

const withoutTypeImports = compiled.content.split('\n').filter(line => !line.startsWith('import type ')).join('\n')
const rewritten = withoutTypeImports.replace(/from '@\/api\/lingdoc\/agentApi'/g, `from './${stubName}'`)
assert.ok(rewritten.includes(`from './${stubName}'`))
const runnable = transformSync(rewritten, { loader: 'ts', format: 'esm' }).code

const tmpFile = join(here, `.selected-evidence.compiled.${process.pid}.mjs`)
writeFileSync(tmpFile, runnable)
let SelectedEvidenceContext: new (...args: never[]) => unknown
const stub = (await import(pathToFileURL(join(here, stubName)).href)).impl as {
  cards: EvidenceCard[]; claims: ResearchClaim[]; cardError: unknown; claimError: unknown; cardCalls: number
}
try {
  SelectedEvidenceContext = (await import(pathToFileURL(tmpFile).href)).default
} finally {
  unlinkSync(tmpFile)
  unlinkSync(join(here, stubName))
}

// remove/open emit wiring is tied to each row's exact ref object.
assert.match(runnable, /emit\(['"]remove['"], row\.ref\)/)
assert.match(runnable, /emit\(['"]open['"], row\.ref\)/)

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

async function render(props: Record<string, unknown>) {
  return renderToString(createSSRApp(SelectedEvidenceContext as never, props) as never)
}
const baseProps = (selected: VersionRef[], extra: Record<string, unknown> = {}) => ({
  project, currentUserId: 'u1', selected, ...extra,
})

beforeEach(() => { stub.cards = []; stub.claims = []; stub.cardError = null; stub.claimError = null })

test('ready 行展示主张、关系、版本、范围与局限，且未选中的卡/主张不出现', async () => {
  stub.cards = [card(), card({ id: 'card-b', version: 1, claim_ref: { id: 'claim-b', version: 1 } })]
  stub.claims = [claim(), claim({ id: 'claim-b', version: 1, text: '未选中卡的主张文本' })]
  const html = await render(baseProps([{ id: 'card-a', version: 2 }]))
  assert.match(html, /久坐时长与心血管风险显著相关/)
  assert.match(html, /data-relation="supports">支持</)
  assert.match(html, /证据卡 v2/)
  assert.match(html, /范围：第三章 研究方法/)
  assert.match(html, /局限：样本量有限/)
  assert.match(html, /data-open="card-a"/)
  assert.match(html, /data-remove="card-a"/)
  assert.match(html, /回看证据/)
  assert.match(html, /取消本次选用/)
  assert.doesNotMatch(html, /未选中卡的主张文本/)
})

test('background 关系如实显示为“背景”，不冒充支持', async () => {
  stub.cards = [card({ support_type: 'background' })]
  stub.claims = [claim()]
  const html = await render(baseProps([{ id: 'card-a', version: 2 }]))
  assert.match(html, /data-relation="background">背景</)
  assert.doesNotMatch(html, /data-relation="background">支持</)
})

test('选中版本与服务端不一致时待重新核对，不自动升级、不展示旧内容', async () => {
  stub.cards = [card({ version: 2 })]
  stub.claims = [claim()]
  const html = await render(baseProps([{ id: 'card-a', version: 1 }]))
  assert.match(html, /待重新核对/)
  assert.match(html, /不会自动改用其他版本/)
  assert.match(html, /v1/)
  assert.doesNotMatch(html, /久坐时长与心血管风险显著相关/)
})

test('证据卡非 CONFIRMED 时待重新核对并标明当前状态', async () => {
  stub.cards = [card({ status: 'NEEDS_REVIEW' })]
  stub.claims = [claim()]
  const html = await render(baseProps([{ id: 'card-a', version: 2 }]))
  assert.match(html, /待重新核对/)
  assert.match(html, /需复核/)
  assert.match(html, /尚未人工确认适用范围/)
  assert.doesNotMatch(html, /久坐时长与心血管风险显著相关/)
})

test('关联主张在所选版本缺失时显示当前无法核验', async () => {
  stub.cards = [card({ claim_ref: { id: 'claim-a', version: 9 } })]
  stub.claims = [claim()]
  const html = await render(baseProps([{ id: 'card-a', version: 2 }]))
  assert.match(html, /当前无法核验/)
  assert.match(html, /关联主张在所选版本不可用/)
})

test('读取报 source_access_denied 时清空展示并明确来源受限', async () => {
  stub.cardError = { code: 'source_access_denied' }
  stub.claims = [claim()]
  const html = await render(baseProps([{ id: 'card-a', version: 2 }]))
  assert.match(html, /当前无法核验：来源访问受限/)
  assert.doesNotMatch(html, /久坐时长与心血管风险显著相关/)
})

test('readonly 模式只保留回看入口，不渲染移除按钮', async () => {
  stub.cards = [card()]
  stub.claims = [claim()]
  const html = await render(baseProps([{ id: 'card-a', version: 2 }], { readonly: true }))
  assert.match(html, /data-open="card-a"/)
  assert.match(html, /回看证据/)
  assert.doesNotMatch(html, /data-remove=/)
  assert.doesNotMatch(html, /取消本次选用/)
})

test('未选择任何证据卡时给出提示而不是空块', async () => {
  const html = await render(baseProps([]))
  assert.match(html, /尚未选择用于正文的证据卡/)
})

test('长主张与长范围词按布局规则换行，不撑破 390px 列', async () => {
  const longText = '超长主张'.repeat(80)
  stub.cards = [card({ scope: `very-long-scope-identifier-${'x'.repeat(200)}` })]
  stub.claims = [claim({ text: longText })]
  const html = await render(baseProps([{ id: 'card-a', version: 2 }]))
  assert.ok(html.includes(longText))
  const styleBlock = descriptor.styles.map(block => block.content).join('\n')
  assert.match(styleBlock, /overflow-wrap:\s*anywhere/)
  assert.match(styleBlock, /word-break:\s*break-word/)
  assert.match(styleBlock, /@media \(max-width:480px\)/)
  assert.match(styleBlock, /:focus-visible/)
})

test('matchSelectedRows 只返回选中的 refs 并去重', () => {
  const cards = [card(), card({ id: 'card-b', version: 1, claim_ref: { id: 'claim-b', version: 1 } })]
  const claims = [claim()]
  const rows = matchSelectedRows([{ id: 'card-a', version: 2 }, { id: 'card-a', version: 2 }], cards, claims)
  assert.equal(rows.length, 1)
  assert.equal(rows[0]!.state, 'ready')
  const unmatched = matchSelectedRows([{ id: 'card-b', version: 1 }], cards, claims)
  assert.equal(unmatched.length, 1)
  assert.equal(unmatched[0]!.state, 'claim-missing')
})

test('请求代次丢弃晚响应，不跨基线回填', () => {
  const epoch = createRequestEpoch()
  const first = epoch.capture()
  epoch.next() // 模拟课题/身份切换
  assert.equal(epoch.accepts(first), false)
  const second = epoch.capture()
  epoch.next()
  assert.equal(epoch.accepts(second), false)
  assert.equal(epoch.accepts(epoch.capture()), true)
})
