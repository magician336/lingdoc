import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const ts = require('typescript')
const source = fs.readFileSync(new URL('./workspace.ts', import.meta.url), 'utf8')

function harness() {
  const calls = [], exports = {}
  const transport = Object.fromEntries(['get', 'post', 'put'].map(method => [method, async (url, input) => {
    calls.push({ method, url, input: input && JSON.parse(JSON.stringify(input)) })
    return { data: [], meta: { replayed: false, refresh_required: false } }
  }]))
  const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  vm.runInNewContext(compiled, { exports, require(name) { assert.equal(name, '@/utils/request'); return transport } })
  return { api: exports, calls }
}

test('chapter retrieval preserves the selected chapter as server-verified request context', async () => {
  const { api, calls } = harness()
  await api.retrieveSources('project/isolated', 'query', ['asset-one'], 'chapter-one')
  assert.equal(calls[0].url, '/api/v1/lingdoc/projects/project%2Fisolated/retrieval')
  assert.deepEqual(calls[0].input, { query: 'query', asset_ids: ['asset-one'], chapter_id: 'chapter-one' })
})

test('project-wide retrieval does not invent chapter authority', async () => {
  const { api, calls } = harness()
  await api.retrieveSources('project', 'query', ['asset-one'])
  assert.deepEqual(calls[0].input, { query: 'query', asset_ids: ['asset-one'] })
})

test('the real Workspace consumer and OpenAPI retain optional chapter retrieval context', () => {
  const workspace = fs.readFileSync(new URL('../../views/lingdoc/Workspace.vue', import.meta.url), 'utf8')
  const search = workspace.match(/async function searchSources\(\) \{[\s\S]*?\n\}/)?.[0] ?? ''
  assert.match(search, /retrieveSources\(project\.value\.id, query, assets\.value\.map\(item => item\.id\), chapter\.value\?\.id\)/)
  const contract = JSON.parse(fs.readFileSync(new URL('../../../../docs/08-本轮实施方案/contracts/openapi.json', import.meta.url), 'utf8'))
  const schema = contract.components.schemas.RetrieveSources
  assert.equal(schema.properties.chapter_id.type, 'string')
  assert.equal(schema.properties.chapter_id.minLength, 1)
  assert.equal(schema.required.includes('chapter_id'), false)
  assert.equal(schema.additionalProperties, false)
})
