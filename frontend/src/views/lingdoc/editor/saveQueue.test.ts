import assert from 'node:assert/strict'
import test from 'node:test'
import { ChapterSaveQueue, blockReasonFor } from './saveQueue'
import { validateCandidate, confirmationInvalidReason } from './guard'
import type { Candidate } from './guard'
import { setTimeout as delay } from 'node:timers/promises'

function makeQueue(save: (ctx: { chapterId: string; expectedWorkingCopyRevision: number }, body: string) => Promise<{ ok: boolean; conflict?: boolean; newWorkingCopyRevision?: number }>) {
  return new ChapterSaveQueue(save, 40)
}

test('900ms queue saves dirty chapter with CAS and shows clean only after success', async () => {
  const seen: Array<{ chapterId: string; expected: number; body: string }> = []
  const q = makeQueue(async (ctx, body) => { seen.push({ chapterId: ctx.chapterId, expected: ctx.expectedWorkingCopyRevision, body }); return { ok: true, newWorkingCopyRevision: ctx.expectedWorkingCopyRevision + 1 } })
  q.setBaseline('c1', 3)
  q.markDirty('c1', '新正文')
  assert.equal(q.state('c1'), 'dirty')
  assert.equal(q.overallState(['c1']), 'dirty')
  await delay(80)
  assert.equal(q.state('c1'), 'clean')
  assert.equal(q.overallState(['c1']), 'clean')
  assert.deepEqual(seen, [{ chapterId: 'c1', expected: 3, body: '新正文' }])
  // 下一次 CAS 用新 revision
  q.markDirty('c1', '再改')
  await delay(80)
  assert.equal(seen[1].expected, 4)
  q.dispose()
})

test('conflict keeps input and marks conflict (409 输入保留)', async () => {
  const q = makeQueue(async () => ({ ok: false, conflict: true }))
  q.setBaseline('c1', 1)
  q.markDirty('c1', '我的输入')
  const r = await q.flushAll()
  assert.equal(r.ok, false)
  assert.equal(q.state('c1'), 'conflict')
  assert.equal(blockReasonFor(q, 'c1', false), '保存发生版本冲突（内容已被他人修改），当前输入已保留；请重新读取后处理。')
  // 输入仍保留：再次 flush 仍发送同一正文
  let sent = ''
  const q2 = new ChapterSaveQueue(async (_c, b) => { sent = b; return { ok: true } }, 40)
  q2.setBaseline('c1', 1)
  q2.markDirty('c1', '我的输入')
  await q2.flushAll()
  assert.equal(sent, '我的输入')
  q.dispose(); q2.dispose()
})

test('flushAll only reports saved when every chapter succeeded', async () => {
  const q = makeQueue(async (ctx) => ({ ok: ctx.chapterId !== 'c2' }))
  q.setBaseline('c1', 1); q.setBaseline('c2', 1)
  q.markDirty('c1', 'A'); q.markDirty('c2', 'B')
  const r = await q.flushAll()
  assert.equal(r.ok, false)
  assert.equal(q.state('c1'), 'clean')
  assert.equal(q.state('c2'), 'error')
  assert.equal(q.overallState(['c1', 'c2']), 'error') // 不误报整份已保存
  q.dispose()
})

test('confirm/export blocked while dirty even before 900ms autosave (反例2)', async () => {
  const q = makeQueue(async () => ({ ok: true }))
  q.setBaseline('c1', 1)
  q.markDirty('c1', '刚输入')
  assert.equal(blockReasonFor(q, 'c1', true), '当前稿有未保存修改，请先保存（Ctrl+S）后再确认/导出。')
  q.dispose()
})

