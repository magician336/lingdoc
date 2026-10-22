import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
const require = createRequire(import.meta.url)
const __filename = fileURLToPath(import.meta.url)
const __dirname = require('node:path').dirname(__filename)

// The actual Vue component and the actual typed API modules.  HTTP is an
// explicit synthetic fixture; no browser, live backend or model call happens.
const test = require('node:test'), assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path'), Module = require('node:module')
const frontend = path.resolve(__dirname, '../../..'), load = Module.createRequire(path.join(frontend, 'package.json')), ts = load('typescript'), compiler = load('@vue/compiler-sfc'), vue = load('vue'), { renderToString } = load('@vue/server-renderer')

let fixture = null
function request(method, url, body, config) {
  fixture.requests.push({ method, url, body: body && JSON.parse(JSON.stringify(body)), config: config && JSON.parse(JSON.stringify(config)) })
  return Promise.resolve({ data: [], meta: { replayed: false, refresh_required: false } })
}
function compile(filename, source, resolve) {
  const m = new Module(filename)
  m.paths = Module._nodeModulePaths(frontend)
  m.require = resolve || load
  m._compile(ts.transpileModule(source ?? fs.readFileSync(filename, 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText, filename)
  return m.exports
}

// The real API modules are compiled against a synthetic transport, so the
// assertions below hold the *actual* URLs and request bodies -- not a copy of
// them written into the fixture.
function report(overrides) {
  return {
    report_id: 'report-1', project_id: 'project-1', chapter_id: 'chapter-1',
    findings: [
      { id: 'finding-1', statement: '第一句偏长，建议拆成两句。', origin_candidate_id: 'candidate-1' },
      { id: 'finding-2', statement: '第二处的术语与全文不一致。', origin_candidate_id: 'candidate-1' },
    ],
    source_ids: ['source-1'], authorized_sources: [],
    baseline: { working_copy_revision: 3, base_chapter_version_id: null, spec_revision: 2, body_hash: 'a'.repeat(64) },
    report_version: 'report-version-1', authorization_state: 'fresh', run_mode: 'mock',
    writing_action: 'review', origin: 'ai_generated', state: 'ready', review_decisions: [],
    error_code: '', scope: 'section',
    selection: { start_utf16: 0, end_utf16: 10, selected_text: '研究问题：' },
    created_at: '2026-10-22T01:00:00Z',
    ...overrides,
  }
}
function workingCopy() {
  return {
    project_id: 'project-1', chapter_id: 'chapter-1', base_chapter_version_id: null,
    spec_revision: 2, working_copy_revision: 4, body_markdown: '研究问题：改写后的正文',
    source_ids: ['source-1'], review_items: [], updated_at: '2026-10-22T02:00:00Z',
  }
}
function fakeTransport(method, url, body, config) {
  fixture.requests.push({ method, url, body: body && JSON.parse(JSON.stringify(body)), config: config && JSON.parse(JSON.stringify(config)) })
  const envelope = data => ({ data, meta: { replayed: false, refresh_required: false } })
  if (url.endsWith('/reject')) return Promise.resolve(envelope(report({ state: 'rejected' })))
  if (url.endsWith('/apply')) return Promise.resolve(envelope(workingCopy()))
  if (url.endsWith('/review-reports')) return Promise.resolve(envelope(report()))
  return Promise.resolve(envelope(report()))
}
const requestStub = {
  get: (url, config) => fakeTransport('GET', url, undefined, config),
  post: (url, body, config) => fakeTransport('POST', url, body, config),
  postNoBody: (url, config) => fakeTransport('POST', url, undefined, config),
  put: (url, body, config) => fakeTransport('PUT', url, body, config),
}
const workspaceApi = compile(path.join(frontend, 'src/api/lingdoc/workspace.ts'), undefined, id => id === '@/utils/request' ? requestStub : load(id))
const candidateApi = compile(path.join(frontend, 'src/api/lingdoc/candidateAdoption.ts'), undefined, id => id === '@/utils/request' ? requestStub : load(id))

const source = path.join(__dirname, 'LingDocReviewReportPanel.vue'), parsed = compiler.parse(fs.readFileSync(source, 'utf8')); assert.deepEqual(parsed.errors, [])
const script = compiler.compileScript(parsed.descriptor, { id: 'g7-review-report-panel-test' }), template = compiler.compileTemplate({ source: parsed.descriptor.template.content, filename: source, id: 'g7-review-report-panel-test', compilerOptions: { bindingMetadata: script.bindings }, isTS: true }); assert.deepEqual(template.errors, [])
const panel = compile(source, script.content, id => id === '@/api/lingdoc/workspace' ? workspaceApi : id === '@/api/lingdoc/candidateAdoption' ? candidateApi : load(id)).default
panel.render = compile(source + '.template', template.code).render

async function render(overrides, seed) {
  fixture = { requests: [] }
  const props = vue.reactive({
    projectId: 'project-1', chapterId: 'chapter-1', scope: 'section',
    resolveSelection: () => ({ start_utf16: 0, end_utf16: 10, selected_text: '研究问题：' }),
    expectedSpecRevision: 2, expectedWorkingCopyRevision: 3, baseChapterVersionId: null,
    ...overrides,
  })
  const events = []; let controller
  // The seed runs inside setup: an SSR pass builds a fresh instance, so state
  // written after the first render would not be in the markup.
  const component = { ...panel, setup(input, context) { controller = panel.setup(input, context); if (seed) seed(controller); return controller } }
  const app = vue.createSSRApp(component, { ...props, onApplied: copy => events.push(copy) })
  const html = await renderToString(app)
  return { html, events, controller, props, fixture, rerender: async () => renderToString(app) }
}

test('the panel starts a review with the scope, the selection and the expected revisions', async () => {
  const { controller, fixture } = await render()
  await controller.create()
  assert.equal(fixture.requests.length, 1)
  const request = fixture.requests[0]
  assert.equal(request.method, 'POST')
  assert.equal(request.url, '/api/v1/lingdoc/projects/project-1/chapters/chapter-1/review-reports')
  assert.equal(request.body.scope, 'section')
  assert.deepEqual(request.body.selection, { start_utf16: 0, end_utf16: 10, selected_text: '研究问题：' })
  assert.equal(request.body.expected_spec_revision, 2)
  assert.equal(request.body.expected_working_copy_revision, 3)
  // 写操作带幂等键：同键同请求是重放，同键不同请求是冲突。
  assert.ok(request.config.headers['Idempotency-Key'])
})

test('the panel asks for the selection when it starts, not while it renders', async () => {
  // 选区的映射要经编辑器的 UTF-16 映射与三重验证：渲染页面上算一次太贵。
  let asked = 0
  const { controller, fixture } = await render({
    resolveSelection: () => { asked += 1; return { start_utf16: 0, end_utf16: 10, selected_text: '研究问题：' } },
  })
  assert.equal(asked, 0)
  await controller.create()
  assert.equal(asked, 1)
  assert.equal(fixture.requests.length, 1)
})

test('the panel says so when the section scope has no selection', async () => {
  const { controller, fixture } = await render({ resolveSelection: () => undefined })
  await controller.create()
  // 没有选区就不发请求：不猜范围。
  assert.equal(fixture.requests.length, 0)
  assert.match(controller.error.value, /选中一段/)
})

test('the panel starts a whole-chapter review without a selection', async () => {
  const { controller, fixture } = await render({ scope: 'chapter' })
  await controller.create()
  assert.equal(fixture.requests[0].body.scope, 'chapter')
  assert.equal(fixture.requests[0].body.selection, undefined)
})

test('the panel shows the five facts of the report', async () => {
  const { html } = await render({}, controller => { controller.report.value = report() })
  // 评审意见、来源、基线版本、报告版本、撤权状态。
  assert.match(html, /第一句偏长/)
  assert.match(html, /第二处的术语与全文不一致/)
  assert.match(html, /source-1/)
  assert.match(html, /工作副本第 3 版/)
  assert.match(html, /report-version-1/)
  assert.match(html, /授权有效/)
})

test('the panel says the authorization is gone when it is gone', async () => {
  const { html } = await render({}, controller => { controller.report.value = report({ authorization_state: 'not_authorized' }) })
  assert.match(html, /授权已失效/)
  assert.doesNotMatch(html, /授权有效/)
})

test('the panel refuses to apply a report whose findings are not all disposed of', async () => {
  const { controller, fixture } = await render()
  controller.report.value = report()
  // 处置齐了但理由没写：服务端要求每一条都有处置**与**理由。
  controller.setDisposition('finding-1', 'resolved')
  controller.setReason('finding-1', '已改写')
  assert.equal(controller.decisionsComplete.value, false)
  await controller.apply()
  assert.equal(fixture.requests.length, 0)

  // 只处置了一条：另一条还没人看过。
  controller.setDisposition('finding-2', 'retained_warning')
  controller.setReason('finding-2', '保留原表述')
  assert.equal(controller.decisionsComplete.value, true)
  await controller.apply()
  assert.equal(fixture.requests.length, 1)
  assert.equal(fixture.requests[0].url, '/api/v1/lingdoc/projects/project-1/chapters/chapter-1/review-reports/report-1/apply')
  assert.deepEqual(fixture.requests[0].body.review_decisions, [
    { review_item_id: 'finding-1', disposition: 'resolved', reason: '已改写' },
    { review_item_id: 'finding-2', disposition: 'retained_warning', reason: '保留原表述' },
  ])
})

test('the panel rejects a report without touching the working copy', async () => {
  const { controller, fixture } = await render()
  controller.report.value = report()
  await controller.reject()
  assert.equal(fixture.requests.length, 1)
  assert.equal(fixture.requests[0].method, 'POST')
  assert.equal(fixture.requests[0].url, '/api/v1/lingdoc/projects/project-1/review-reports/report-1/reject')
  assert.ok(fixture.requests[0].config.headers['Idempotency-Key'])
  // 拒绝不发任何写工作副本的请求。
  assert.ok(fixture.requests.every(item => !item.url.endsWith('/apply')))
})

test('the panel reads the report back before it is applied', async () => {
  const { controller, fixture } = await render()
  controller.report.value = report()
  await controller.preview()
  assert.equal(fixture.requests[0].method, 'GET')
  assert.equal(fixture.requests[0].url, '/api/v1/lingdoc/projects/project-1/review-reports/report-1')
})

test('the workspace actually mounts the review report panel', () => {
  // 缺口：四个评审端点与服务端都齐了，页面上却点不到。这条守卫钉的是引用
  // 本身，而不是它的行为。
  const workspace = fs.readFileSync(path.join(__dirname, 'Workspace.vue'), 'utf8')
  assert.match(workspace, /import LingDocReviewReportPanel from '\.\/LingDocReviewReportPanel\.vue'/)
  assert.match(workspace, /<LingDocReviewReportPanel/)
  assert.match(workspace, /@applied="onReviewReportApplied"/)
})
