import assert from 'node:assert/strict'
import test from 'node:test'
import { reviewAnchorHint, reviewAnchorState, reviewItemStatus, reviewStatusLabel, reviewProgress } from './reviewPanel'

test('missing draft or empty reason means pending regardless of disposition', () => {
  assert.equal(reviewItemStatus(undefined), 'pending')
  assert.equal(reviewItemStatus({ disposition: 'resolved', reason: '' }), 'pending')
  assert.equal(reviewItemStatus({ disposition: 'resolved', reason: '   ' }), 'pending')
  assert.equal(reviewItemStatus({ disposition: 'retained_warning', reason: '' }), 'pending')
  // 显式选择 + 空白理由：仍不算处理完
  assert.equal(reviewItemStatus({ disposition: 'resolved', reason: '  ', explicitlyChosen: true }), 'pending')
})

test('R1 regression: reason filled without an explicit choice stays pending', () => {
  // 只填理由、没点任何处理方式按钮（创建默认值 resolved 不算数）
  assert.equal(reviewItemStatus({ disposition: 'resolved', reason: '独立复验：仅输入依据' }), 'pending')
  assert.equal(reviewItemStatus({ disposition: 'resolved', reason: '独立复验：仅输入依据', explicitlyChosen: false }), 'pending')
})

test('R1 regression: explicit choice without a reason stays pending', () => {
  assert.equal(reviewItemStatus({ disposition: 'resolved', reason: '', explicitlyChosen: true }), 'pending')
  assert.equal(reviewItemStatus({ disposition: 'retained_warning', reason: '', explicitlyChosen: true }), 'pending')
})

test('explicit choice plus non-empty reason maps to distinct handled states', () => {
  assert.equal(reviewItemStatus({ disposition: 'resolved', reason: '依据来源片段核对', explicitlyChosen: true }), 'resolved')
  assert.equal(reviewItemStatus({ disposition: 'retained_warning', reason: '本次接受', explicitlyChosen: true }), 'retained')
  assert.equal(reviewStatusLabel('pending'), '未处理')
  assert.equal(reviewStatusLabel('resolved'), '已处理 · 已解决')
  assert.equal(reviewStatusLabel('retained'), '已处理 · 保留提醒')
})

test('progress counts only explicitly handled items and never crosses items by index', () => {
  const items = [{ id: 'r1' }, { id: 'r2' }, { id: 'r3' }]
  const keyOf = (x: { id: string }) => `ch1:${x.id}`
  assert.deepEqual(reviewProgress(items, keyOf, {}), { done: 0, total: 3 })
  assert.deepEqual(reviewProgress(items, keyOf, { 'ch1:r2': { disposition: 'resolved', reason: 'x', explicitlyChosen: true } }), { done: 1, total: 3 })
  // 只填理由未选择：不计入进度（R1 反例）
  assert.deepEqual(reviewProgress(items, keyOf, { 'ch1:r2': { disposition: 'resolved', reason: '仅依据' } }), { done: 0, total: 3 })
  // 键带章身份：另一章的同 id 不算数
  assert.deepEqual(reviewProgress(items, keyOf, { 'ch2:r1': { disposition: 'resolved', reason: 'x', explicitlyChosen: true } }), { done: 0, total: 3 })
})

test('stored located status without current text is not presented as a verified current location', () => {
  assert.equal(reviewAnchorState({ status: 'located' }), 'unchecked')
  assert.equal(reviewAnchorHint({ status: 'located' }), '当前稿关联待核对')
  assert.equal(reviewAnchorState({ status: 'stale' }), 'stale')
  assert.equal(reviewAnchorState(null), 'none')
  assert.equal(reviewAnchorState(undefined), 'none')
  assert.equal(reviewAnchorHint(null), '未关联片段')
})

test('current UTF-16 quote validates the displayed location and changed or shifted text stays unchecked', () => {
  const anchor = { status: 'located', start_utf16: 3, end_utf16: 5, quote_markdown: '引文' }
  assert.equal(reviewAnchorState(anchor, '前🧪引文后'), 'located')
  assert.equal(reviewAnchorHint(anchor, '前🧪引文后'), '已关联当前原文')
  assert.equal(reviewAnchorState(anchor, '前🧪改文后'), 'unchecked')
  assert.equal(reviewAnchorState(anchor, '新增前🧪引文后'), 'unchecked')
  assert.equal(reviewAnchorHint(anchor, '前🧪改文后'), '当前稿关联待核对')
  assert.equal(reviewAnchorState({ ...anchor, end_utf16: 99 }, '前🧪引文后'), 'unchecked')
})

test('a stored stale anchor does not revive just because its text appears again', () => {
  const anchor = { status: 'stale', start_utf16: 0, end_utf16: 2, quote_markdown: '引文' }
  assert.equal(reviewAnchorState(anchor, '引文'), 'stale')
  assert.equal(reviewAnchorHint(anchor, '引文'), '关联已失效')
})
