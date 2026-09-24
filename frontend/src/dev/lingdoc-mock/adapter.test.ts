import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { InternalAxiosRequestConfig } from 'axios'
import { createContractMockAdapter } from './adapter.ts'

const config = (values = {}) => ({ url: '/api/v1/lingdoc/templates/template-demo', method: 'get',
  headers: {}, ...values }) as InternalAxiosRequestConfig
const payload = (status = 200, checked = 'true', mode = 'mock') => new Response('{"data":{"name":"demo"}}', {
  status, headers: { 'X-LingDoc-Mode': mode, 'X-LingDoc-Contract-Checked': checked },
})

test('dev transport strips credentials and ignores production baseURL', async () => {
  let calls = 0
  const adapter = createContractMockAdapter({ enabled: true, status: 200, example: 'success',
    fetcher: async (url, init) => {
      calls++
      assert.equal(url, 'http://127.0.0.1:4010/api/v1/lingdoc/templates/template-demo')
      assert.equal(init?.credentials, 'omit')
      assert.equal(init?.redirect, 'error')
      const headers = new Headers(init?.headers)
      assert.equal(headers.get('authorization'), null)
      assert.equal(headers.get('cookie'), null)
      assert.equal(headers.get('x-tenant-id'), null)
      assert.equal(headers.get('x-lingdoc-mock-example'), 'success')
      assert.equal(headers.get('x-request-id'), 'test')
      return payload()
    },
  })
  const result = await adapter(config({ baseURL: 'https://not-called.invalid', headers: {
    Authorization: 'Bearer do-not-forward', Cookie: 'private', 'X-Tenant-ID': '1', 'X-Request-ID': 'test',
  } }))
  assert.equal(result.status, 200)
  assert.equal(calls, 1)
})

test('HTTP errors retain response/config for existing request interceptors', async () => {
  const adapter = createContractMockAdapter({ enabled: true, status: 422, example: 'invalid_state',
    fetcher: async () => payload(422) })
  await assert.rejects(adapter(config()), (error: any) => error.response.status === 422 && !!error.config)
})

test('auth and untrusted endpoint responses never trigger token-refresh paths', async () => {
  for (const response of [payload(401), payload(200, 'true', 'real')]) {
    const adapter = createContractMockAdapter({ enabled: true, status: 200, example: 'success', fetcher: async () => response })
    await assert.rejects(adapter(config()), (error: any) => error.response === undefined)
  }
})

test('invalid selections/remote origins are rejected before any network', async () => {
  for (const origin of ['https://example.com', 'http://localhost:4010', 'http://127.0.0.1:4010/private']) {
    assert.throws(() => createContractMockAdapter({ enabled: true, status: 200, example: 'success', origin }))
  }
  assert.throws(() => createContractMockAdapter({ enabled: false, status: 200, example: 'success' }))
  const adapter = createContractMockAdapter({ enabled: true, status: 200, example: 'success',
    fetcher: async () => { throw new Error('unexpected network') } })
  for (const url of ['https://example.com', '/api/v1/auth/refresh', '/api/v1/lingdoc/../auth', '/api/v1/lingdoc/%2fsecret']) {
    await assert.rejects(adapter(config({ url })), /Only LingDoc/)
  }
})

test('connection failure is terminal and does not retry against real backend', async () => {
  let calls = 0
  const adapter = createContractMockAdapter({ enabled: true, status: 200, example: 'success',
    fetcher: async () => { calls++; throw new Error('offline') } })
  await assert.rejects(adapter(config()), /offline/)
  assert.equal(calls, 1)
})
