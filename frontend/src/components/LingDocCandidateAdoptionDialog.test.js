import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
const require = createRequire(import.meta.url)
const __filename = fileURLToPath(import.meta.url)
const __dirname = require('node:path').dirname(__filename)

// Actual Vue script/template execution with a synthetic HTTP fixture: no browser,
// no live backend, no model call.
const test = require('node:test'), assert = require('node:assert/strict')
const fs = require('node:fs'), path = require('node:path'), Module = require('node:module')
const frontend = path.resolve(__dirname, '../..'), load = Module.createRequire(path.join(frontend, 'package.json'))
const ts = load('typescript'), compiler = load('@vue/compiler-sfc'), vue = load('vue'), { renderToString } = load('@vue/server-renderer')

const clone = v => JSON.parse(JSON.stringify(v))
let fixture, nonce = 0

function request(method, url, body, config) {
  fixture.requests.push({ method, url, body: body && clone(body), config: config && clone(config) })
  if (method === 'POST') {
    return Promise.resolve({ data: clone(fixture.chapter), request_id: 'synthetic', meta: { replayed: false, refresh_required: false } })
  }
  return Promise.resolve({ data: clone([fixture.chapter]), request_id: 'synthetic', meta: { replayed: false, refresh_required: false } })
}

function compile(filename, source, resolve) {
  const m = new Module(filename); m.paths = Module._nodeModulePaths(frontend); m.require = resolve || load
  m._compile(ts.transpileModule(source ?? fs.readFileSync(filename, 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true },
  }).outputText, filename)
  return m.exports
}

const api = compile(path.join(frontend, 'src/api/lingdoc/candidateAdoption.ts'), undefined,
  id => id === '@/utils/request'
    ? { get: (url, c) => request('GET', url, undefined, c), post: (url, b, c) => request('POST', url, b, c) }
    : load(id))

const wikiLineDiff = compile(path.join(frontend, 'src/utils/wikiLineDiff.ts'), undefined, id => load(id))

const source = path.join(frontend, 'src/components/LingDocCandidateAdoptionDialog.vue')
const parsed = compiler.parse(fs.readFileSync(source, 'utf8'))
assert.deepEqual(parsed.errors, [])
const script = compiler.compileScript(parsed.descriptor, { id: 'candidate-adoption-test' })
const template = compiler.compileTemplate({
  source: parsed.descriptor.template.content, filename: source, id: 'candidate-adoption-test',
  compilerOptions: { bindingMetadata: script.bindings }, isTS: true,
})
assert.deepEqual(template.errors, [])

const dialog = compile(source, script.content, id => {
  if (id === '@/api/lingdoc/candidateAdoption') return { ...api, newIdempotencyKey: () => `synthetic-key-${++nonce}` }
  if (id === '@/views/lingdoc/candidateAdoption') return { keyForAttempt: (attempt, input, mint) => ({ signature: JSON.stringify(input), key: mint() }) }
  if (id === '@/utils/wikiLineDiff') return wikiLineDiff
  if (id === '@/stores/auth') return { useAuthStore: () => ({ currentUserId: 'user-1' }) }
  return load(id)
}).default
dialog.render = compile(source + '.template', template.code).render

function candidate(overrides = {}) {
  return {
    id: 'candidate-1', project_id: 'project-1', chapter_id: 'chapter-1', run_id: 'run-1',
    body_markdown: '新的正文\n第二段', source_ids: ['source-1'],
    basis: { spec_revision: 1, chapter_version_id: 'version-1', template_id: 't', template_version: '1', ruleset_hash: '', asset_versions: [] },
    validity: 'fresh', review_items: [], origin: 'ai_generated', writing_action: 'generate',
    ...overrides,
  }
}

function chapter(overrides = {}) {
  return {
    id: 'chapter-1', project_id: 'project-1', section_id: 'question', title: '研究问题',
    current_version_id: 'version-1', body_markdown: '旧的正文\n保留段', source_ids: [],
    review_items: [], confirmation_valid: false,
    ...overrides,
  }
}

async function render(props = {}) {
  const passed = {
    projectId: 'project-1', chapter: chapter(), candidate: candidate(),
    expectedSpecRevision: 1, open: true, ...props,
  }
  fixture = { requests: [], chapter: passed.chapter }
  const component = { ...dialog }
  const html = await renderToString(vue.createSSRApp(component, passed))
  return { html, requests: clone(fixture.requests) }
}

test('a whole adoption shows the replacement range and the line diff before it runs', async () => {
  const { html } = await render()
  // 替换范围：整段采纳时是整篇。
  assert.match(html, /替换范围：整篇/)
  // 行级差异：删掉的旧行与新增的新行都在。
  assert.match(html, /旧的正文/)
  assert.match(html, /新的正文/)
  assert.match(html, /candidate-adoption__diff--del/)
  assert.match(html, /candidate-adoption__diff--add/)
})

test('the dialog states what is being confirmed', async () => {
  const { html } = await render()
  assert.match(html, /动作：generate/)
  assert.match(html, /写入目标：正式版本·整段/)
  assert.match(html, /确认人：user-1/)
  assert.match(html, /来源标注：AI 生成/)
})

test('a candidate without a recorded action cannot be adopted', async () => {
  const { html } = await render({ candidate: candidate({ writing_action: undefined }) })
  assert.match(html, /这个候选没有记录产生它的动作或来源，无法确认采纳/)
  assert.match(html, /disabled/)
})
