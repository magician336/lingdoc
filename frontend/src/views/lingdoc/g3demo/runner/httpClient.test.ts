import assert from 'node:assert/strict'
import test from 'node:test'
import {
  G3_IDEMPOTENCY_MAX_LENGTH,
  describeHttpFailure,
  envelopeData,
  errorCodeOf,
  errorMessageOf,
  isValidIdempotencyKey,
  shouldSendIdempotencyKey,
} from './httpClient'

test('GET 一律不带幂等键', () => {
  assert.equal(shouldSendIdempotencyKey('GET', '/api/v1/lingdoc/projects/p1/chapters'), false)
  assert.equal(shouldSendIdempotencyKey('GET', '/api/v1/lingdoc/projects/p1'), false)
})

test('G3 写路由都带幂等键', () => {
  assert.equal(shouldSendIdempotencyKey('POST', '/api/v1/lingdoc/projects'), true)
  assert.equal(shouldSendIdempotencyKey('PUT', '/api/v1/lingdoc/projects/p1/spec'), true)
  assert.equal(shouldSendIdempotencyKey('POST', '/api/v1/lingdoc/projects/p1/activate'), true)
  assert.equal(
    shouldSendIdempotencyKey('POST', '/api/v1/lingdoc/projects/p1/activation-read'),
    true,
  )
})

test('契约未标幂等键的交付检查不带键', () => {
  assert.equal(shouldSendIdempotencyKey('POST', '/api/v1/lingdoc/projects/p1/checks'), false)
  assert.equal(shouldSendIdempotencyKey('POST', '/api/v1/lingdoc/projects/p1/template-checks'), false)
})

test('非 G3 路由不在幂等键契约内', () => {
  assert.equal(shouldSendIdempotencyKey('POST', '/api/v1/auth/register'), false)
  assert.equal(shouldSendIdempotencyKey('POST', '/api/v1/auth/login'), false)
  assert.equal(shouldSendIdempotencyKey('GET', '/api/v1/system/info'), false)
})

test('幂等键长度契约是 8–128 字符', () => {
  assert.equal(isValidIdempotencyKey('1234567'), false)
  assert.equal(isValidIdempotencyKey('12345678'), true)
  assert.equal(isValidIdempotencyKey('x'.repeat(G3_IDEMPOTENCY_MAX_LENGTH)), true)
  assert.equal(isValidIdempotencyKey('x'.repeat(G3_IDEMPOTENCY_MAX_LENGTH + 1)), false)
  // 去空格后判定
  assert.equal(isValidIdempotencyKey('  12345678  '), true)
})

test('统一信封取 data，普通响应原样返回', () => {
  assert.deepEqual(envelopeData({ data: { id: 'p1' }, request_id: 'r' }), { id: 'p1' })
  assert.deepEqual(envelopeData({ success: true }), { success: true })
  assert.deepEqual(envelopeData([1, 2]), [1, 2])
  assert.equal(envelopeData(null), null)
})

test('错误码与错误信息都能从两种信封里读出来', () => {
  assert.equal(errorCodeOf({ error: { code: 'version_conflict' } }), 'version_conflict')
  assert.equal(errorCodeOf({ error: 'Unauthorized: missing authentication' }), 'Unauthorized: missing authentication')
  assert.equal(errorCodeOf({}), '')
  assert.equal(errorMessageOf({ error: { message: '请求字段不符合约定。' } }), '请求字段不符合约定。')
  assert.equal(errorMessageOf({ message: 'boom' }), 'boom')
  assert.equal(errorMessageOf('plain'), 'plain')
  assert.equal(errorMessageOf(null), '')
})

test('认证失败与权限不足有明确可读的说法', () => {
  assert.match(describeHttpFailure(401, {}), /认证失败/)
  assert.match(describeHttpFailure(403, {}), /权限不足/)
  assert.match(describeHttpFailure(404, {}), /不存在/)
  assert.match(describeHttpFailure(409, {}), /冲突/)
  assert.match(describeHttpFailure(500, { error: { message: 'boom' } }), /boom/)
})
