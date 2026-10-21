import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import type { AcceptCandidateInput, AdoptionConfirmation } from '@/api/lingdoc/candidateAdoption'
import { adoptionSignature, keyForAttempt, newSnapshotId, workingCopyBodyHash } from './candidateAdoption'

const workspace = readFileSync(new URL('./Workspace.vue', import.meta.url), 'utf8')
const dialog = readFileSync(new URL('../../components/LingDocCandidateAdoptionDialog.vue', import.meta.url), 'utf8')

function confirmationOf(overrides: Partial<AdoptionConfirmation> = {}): AdoptionConfirmation {
  return {
    writing_action: 'generate',
    adoption_target: 'formal_version_whole',
    confirmed_at: '2026-10-22T01:00:00.000Z',
    confirmed_by: 'user-1',
    origin: 'ai_generated',
    ...overrides,
  }
}

function inputOf(overrides: Partial<AcceptCandidateInput> = {}): AcceptCandidateInput {
  return {
    candidate_id: 'candidate-1',
    expected_chapter_version_id: null,
    expected_spec_revision: 3,
    replace_existing: false,
    expected_working_copy_revision: 7,
    expected_body_hash: 'a'.repeat(64),
    confirmation: confirmationOf(),
    ...overrides,
  }
}

/** 每次调用铸一把新键，并记下铸了几把——复用与否只看这个计数。 */
function mintCounter() {
  const state = { calls: 0 }
  return {
    state,
    mint: () => { state.calls += 1; return `key-${state.calls}` },
  }
}

test('the same request reuses its key, a different one does not', () => {
  const { state, mint } = mintCounter()
  let attempt = keyForAttempt(null, inputOf(), mint)
  assert.equal(state.calls, 1)

  // 重试：输入一字未改，必须仍用原来那把键。换键等于让服务端把一次网络超时的重试
  // 当成另一次整章替换。
  attempt = keyForAttempt(attempt, inputOf(), mint)
  assert.equal(state.calls, 1)
  assert.equal(attempt.key, 'key-1')

  // 别人刚存了一版：预期章版本变了，这是另一次请求。
  attempt = keyForAttempt(attempt, inputOf({ expected_chapter_version_id: 'version-9' }), mint)
  assert.equal(state.calls, 2)

  // 这一章从空变成有版本，替换语义随之翻转。
  attempt = keyForAttempt(attempt, inputOf({ expected_chapter_version_id: 'version-9', replace_existing: true }), mint)
  assert.equal(state.calls, 3)

  // 换了候选、换了研究条件版本，同样是另一次请求。
  attempt = keyForAttempt(attempt, inputOf({
    candidate_id: 'candidate-2', expected_chapter_version_id: 'version-9', replace_existing: true,
  }), mint)
  assert.equal(state.calls, 4)
  keyForAttempt(attempt, inputOf({
    candidate_id: 'candidate-2', expected_chapter_version_id: 'version-9', replace_existing: true,
    expected_spec_revision: 4,
  }), mint)
  assert.equal(state.calls, 5)
})

test('the signature covers every field that goes into the request body', () => {
  // 逐个字段翻一遍：漏掉任何一项，两次不同的采纳会共用一把键，第二次换来一个 409。
  const base = adoptionSignature(inputOf())
  assert.notEqual(adoptionSignature(inputOf({ candidate_id: 'candidate-2' })), base)
  assert.notEqual(adoptionSignature(inputOf({ expected_chapter_version_id: 'version-9' })), base)
  assert.notEqual(adoptionSignature(inputOf({ expected_spec_revision: 4 })), base)
  assert.notEqual(adoptionSignature(inputOf({ replace_existing: true })), base)
  // 工作副本声明：换了要动的那一份草稿，就是另一次请求。
  assert.notEqual(adoptionSignature(inputOf({ expected_working_copy_revision: 8 })), base)
  assert.notEqual(adoptionSignature(inputOf({ expected_body_hash: 'b'.repeat(64) })), base)
  // 带快照的替换：它放行了 dirty，所以也是另一次请求。
  assert.notEqual(adoptionSignature(inputOf({ snapshot_id: 'snapshot-1' })), base)
  assert.equal(adoptionSignature(inputOf()), base)
})

test('the signature covers the adoption confirmation', () => {
  // 确认载荷在请求体里，所以它必须在签名里：确认了另一个动作、另一个采纳目标或
  // 另一个范围，都是另一次采纳，必须换一把键。
  const base = adoptionSignature(inputOf())
  assert.notEqual(adoptionSignature(inputOf({ confirmation: confirmationOf({ writing_action: 'rewrite' }) })), base)
  assert.notEqual(adoptionSignature(inputOf({ confirmation: confirmationOf({ adoption_target: 'working_copy_whole' }) })), base)
  assert.notEqual(adoptionSignature(inputOf({ confirmation: confirmationOf({ confirmed_by: 'user-2' }) })), base)
  assert.notEqual(adoptionSignature(inputOf({ confirmation: confirmationOf({ origin: 'human' }) })), base)
  assert.notEqual(
    adoptionSignature(inputOf({ confirmation: confirmationOf({ adoption_target: 'working_copy_partial', scope: { start_utf16: 0, end_utf16: 4 } }) })),
    base,
  )
  assert.equal(adoptionSignature(inputOf()), base)
})

