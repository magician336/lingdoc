import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import { compileTemplate } from '@vue/compiler-sfc'
import ts from 'typescript'

// #143 TD-07 的三个前端子面：立项对话框的差异两类列表与过期确认恢复、
// 丢弃/恢复列表。夹具从 Workspace.vue 源文件里切出真实的模板片段做 SSR
// 渲染——不是复制的假模板；Workspace.vue 本体零改动（挂载仍走保留路径）。
// 只证明呈现结构与状态投影，不证明浏览器或服务端行为。

const require = createRequire(import.meta.url)
const workspacePath = fileURLToPath(new URL('./Workspace.vue', import.meta.url))
const workspaceSource = readFileSync(workspacePath, 'utf8')

function slice(startMark: string, endMark: RegExp): string {
  const start = workspaceSource.indexOf(startMark)
  assert.ok(start >= 0, `missing start mark ${startMark}`)
  const end = endMark.exec(workspaceSource.slice(start))
  assert.ok(end, `missing end mark ${endMark}`)
  return workspaceSource.slice(start, start + (end.index + end[0].length))
}

function render(sliceSource: string, ctx: Record<string, unknown>): string {
  const compiled = compileTemplate({
    source: sliceSource,
    filename: workspacePath,
    id: 'workspace-slice-test',
    ssr: true,
  })
  assert.deepEqual(compiled.errors, [])
  const js = ts.transpileModule(compiled.code, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText
  const sandbox: Record<string, unknown> = { exports: {}, require }
  runInNewContext(js, sandbox)
  const templateExports = sandbox.exports as { ssrRender: (ctx: unknown, push: (chunk: string) => void, parent: null, metadata: Record<string, never>) => void }
  let out = ''
  templateExports.ssrRender(ctx, (chunk: string) => { out += chunk }, null, {})
  return out
}

const activationSlice = slice(
  '<div v-if="activationOpen && activationDiff && project" ref="activationDialogRef"',
  /<\/section>\r?\n\s*<\/div>/,
)
const discardedSlice = slice('<details class="discarded-drafts"', /<\/details>/)

const diff = (projectVersion: number) => ({
  current_project_version: 2,
  current_spec_revision: 1,
  changed_fields: [
    { key: 'research_subject', value: '人工主题', origin: 'human', status: 'draft', modified_by: 'user-b', modified_at: '2026-10-27T01:00:00Z' },
    { key: 'research_goal', value: 'AI 目标', origin: 'ai_generated', status: 'pending_confirmation', modified_by: 'user-b', modified_at: '2026-10-27T01:00:00Z' },
  ],
  pending_ai_fields: [
    { key: 'research_goal', value: 'AI 目标', origin: 'ai_generated', status: 'pending_confirmation', modified_by: 'user-b', modified_at: '2026-10-27T01:00:00Z' },
  ],
  ...(projectVersion === 2 ? {} : {}),
})

function activationCtx(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    activationOpen: true,
    busy: false,
    activationAcknowledged: true,
    auth: { currentUserId: 'user-a' },
    conditionLabel: (key: string) => ({ research_subject: '研究对象', research_goal: '研究目标' }[key] ?? key),
    closeActivationReview: () => {},
    confirmActivationReview: () => {},
    activationDiff: diff(2),
    project: {
      id: 'p1', project_version: 2,
      spec_fields: { research_goal: { provenance: { source_type: 'conversation', source_id: 'session-20', locator: 'turns/20#text', needs_review: false } } },
    },
    ...overrides,
  }
}

test('立项对话框把修改字段与待确认 AI 字段分列，他人修改单独列出并显示来源', () => {
  const html = render(activationSlice, activationCtx())
  assert.match(html, /研究对象/)
  assert.match(html, /研究目标/)
  assert.match(html, /其他成员的修改/)
  assert.match(html, /user-b/)
  assert.match(html, /有 1 项 AI 辅助字段待你核对/)
  assert.match(html, /来源：conversation/)
  assert.match(html, /仅为记录，不证明研究结论/)
})

test('课题版本已变化时对话框显示过期告警并禁用立项按钮', () => {
  const stale = activationCtx({ project: { id: 'p1', project_version: 3, spec_fields: {} } })
  const html = render(activationSlice, stale)
  assert.match(html, /课题已变化；请返回并重新查看/)
  assert.match(html, /disabled[^>]*>确认并立项|<button[^>]*disabled[^>]*>正在立项…|<button[^>]*disabled[^>]*> ?确认并立项/)
})

test('确认条件齐备时立项按钮可点，不出现 disabled', () => {
  const html = render(activationSlice, activationCtx())
  const button = html.slice(html.indexOf('确认并立项') - 400, html.indexOf('确认并立项') + 20)
  assert.ok(button.includes('<button'), button)
  assert.ok(!/disabled[^>]*>[\s\S]*确认并立项/.test(html), '确认按钮不应禁用')
})

test('丢弃列表渲染可恢复草稿，空列表显示明确空态', () => {
  const filled = render(discardedSlice, { discardedOpen: true, busy: false, discardedDrafts: [{ id: 'd1', name: '旧草稿' }], restoreDiscarded: () => {}, showDiscarded: () => {} })
  assert.match(filled, /旧草稿/)
  assert.match(filled, /恢复草稿/)
  assert.ok(!/disabled[^>]*>恢复草稿/.test(filled), '恢复按钮不应禁用')
  const empty = render(discardedSlice, { discardedOpen: true, busy: false, discardedDrafts: [], restoreDiscarded: () => {}, showDiscarded: () => {} })
  assert.match(empty, /没有可恢复的草稿/)
})
