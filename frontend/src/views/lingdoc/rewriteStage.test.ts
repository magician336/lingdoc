import assert from 'node:assert/strict'
import test from 'node:test'
import {
  rewriteStage,
  rewriteStageAdoptable,
  rewriteStageLabel,
  rewriteStageNotice,
} from './rewriteStage'

// 这组断言守的是「失效判据只有一个拥有者，『正在应用』是一个事实」。
//
// 服务端曾经用一个 `status` 字符串同时装「走到了哪一步」与「还能不能被采纳」，
// 页面因此读一个字段、猜两件事。现在这个字段没了：失效判据归 `validity`，
// 「正在应用」归 `apply_pending`，页面把这两个事实折成一个阶段。

test('阶段由 validity 与 apply_pending 两个事实派生，不是一个字符串状态机', () => {
  // 可采纳：判据是 fresh，且此刻没有正在进行的采纳。
  assert.equal(rewriteStage({ validity: 'fresh' }), 'ready')
  // 输入变了。
  assert.equal(rewriteStage({ validity: 'stale' }), 'stale')
  // 授权没了。
  assert.equal(rewriteStage({ validity: 'not_authorized' }), 'not_authorized')
  // 没有交出可采纳的产出，且没有失败原因 ⇒ 还在跑。
  assert.equal(rewriteStage({ validity: '' }), 'pending')
  // 没有交出可采纳的产出，且有失败原因 ⇒ 失败。
  assert.equal(rewriteStage({ validity: '', error_code: 'model_unavailable' }), 'failed')
  // 正在写：这件事优先于失效判据——一次正在进行的采纳此刻就在写。
  assert.equal(rewriteStage({ validity: 'fresh', apply_pending: true }), 'applying')
})

test('撤权与输入变了不是同一个阶段：它们的下一步不同', () => {
  assert.notEqual(rewriteStage({ validity: 'stale' }), rewriteStage({ validity: 'not_authorized' }))
  // 输入变了 ⇒ 重新运行那个动作。
  assert.match(rewriteStageNotice(rewriteStage({ validity: 'stale' })), /重新运行该动作/)
  // 授权没了 ⇒ 先恢复授权，重新运行也没用。
  const revoked = rewriteStageNotice(rewriteStage({ validity: 'not_authorized' }))
  assert.match(revoked, /恢复授权/)
  assert.notEqual(revoked, rewriteStageNotice(rewriteStage({ validity: 'stale' })))
})

test('只有可采纳与正在应用两个阶段能走采纳', () => {
  assert.equal(rewriteStageAdoptable('ready'), true)
  assert.equal(rewriteStageAdoptable('applying'), true)
  assert.equal(rewriteStageAdoptable('stale'), false)
  assert.equal(rewriteStageAdoptable('not_authorized'), false)
  assert.equal(rewriteStageAdoptable('failed'), false)
  assert.equal(rewriteStageAdoptable('pending'), false)
})

test('阶段名与下一步各自都有说法', () => {
  for (const stage of ['ready', 'stale', 'not_authorized', 'failed', 'applying', 'pending'] as const) {
    assert.ok(rewriteStageLabel(stage).length > 0)
    assert.equal(typeof rewriteStageNotice(stage), 'string')
  }
  assert.equal(rewriteStageLabel('not_authorized'), '授权已失效')
})

test('还没有候选时，阶段是进行中而不是空', () => {
  assert.equal(rewriteStage(null), 'pending')
  assert.equal(rewriteStage(undefined), 'pending')
})