test('a retried adoption keeps its key because the confirmation moment does not move', () => {
  // 确认时刻在对话框打开时定下：它每次都变的话，同一次采纳的重试会换一把键，
  // 整章替换就做两遍。
  const { state, mint } = mintCounter()
  let attempt = keyForAttempt(null, inputOf(), mint)
  assert.equal(state.calls, 1)
  attempt = keyForAttempt(attempt, inputOf(), mint)
  assert.equal(state.calls, 1)
  assert.equal(attempt.key, 'key-1')
})

test('the dialog signs the very body it sends', () => {
  // 曾经是两处：签名按 props 现抄一份字段表，请求用另一个对象字面量。两处漂开时
  // 每一边看起来都对。这条断言盯的是那处调用点。
  assert.match(dialog, /keyForAttempt\(attempt\.value, input\.value, newIdempotencyKey\)/)
  assert.match(dialog, /acceptCandidate\(\s*props\.projectId,\s*props\.chapter\.id,\s*input\.value,/)
  assert.doesNotMatch(dialog, /idempotencyKeyForAttempt/)
})

test('the working copy digest is the sha256 of the body, in lowercase hex', async () => {
  // 算法是契约的一部分：服务端算的是同一个值（candidateadoption 的
  // workingCopyBodyHash）。两边算不出同一个值，这一声明就白声明了。
  const digest = await workingCopyBodyHash('正文')
  assert.equal(digest.length, 64)
  assert.match(digest, /^[0-9a-f]{64}$/)
  // 空正文也有确定的摘要。
  assert.equal((await workingCopyBodyHash('')).length, 64)
  // 换一个正文，摘要就换。
  assert.notEqual(await workingCopyBodyHash('正文'), await workingCopyBodyHash('另一段'))
})

test('a snapshot id is minted per replacement', () => {
  assert.notEqual(newSnapshotId(), newSnapshotId())
  assert.match(newSnapshotId(), /^candidate-adoption-snapshot-/)
})

test('a successful adoption cannot be cancelled before chapter readback succeeds', () => {
  assert.match(dialog, /function cancel\(\) \{\s*if \(!submitting\.value && !accepted\.value\) emit\('cancel'\)/)
  assert.match(dialog, /class="candidate-adoption__close" :disabled="submitting \|\| accepted"/)
  assert.match(dialog, /class="candidate-adoption__secondary" :disabled="submitting \|\| accepted"/)
})

test('the workspace actually mounts the adoption dialog', () => {
  // 缺口 ① 原来的样子就是「组件全仓零引用」：能力造好了，界面上点不到。
  // 所以这条守卫钉的是引用本身，而不是它的行为。
  assert.match(workspace, /import LingDocCandidateAdoptionDialog from '@\/components\/LingDocCandidateAdoptionDialog\.vue'/)
  assert.match(workspace, /<LingDocCandidateAdoptionDialog/)
  assert.match(workspace, /@adopted="onAdopted"/)
  assert.match(workspace, /@click="openAdoption"/)
})

test('project switching reads the first chapter draft before replacing workspace state', () => {
  // The continuous editor stages every chapter, rather than only the old first-chapter draft.
  const initialRead = workspace.indexOf('const copy = (await getWorkingCopy(id, item.id)).data')
  const projectMutation = workspace.indexOf('project.value = result.data', initialRead)
  assert.ok(initialRead >= 0)
  assert.ok(projectMutation >= 0)
  assert.ok(initialRead < projectMutation)
  assert.match(workspace, /if \(discardReviewDrafts\) \{\s*reviewDrafts\.value = \{\}\s*savedReviewDrafts\.value = \{\}/)
})

test('project selection uses named options and the shared unsaved-input policy', () => {
  assert.doesNotMatch(workspace, /selectProject\([^\n]*,\s*(?:true|false)(?:\s*,|\))/)
  assert.match(workspace, /selectProject\(project\.id, \{ force: true \}\)/)
  assert.match(workspace, /selectProject\(id, \{ force: true, skipConfirm: true \}\)/)
  assert.match(workspace, /selectProject\(id, \{ handledConditions: handledDraft \}\)/)
  assert.match(workspace, /const unsavedInputState = computed<UnsavedInputState>/)
  assert.match(workspace, /unsavedInputsAreDirty\(\{\s*\.\.\.unsavedInputState\.value,[\s\S]*?\}, \{ conditionsHandled \}\)/)
})
