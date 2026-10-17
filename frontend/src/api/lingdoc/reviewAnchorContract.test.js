import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
const require = createRequire(import.meta.url)
const __filename = fileURLToPath(import.meta.url)
const __dirname = require('node:path').dirname(__filename)

// Contract/client tests only. These do not start HTTP, execute Go or validate browser paint.
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const test = require('node:test')
const ts = require('typescript')
const Ajv = require('ajv')
const root = path.resolve(__dirname, '../../../..')
const spec = JSON.parse(fs.readFileSync(path.join(root, 'docs/08-本轮实施方案/contracts/openapi.json'), 'utf8'))
const getPath = '/projects/{projectId}/chapters/{chapterId}/confirmations/current'
const putPath = '/projects/{projectId}/chapters/{chapterId}/working-copy/review-items/{itemId}/anchor'
const get = spec.paths[getPath]?.get
const put = spec.paths[putPath]?.put
const ajv = new Ajv({ strict: false, validateFormats: false })
ajv.addSchema(spec, 'lingdoc-contract')
const validate = schema => ajv.compile(schema)
const check = (schema, value) => {
  const accepts = validate(schema)
  assert.equal(accepts(value), true, JSON.stringify(accepts.errors))
}
const reject = (schema, value) => assert.equal(validate(schema)(value), false)
const getMedia = () => get.responses['200'].content['application/json']
const putMedia = () => put.responses['200'].content['application/json']
const requestMedia = () => put.requestBody.content['application/json']
// Register the complete root with Ajv so local schema references resolve exactly as published.
function rooted(schema) {
  return { ...schema, components: spec.components }
}
const requests = []
let response
const request = new Proxy({}, { get: (_, method) => (...args) => {
  requests.push({ method, args })
  return Promise.resolve(response)
} })
const exportsBox = { exports: {} }
const clientFile = path.join(__dirname, 'workspace.ts')
const client = ts.transpileModule(fs.readFileSync(clientFile, 'utf8'), { fileName: clientFile, compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
new Function('require', 'module', 'exports', client)(id => {
  assert.equal(id, '@/utils/request')
  return request
}, exportsBox, exportsBox.exports)
const api = exportsBox.exports

function assertParameters(op, names, write) {
  assert.deepEqual(op.parameters.filter(p => p.in === 'path').map(p => p.name).sort(), [...names].sort())
  assert.ok(op.parameters.filter(p => p.in === 'path').every(p => p.required && p.schema.minLength === 1))
  assert.equal(op.parameters.some(p => p.in === 'header' && p.name === 'Idempotency-Key' && p.required), write)
}

test('published paths agree with registered Go routes and preserve read/write capabilities', () => {
  assert.equal(get.operationId, 'getCurrentConfirmation')
  assert.equal(put.operationId, 'setReviewItemAnchor')
  assertParameters(get, ['projectId', 'chapterId'], false)
  assertParameters(put, ['projectId', 'chapterId', 'itemId'], true)
  assert.equal(get.requestBody, undefined)
  assert.equal(put.requestBody.required, true)
  const http = fs.readFileSync(path.join(root, 'internal/lingdoc/workspace/http.go'), 'utf8')
  assert.ok(http.includes('routes.Read.GET("/projects/:projectId/chapters/:chapterId/confirmations/current", h.getCurrentConfirmation)'))
  assert.ok(http.includes('routes.Write.PUT("/projects/:projectId/chapters/:chapterId/working-copy/review-items/:itemId/anchor", h.setReviewAnchor)'))
  const domain = fs.readFileSync(path.join(root, 'internal/lingdoc/workspacecore/review_anchor.go'), 'utf8')
  assert.ok(domain.includes('"write:"+chapterID'))
  assert.ok(domain.includes('"read"'))
  assert.deepEqual(spec.security, [{ bearerAuth: [] }])
})

test('GET accepts current confirmation and explicit null, rejecting an incomplete confirmation', () => {
  const media = getMedia()
  for (const example of Object.values(media.examples)) check(rooted(media.schema), example.value)
  assert.equal(media.examples.none.value.data, null)
  const incomplete = structuredClone(media.examples.current.value)
  delete incomplete.data.review_decisions
  reject(rooted(media.schema), incomplete)
  reject(rooted(media.schema), { ...media.examples.none.value, data: {} })
  const invalidated = structuredClone(media.examples.current.value); invalidated.data.valid = false
  reject(rooted(media.schema), invalidated)
})

test('PUT selection and explicit null clear share the exact version/revision request contract', () => {
  const media = requestMedia()
  check(rooted(media.schema), media.example)
  check(rooted(media.schema), { ...media.example, anchor: null })
  check(rooted(media.schema), { ...media.example, expected_chapter_version_id: null, anchor: null })
  const missing = structuredClone(media.example); delete missing.anchor
  reject(rooted(media.schema), missing)
  reject(rooted(media.schema), { ...media.example, expected_working_copy_revision: 0 })
  reject(rooted(media.schema), { ...media.example, expected_spec_revision: -1 })
  reject(rooted(media.schema), { ...media.example, anchor: { ...media.example.anchor, working_copy_revision: 5, status: 'located' } })
  reject(rooted(media.schema), { ...media.example, anchor: { ...media.example.anchor, start_utf16: -1 } })
  reject(rooted(media.schema), { ...media.example, anchor: { ...media.example.anchor, quote_markdown: '' } })
})

test('PUT returns full persisted WorkingCopy rather than a bare anchor or ChapterVersion', () => {
  const media = putMedia()
  for (const example of Object.values(media.examples)) check(rooted(media.schema), example.value)
  const located = media.examples.located.value.data
  const cleared = media.examples.cleared.value.data
  assert.equal(located.working_copy_revision, requestMedia().example.expected_working_copy_revision + 1)
  assert.equal(located.review_items[0].anchor.working_copy_revision, located.working_copy_revision)
  assert.equal(located.review_items[0].anchor.chapter_version_id, located.base_chapter_version_id)
  assert.equal(cleared.working_copy_revision, located.working_copy_revision + 1)
  assert.equal(Object.hasOwn(cleared.review_items[0], 'anchor'), false)
  assert.equal(cleared.body_markdown, located.body_markdown)
  assert.equal(cleared.base_chapter_version_id, located.base_chapter_version_id)
  assert.equal(located.body_markdown.slice(located.review_items[0].anchor.start_utf16, located.review_items[0].anchor.end_utf16), located.review_items[0].anchor.quote_markdown)
  reject(rooted(media.schema), { ...media.examples.located.value, data: located.review_items[0].anchor })
  const invalid = structuredClone(media.examples.located.value); delete invalid.data.working_copy_revision
  reject(rooted(media.schema), invalid)
})

test('failure examples distinguish source denial, stale basis and lost-response replay', () => {
  for (const op of [get, put]) for (const [status, body] of Object.entries(op.responses)) {
    for (const example of Object.values(body.content?.['application/json']?.examples ?? {})) {
      check(rooted(body.content['application/json'].schema), example.value)
      if (Number(status) >= 400) assert.equal(Object.hasOwn(example.value, 'data'), false)
    }
  }
  const conflicts = put.responses['409'].content['application/json'].examples
  assert.equal(conflicts.version_conflict.value.error.retryable, false)
  assert.equal(conflicts.idempotency_conflict.value.error.retryable, false)
  assert.equal(conflicts.request_in_progress.value.error.retryable, true)
  assert.equal(get.responses['403'].content['application/json'].examples.source_access_denied.value.error.code, 'source_access_denied')
  const replay = structuredClone(putMedia().examples.located.value)
  replay.meta = { replayed: true, refresh_required: true }
  check(rooted(putMedia().schema), replay)
})

test('actual frontend GET forwards encoded identifiers and preserves a null response', async () => {
  requests.length = 0; response = getMedia().examples.none.value
  const result = await api.getCurrentConfirmation('project/中文', 'chapter/1')
  assert.equal(result, response)
  assert.equal(result.data, null)
  assert.deepEqual(requests, [{ method: 'get', args: ['/api/v1/lingdoc/projects/project%2F%E4%B8%AD%E6%96%87/chapters/chapter%2F1/confirmations/current'] }])
})

test('actual frontend PUT sends a selection or null with unchanged idempotency and receives WorkingCopy', async () => {
  const body = structuredClone(requestMedia().example)
  for (const anchor of [body.anchor, null]) {
    requests.length = 0; response = putMedia().examples[anchor ? 'located' : 'cleared'].value
    const input = { ...body, anchor }
    const result = await api.putReviewItemAnchor('p/1', 'ch/1', 'review/1', input, 'pr122-review-anchor-key')
    assert.equal(result, response)
    assert.equal(result.data.chapter_id, 'ch-question')
    assert.equal(requests[0].method, 'put')
    assert.equal(requests[0].args[0], '/api/v1/lingdoc/projects/p%2F1/chapters/ch%2F1/working-copy/review-items/review%2F1/anchor')
    assert.equal(requests[0].args[1], input)
    assert.deepEqual(requests[0].args[2], { headers: { 'Idempotency-Key': 'pr122-review-anchor-key' } })
    check(rooted(requestMedia().schema), input)
  }
})
