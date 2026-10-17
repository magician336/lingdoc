import { test } from 'node:test'
import assert from 'node:assert/strict'
import { inspectorOnNarrowTransition, mergeWbLayoutPref, readWbLayoutPref, wbLayoutStorageKey } from './layoutPref'

test('wbLayoutStorageKey 按用户与租户隔离，匿名有兜底', () => {
  assert.equal(wbLayoutStorageKey('{"id":"u1","tenant_id":"t1"}'), 'lingdoc:wb-layout:u1:t1')
  assert.equal(wbLayoutStorageKey('{"user_id":"u2"}'), 'lingdoc:wb-layout:u2:')
  assert.equal(wbLayoutStorageKey(null), 'lingdoc:wb-layout:anonymous')
  assert.equal(wbLayoutStorageKey('not-json'), 'lingdoc:wb-layout:anonymous')
})

test('readWbLayoutPref 只接受布尔字段，坏数据退回空偏好', () => {
  assert.deepEqual(readWbLayoutPref('{"rail":true,"insp":false}'), { rail: true, insp: false })
  assert.deepEqual(readWbLayoutPref('{"rail":"yes","insp":1}'), {})
  assert.deepEqual(readWbLayoutPref('not-json'), {})
  assert.deepEqual(readWbLayoutPref(null), {})
  assert.deepEqual(readWbLayoutPref('[1,2]'), {})
})

test('mergeWbLayoutPref 局部更新不丢既有键', () => {
  assert.deepEqual(mergeWbLayoutPref({ rail: true }, { insp: false }), { rail: true, insp: false })
  assert.deepEqual(mergeWbLayoutPref({ rail: true, insp: true }, { rail: false }), { rail: false, insp: true })
})

test('浮层宽屏进入窄屏自动收起，返回宽屏恢复显式打开且不修改偏好', () => {
  const pref = { insp: true }
  assert.equal(inspectorOnNarrowTransition(true, false, true, pref, false), false)
  assert.equal(inspectorOnNarrowTransition(false, true, false, pref, false), true)
  assert.deepEqual(pref, { insp: true })
})

test('显式关闭在返回宽屏后仍关闭，未设置偏好时使用当前宽屏默认', () => {
  assert.equal(inspectorOnNarrowTransition(false, true, false, { insp: false }, true), false)
  assert.equal(inspectorOnNarrowTransition(false, true, false, {}, false), false)
  assert.equal(inspectorOnNarrowTransition(false, true, false, {}, true), true)
})

test('没有跨窄屏边界时保留当前状态，允许用户在窄屏手动打开', () => {
  assert.equal(inspectorOnNarrowTransition(true, true, true, { insp: false }, false), true)
  assert.equal(inspectorOnNarrowTransition(false, false, false, { insp: true }, true), false)
})
