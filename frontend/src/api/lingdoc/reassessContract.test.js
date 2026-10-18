import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'

// Consume the published contract, rather than re-describing the server in a stub.
const spec = JSON.parse(fs.readFileSync(new URL('../../../../docs/08-本轮实施方案/contracts/openapi.json', import.meta.url), 'utf8'))
const operation = spec.paths['/projects/{projectId}/change-sets/{changeSetId}/reassess'].post

test('published reassessment accepts no request body and never instructs clients to send an empty object', () => {
  assert.equal(operation.operationId, 'reassessChangeSet')
  assert.equal(operation.requestBody, undefined, 'A generated client must not be required to send {} or null')
  assert.match(operation.description ?? '', /0.*字节|零字节/)
  assert.match(operation.responses['400'].description, /请求体/)
})

test('bodyless reassessment still requires the original idempotency key and preserves success/conflict responses', () => {
  const key = operation.parameters.find(parameter => parameter.in === 'header' && parameter.name === 'Idempotency-Key')
  assert.equal(key.required, true)
  assert.ok(key.schema.minLength >= 8)
  assert.equal(operation.responses['201'].content['application/json'].schema.$ref, '#/components/schemas/ChangeSetResponse')
  assert.ok(operation.responses['409'])
})
