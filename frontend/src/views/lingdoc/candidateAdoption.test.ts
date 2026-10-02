import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import type { AcceptCandidateInput } from '@/api/lingdoc/candidateAdoption'
import { adoptionSignature, keyForAttempt } from './candidateAdoption'

const workspace = readFileSync(new URL('./Workspace.vue', import.meta.url), 'utf8')
const dialog = readFileSync(new URL('../../components/LingDocCandidateAdoptionDialog.vue', import.meta.url), 'utf8')

function inputOf(overrides: Partial<AcceptCandidateInput> = {}): AcceptCandidateInput {
  return {
    candidate_id: 'candidate-1',
    expected_chapter_version_id: null,
    expected_spec_revision: 3,
    replace_existing: false,
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
  assert.equal(adoptionSignature(inputOf()), base)
})

test('the dialog signs the very body it sends', () => {
  // 曾经是两处：签名按 props 现抄一份字段表，请求用另一个对象字面量。两处漂开时
  // 每一边看起来都对。这条断言盯的是那处调用点。
  assert.match(dialog, /keyForAttempt\(attempt\.value, input\.value, newIdempotencyKey\)/)
  assert.match(dialog, /acceptCandidate\(\s*props\.projectId,\s*props\.chapter\.id,\s*input\.value,/)
  assert.doesNotMatch(dialog, /idempotencyKeyForAttempt/)
})

test('the workspace actually mounts the adoption dialog', () => {
  // 缺口 ① 原来的样子就是「组件全仓零引用」：能力造好了，界面上点不到。
  // 所以这条守卫钉的是引用本身，而不是它的行为。
  assert.match(workspace, /import LingDocCandidateAdoptionDialog from '@\/components\/LingDocCandidateAdoptionDialog\.vue'/)
  assert.match(workspace, /<LingDocCandidateAdoptionDialog/)
  assert.match(workspace, /@adopted="onAdopted"/)
  assert.match(workspace, /@click="openAdoption"/)
})
