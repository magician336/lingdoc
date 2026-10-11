import assert from 'node:assert/strict'
import test from 'node:test'
import { canLeaveWorkspace, confirmationBlockReasons, hasUnsavedInputs, installUnloadGuard, reviewDraftsAreDirty } from './uiSafety'

const clean = { chapterChanged: false, specChanged: false, templateChanged: false, saving: false, reviewChanged: false }
function unloadEvent() {
  const event = new Event('beforeunload', { cancelable: true })
  // Node's Event is not a browser BeforeUnloadEvent; provide its writable field.
  Object.defineProperty(event, 'returnValue', { value: 'unset', writable: true })
  return event
}
test('saved server drafts do not warn; each unsaved input and an in-flight save does', () => {
  assert.equal(hasUnsavedInputs(clean), false)
  for (const field of Object.keys(clean)) assert.equal(hasUnsavedInputs({ ...clean, [field]: true }), true, field)
})

test('refresh guard reacts to save completion and unregisters on leaving the workspace', () => {
  const target = new EventTarget()
  let dirty = true
  const remove = installUnloadGuard(target as unknown as Pick<Window, 'addEventListener' | 'removeEventListener'>, () => dirty)
  const first = unloadEvent()
  target.dispatchEvent(first)
  assert.equal(first.defaultPrevented, true)
  dirty = false
  const saved = unloadEvent()
  target.dispatchEvent(saved)
  assert.equal(saved.defaultPrevented, false)
  dirty = true
  remove()
  const left = unloadEvent()
  target.dispatchEvent(left)
  assert.equal(left.defaultPrevented, false)
})

test('route leave preserves input on cancel, allows explicit discard and stays quiet when clean', () => {
  let calls = 0
  assert.equal(canLeaveWorkspace(false, () => { calls++; return false }), true)
  assert.equal(calls, 0)
  assert.equal(canLeaveWorkspace(true, () => { calls++; return false }), false)
  assert.equal(canLeaveWorkspace(true, () => true), true)
  assert.equal(calls, 1)
})

test('empty default review forms are clean; changed reasons and decisions remain protected until acknowledged', () => {
  const draft = { disposition: 'resolved' as const, reason: '' }
  assert.equal(reviewDraftsAreDirty({ c1: draft }, {}), false)
  assert.equal(reviewDraftsAreDirty({ c1: { ...draft, reason: '合成核查依据' } }, {}), true)
  assert.equal(reviewDraftsAreDirty({ c1: { ...draft, disposition: 'retained_warning' } }, {}), true)
  const acknowledged = { c1: { ...draft, reason: '合成核查依据' } }
  assert.equal(reviewDraftsAreDirty(acknowledged, structuredClone(acknowledged)), false)
  assert.equal(reviewDraftsAreDirty({ ...acknowledged, c2: { ...draft, reason: '另一章未确认' } }, acknowledged), true)
})

const confirmable = { busy: false, saving: false, unsaved: false, needsCommit: false, missingReasons: 0 }
test('confirmation explains every blocker; completed confirmation has no stale disabled reason', () => {
  assert.deepEqual(confirmationBlockReasons(confirmable), [])
  assert.deepEqual(confirmationBlockReasons({ ...confirmable, missingReasons: 2 }), ['还有 2 条待核事项未填写处理理由'])
  assert.deepEqual(confirmationBlockReasons({ ...confirmable, busy: true, saving: true }), ['工作副本正在保存，请等待保存完成'])
  assert.deepEqual(confirmationBlockReasons({ ...confirmable, busy: true }), ['当前操作尚未完成，请稍候'])
  assert.deepEqual(confirmationBlockReasons({ ...confirmable, unsaved: true, needsCommit: true, missingReasons: 1 }), [
    '请先保存当前修改', '请先将工作副本提交为正式版本', '还有 1 条待核事项未填写处理理由',
  ])
})