test('candidate: chapter switch kills candidate, refresh cannot resurrect (反例4)', () => {
  const cand: Candidate = { ctx: { projectId: 'p1', chapterId: 'c1', bodyVersion: 2 }, quote: '甲章选句', createdAt: 1 }
  assert.equal(validateCandidate(cand, { projectId: 'p1', chapterId: 'c1', bodyVersion: 2 }).ok, true)
  const afterSwitch = validateCandidate(cand, { projectId: 'p1', chapterId: 'c2', bodyVersion: 2 })
  assert.deepEqual(afterSwitch, { ok: false, reason: 'chapter-mismatch' })
  // 版本变化同样作废（含"刷新"场景：没有任何刷新路径能复活旧候选）
  assert.deepEqual(validateCandidate(cand, { projectId: 'p1', chapterId: 'c1', bodyVersion: 3 }), { ok: false, reason: 'stale-version' })
  assert.deepEqual(validateCandidate(cand, { projectId: 'p2', chapterId: 'c1', bodyVersion: 2 }), { ok: false, reason: 'project-mismatch' })
  assert.deepEqual(validateCandidate(null, { projectId: 'p1', chapterId: 'c1', bodyVersion: 2 }), { ok: false, reason: 'empty-selection' })
})

test('confirmation invalidated honestly by local edits (反例2)', () => {
  const snap = { chapterVersionId: 'v1', confirmedAtRevision: 1 }
  assert.equal(confirmationInvalidReason(snap, { committedChapterVersionId: 'v1', bodyVersion: 1, hasUnsavedChanges: false }), null)
  assert.match(confirmationInvalidReason(snap, { committedChapterVersionId: 'v1', bodyVersion: 1, hasUnsavedChanges: true }) ?? '', /未保存修改/)
  assert.match(confirmationInvalidReason(snap, { committedChapterVersionId: 'v2', bodyVersion: 2, hasUnsavedChanges: false }) ?? '', /已确认版本失效/)
  assert.equal(confirmationInvalidReason(null, { committedChapterVersionId: 'v1', bodyVersion: 1, hasUnsavedChanges: true }), null)
})

test('late response of A never clobbers newer input B (写入链核销)', async () => {
  let resolveA: ((r: { ok: boolean; newWorkingCopyRevision?: number }) => void) | null = null
  const sent: string[] = []
  let call = 0
  const q = new ChapterSaveQueue(async (_ctx, body) => {
    call++
    sent.push(body)
    if (call === 1) return new Promise(res => { resolveA = res })
    return { ok: true, newWorkingCopyRevision: 10 + call }
  }, 20)
  q.setBaseline('c1', 1)
  q.markDirty('c1', '稿A')
  const flushing = q.flushAll()
  await delay(30)
  q.markDirty('c1', '稿B')
  await delay(60)
  assert.equal(q.state('c1'), 'saving')
  resolveA!({ ok: true, newWorkingCopyRevision: 2 })
  await delay(80)
  const r = await flushing
  assert.deepEqual(sent, ['稿A', '稿B'])
  assert.equal(sent[sent.length - 1], '稿B')
  assert.equal(r.ok, true)
  q.dispose()
})

test('draftBlockReason: saved-but-uncommitted blocks confirm/export (反例2 基线)', async () => {
  const { draftBlockReason } = await import('./guard')
  assert.equal(draftBlockReason({ dirty: false, conflict: false, savedRevision: 5, committedRevision: 5 }), null)
  assert.match(draftBlockReason({ dirty: false, conflict: false, savedRevision: 6, committedRevision: 5 }) ?? '', /未提交正式版本/)
  assert.match(draftBlockReason({ dirty: true, conflict: false, savedRevision: 6, committedRevision: 5 }) ?? '', /未保存/)
  assert.match(draftBlockReason({ dirty: false, conflict: true, savedRevision: 6, committedRevision: 5 }) ?? '', /冲突/)
})

test('R4 握手：晚到的旧基线不得把 CAS 基线降级（防 expected=0/过期 revision 400）', async () => {
  const sent: number[] = []
  const q = new ChapterSaveQueue(async (ctx) => { sent.push(ctx.expectedWorkingCopyRevision); return { ok: true, newWorkingCopyRevision: ctx.expectedWorkingCopyRevision + 1 } }, 20)
  q.setBaseline('c2', 0)          // 初始未知
  q.setBaseline('c2', 4)          // 全章握手完成：真实基线到达
  q.setBaseline('c2', 0)          // 父级旧快照晚到 → 不得降级
  q.markDirty('c2', '正文')
  await q.flushAll()
  assert.deepEqual(sent, [4])     // 必须是真实基线 4，不是 0
  q.dispose()
})
