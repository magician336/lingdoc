import assert from 'node:assert/strict'
import test from 'node:test'
import { canLeaveWorkspace, conditionsChangeInputsAreDirty, conditionsChangeSnapshot, confirmationBlockReasons, hasUnsavedInputs, installUnloadGuard, reviewDraftsAreDirty, unsavedInputsAreDirty } from './uiSafety'

const clean = { chapterChanged: false, specChanged: false, templateChanged: false, saving: false, reviewChanged: false, conditionsChanged: false, editorUnsaved: false }
function unloadEvent() {
  const event = new Event('beforeunload', { cancelable: true })
  // Node's Event is not a browser BeforeUnloadEvent; provide its writable field.
  Object.defineProperty(event, 'returnValue', { value: 'unset', writable: true })
  return event
}
test('saved server drafts do not warn; each unsaved input and an in-flight save does', () => {
  assert.equal(hasUnsavedInputs(clean), false)
  for (const field of Object.keys(clean)) assert.equal(hasUnsavedInputs({ ...clean, [field]: true }), true, field)
  assert.equal(unsavedInputsAreDirty(clean), false)
  for (const field of Object.keys(clean)) assert.equal(unsavedInputsAreDirty({ ...clean, [field]: true }), true, field)
  assert.equal(unsavedInputsAreDirty({ ...clean, conditionsChanged: true }, { conditionsHandled: true }), false)
  assert.equal(unsavedInputsAreDirty({ ...clean, conditionsChanged: true, specChanged: true }, { conditionsHandled: true }), false)
  assert.equal(unsavedInputsAreDirty({ ...clean, conditionsChanged: true, chapterChanged: true }, { conditionsHandled: true }), true)
  assert.equal(unsavedInputsAreDirty({ ...clean, conditionsChanged: true }), true)
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

test('conditions auxiliary form starts clean and protects either reason or chapter selection after spec is restored', () => {
  const baseline = conditionsChangeSnapshot('p1', '', [])
  assert.equal(conditionsChangeInputsAreDirty(conditionsChangeSnapshot('p1', '', []), baseline), false)
  for (const draft of [conditionsChangeSnapshot('p1', '变更理由', []), conditionsChangeSnapshot('p1', '', ['c1'])]) {
    const conditionsChanged = conditionsChangeInputsAreDirty(draft, baseline)
    assert.equal(conditionsChanged, true)
    assert.equal(unsavedInputsAreDirty({ ...clean, conditionsChanged }), true)
    assert.equal(canLeaveWorkspace(conditionsChanged, () => false), false)
  }
})

test('conditions acknowledgement compares submitted meaning and snapshots do not share checkbox arrays', () => {
  const chosen = ['c2', 'c1']
  const acknowledged = conditionsChangeSnapshot('p1', ' 理由 ', chosen)
  assert.deepEqual(chosen, ['c2', 'c1'])
  assert.equal(conditionsChangeInputsAreDirty(conditionsChangeSnapshot('p1', '理由', ['c1', 'c2']), acknowledged), false)
  chosen.push('c3')
  assert.deepEqual(acknowledged.chapterIds, ['c1', 'c2'])
  assert.equal(conditionsChangeInputsAreDirty(conditionsChangeSnapshot('p1', '新理由', ['c1', 'c2']), acknowledged), true)
  assert.equal(conditionsChangeInputsAreDirty(conditionsChangeSnapshot('p1', '理由', chosen), acknowledged), true)
  assert.equal(conditionsChangeInputsAreDirty(conditionsChangeSnapshot('p2', '理由', ['c1', 'c2']), acknowledged), true)
})

test('acknowledged preview clears auxiliary dirty while changed spec and other drafts remain protected', () => {
  const submitted = conditionsChangeSnapshot('p1', '已提交理由', ['c1'])
  const conditionsChanged = conditionsChangeInputsAreDirty(conditionsChangeSnapshot('p1', '已提交理由', ['c1']), submitted)
  assert.equal(conditionsChanged, false)
  assert.equal(unsavedInputsAreDirty({ ...clean, conditionsChanged, specChanged: true }), true)
  assert.equal(unsavedInputsAreDirty({ ...clean, conditionsChanged, chapterChanged: true }), true)
  assert.equal(unsavedInputsAreDirty({ ...clean, conditionsChanged, reviewChanged: true }), true)
  const reset = conditionsChangeSnapshot('p1', '', [])
  assert.equal(conditionsChangeInputsAreDirty(conditionsChangeSnapshot('p1', '', []), reset), false)
  assert.equal(unsavedInputsAreDirty(clean), false)
})

test('refresh guard protects hidden unsaved conditions inputs and releases their acknowledged baseline', () => {
  const target = new EventTarget()
  const draft = conditionsChangeSnapshot('p1', '暂存理由', ['c1'])
  let baseline = conditionsChangeSnapshot('p1', '', [])
  const remove = installUnloadGuard(target as unknown as Pick<Window, 'addEventListener' | 'removeEventListener'>,
    () => unsavedInputsAreDirty({ ...clean, conditionsChanged: conditionsChangeInputsAreDirty(draft, baseline) }))
  const hidden = unloadEvent()
  target.dispatchEvent(hidden)
  assert.equal(hidden.defaultPrevented, true)
  baseline = conditionsChangeSnapshot('p1', '暂存理由', ['c1'])
  const acknowledged = unloadEvent()
  target.dispatchEvent(acknowledged)
  assert.equal(acknowledged.defaultPrevented, false)
  remove()
})

test('handled conditions never release cross-chapter editor drafts or a pending save', () => {
  assert.equal(hasUnsavedInputs({ ...clean, editorUnsaved: true }), true)
  assert.equal(hasUnsavedInputs({ ...clean, saving: true }), true)
  for (const field of ['editorUnsaved', 'saving', 'reviewChanged'] as const) {
    assert.equal(unsavedInputsAreDirty({ ...clean, conditionsChanged: true, [field]: true }, { conditionsHandled: true }), true, field)
  }
})

const confirmable = { busy: false, saving: false, unsaved: false, needsCommit: false, missingReasons: 0 }
test('confirmation explains every blocker; completed confirmation has no stale disabled reason', () => {
  assert.deepEqual(confirmationBlockReasons(confirmable), [])
  assert.deepEqual(confirmationBlockReasons({ ...confirmable, missingReasons: 2 }), ['还有 2 条待核事项未完成处理（需选择处理方式并填写理由）'])
  assert.deepEqual(confirmationBlockReasons({ ...confirmable, busy: true, saving: true }), ['工作副本正在保存，请等待保存完成'])
  assert.deepEqual(confirmationBlockReasons({ ...confirmable, busy: true }), ['当前操作尚未完成，请稍候'])
  assert.deepEqual(confirmationBlockReasons({ ...confirmable, unsaved: true, needsCommit: true, missingReasons: 1 }), [
    '请先保存当前修改', '请先将工作副本提交为正式版本', '还有 1 条待核事项未完成处理（需选择处理方式并填写理由）',
  ])
})
