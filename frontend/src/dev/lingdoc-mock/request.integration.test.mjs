// Execute the actual request.ts source with REAL Axios. Only adjacent UI/auth
// dependencies are isolated; no interceptor or envelope implementation is copied.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { test } from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import { createContractMockAdapter } from './adapter.ts'

const require = createRequire(import.meta.url)
function requestModule() {
  const source = readFileSync(new URL('../../utils/request.ts', import.meta.url), 'utf8')
  const compiled = ts.transpileModule(source, { compilerOptions: {
    module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true,
  } }).outputText
  let refreshCalls = 0
  const imports = {
    axios: require('axios'),
    './index': { generateRandomString: () => 't06-integration', MAX_FILE_SIZE_MB: 50, MAX_SKILL_BUNDLE_SIZE_MB: 50 },
    '@/i18n': { global: { t: key => key, locale: { value: 'zh-CN' } } },
    './api-base': { getApiBaseUrl: () => 'https://must-not-be-called.invalid' },
    './uploadLimit': { isSkillBundleUploadUrl: () => false },
    './authRefresh': { isEmbedPage: () => false, forceReloginRedirect: () => { throw new Error('unexpected redirect') },
      refreshAccessTokenShared: () => { refreshCalls++; throw new Error('unexpected refresh') } },
  }
  const module = { exports: {} }
  const context = vm.createContext({ module, exports: module.exports, console, Blob,
    localStorage: { getItem: key => key === 'weknora_token' ? 'private-sentinel-token' :
      key === 'weknora_selected_tenant_id' ? '7' : null },
    require: name => { if (!(name in imports)) throw new Error('Unisolated import: ' + name); return imports[name] },
  })
  new vm.Script(compiled, { filename: 'actual-request.ts' }).runInContext(context)
  return { get: module.exports.get, refreshCalls: () => refreshCalls }
}

test('real request.ts + Axios preserve mock success and structured 422', async () => {
  const request = requestModule()
  const fetcher = async (_url, init) => {
    const headers = new Headers(init.headers)
    assert.equal(headers.get('authorization'), null)
    assert.equal(headers.get('x-tenant-id'), null)
    const status = Number(headers.get('x-lingdoc-mock-status'))
    const body = status === 200 ? { data: { fixture: true }, request_id: 't06', meta: { replayed: false, refresh_required: false } } :
      { error: { code: 'invalid_state', message: 'mock failure', retryable: false }, request_id: 't06' }
    return new Response(JSON.stringify(body), { status,
      headers: { 'X-LingDoc-Mode': 'mock', 'X-LingDoc-Contract-Checked': 'true' } })
  }
  const config = (status, example) => ({ adapter: createContractMockAdapter({ enabled: true, status, example, fetcher }) })
  const result = await request.get('/api/v1/lingdoc/templates/template-demo', config(200, 'success'))
  assert.equal(result.data.fixture, true)
  assert.equal(result.$httpStatus, 200)
  assert.equal(Object.keys(result).includes('$httpStatus'), false)
  await assert.rejects(request.get('/api/v1/lingdoc/templates/template-demo', config(422, 'invalid_state')), error => {
    assert.equal(error.$httpStatus, 422)
    assert.equal(error.error.code, 'invalid_state')
    assert.equal(error.message, 'mock failure')
    assert.equal(error.request_id, 't06')
    return true
  })
  assert.equal(request.refreshCalls(), 0)
})

test('shared OpenAPI -> real local HTTP -> Axios -> actual request layer', {
  skip: !process.env.LINGDOC_MOCK_ORIGIN && 'Run dedicated T06 workflow or start the fixture server locally',
}, async () => {
  const request = requestModule()
  const options = { enabled: true, origin: process.env.LINGDOC_MOCK_ORIGIN }
  const result = await request.get('/api/v1/lingdoc/templates/template-demo', {
    adapter: createContractMockAdapter({ ...options, status: 200, example: 'success' }),
  })
  assert.equal(result.$httpStatus, 200)
  assert.equal(result.data.id, 'template-demo')
  assert.equal(result.data.is_demo, true)
  assert.equal(result.data.sections.length, 2)
  await assert.rejects(request.get('/api/v1/lingdoc/templates/template-demo', {
    adapter: createContractMockAdapter({ ...options, status: 422, example: 'invalid_state' }),
  }), error => error.$httpStatus === 422 && error.error.code === 'invalid_state' && typeof error.request_id === 'string')
  assert.equal(request.refreshCalls(), 0)
})
