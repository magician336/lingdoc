import assert from 'node:assert/strict'
import test from 'node:test'
import { relocateAnchor, quoteOccurrences, utf16Slice } from './anchor'
import type { ReviewAnchor } from './anchor'

const base: ReviewAnchor = {
  start_utf16: 0, end_utf16: 0, quote_markdown: '',
  working_copy_revision: 1, chapter_version_id: 'v1', status: 'located',
}

test('UTF-16 offsets with Chinese + emoji: indexOf is code-unit based', () => {
  // "😀" 是 2 个 UTF-16 code unit；中文各 1 个
  const md = '前言😀后文，目标句。'
  const start = md.indexOf('目标')
  assert.equal(start, 7) // 前(0)言(1)😀(2-3)后(4)文(5)，(6)目(7)——😀 占 2 个 UTF-16 code unit
  assert.equal(utf16Slice(md, start, start + 3), '目标句')
  const anchor: ReviewAnchor = { ...base, quote_markdown: '目标句', start_utf16: start, end_utf16: start + 3 }
  const r = relocateAnchor(md, anchor)
  assert.deepEqual(r, { status: 'located', start_utf16: start, end_utf16: start + 3 })
})

test('relocation: unique exact quote moves with insert before it', () => {
  const before = '甲。乙句。'
  const after = '新段落。\n\n甲。乙句。'
  const anchor: ReviewAnchor = { ...base, quote_markdown: '乙句' }
  const r = relocateAnchor(after, anchor)
  assert.equal(r.status, 'located')
  if (r.status === 'located') assert.equal(utf16Slice(after, r.start_utf16, r.end_utf16), '乙句')
  void before
})

test('relocation: quote deleted → stale (never jumps to wrong paragraph)', () => {
  const md = '甲。丙。'
  const anchor: ReviewAnchor = { ...base, quote_markdown: '乙句' }
  assert.deepEqual(relocateAnchor(md, anchor), { status: 'stale' })
})

test('relocation: duplicated quote → stale (ambiguous, no guessing)', () => {
  const md = '乙句。中间。乙句。'
  const anchor: ReviewAnchor = { ...base, quote_markdown: '乙句' }
  assert.deepEqual(relocateAnchor(md, anchor), { status: 'stale' })
  assert.deepEqual(quoteOccurrences(md, '乙句'), [0, 6])
})

test('relocation: quote text itself altered → stale (formatting around it does not matter)', () => {
  // 引文子串未被改动（仅外层加了加粗）→ 仍唯一精确出现，符合合同"原文精确相等"
  const wrapped = '甲。**乙句**。'
  const anchor: ReviewAnchor = { ...base, quote_markdown: '乙句' }
  assert.deepEqual(relocateAnchor(wrapped, anchor), { status: 'located', start_utf16: 4, end_utf16: 6 })
  // 引文自身被改写 → stale
  assert.deepEqual(relocateAnchor(wrapped, { ...base, quote_markdown: '乙句改' }), { status: 'stale' })
})

test('emoji-heavy boundary exactness', () => {
  const md = '😀😃😄 目标😎句子'
  const anchor: ReviewAnchor = { ...base, quote_markdown: '目标😎句子' }
  const r = relocateAnchor(md, anchor)
  assert.equal(r.status, 'located')
  if (r.status === 'located') assert.equal(utf16Slice(md, r.start_utf16, r.end_utf16), '目标😎句子')
})
