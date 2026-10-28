import test from 'node:test'
import assert from 'node:assert/strict'
import { sameChapterCommitBasis, type ChapterCommitBasis } from './chapterCommitReview'

const read: ChapterCommitBasis = { projectId: 'project-a', chapterId: 'chapter-a', identity: 'tenant-a:author-a', session: 2, specRevision: 3, workingCopyRevision: 7, chapterVersionId: 'version-a' }

test('human wording choice is usable only for the exact saved copy that was reviewed', () => {
  assert.equal(sameChapterCommitBasis(read, { ...read }), true)
  assert.equal(sameChapterCommitBasis(read, null), false)
  for (const changed of [
    { projectId: 'project-b' }, { chapterId: 'chapter-b' }, { identity: 'tenant-b:author-a' },
    { identity: 'tenant-a:author-b' }, { session: 3 }, { specRevision: 4 },
    { workingCopyRevision: 8 }, { chapterVersionId: 'version-b' }, { chapterVersionId: null },
  ]) assert.equal(sameChapterCommitBasis(read, { ...read, ...changed }), false, JSON.stringify(changed))
})

test('a first chapter with no formal version still has a concrete review basis', () => {
  const first = { ...read, chapterVersionId: null }
  assert.equal(sameChapterCommitBasis(first, { ...first }), true)
  assert.equal(sameChapterCommitBasis(first, read), false)
})

import { dismissalFromSuspected, parseSuspectedConditions } from './chapterCommitReview'

test('the suspected list is parsed only from the server refusal, never judged locally', () => {
  const refusal = { error: { code: 'condition_change_confirmation_required', details: { suspected_condition_changes: [{ field_id: 'research_subject', value: '旧研究主题值' }] } } }
  assert.deepEqual(parseSuspectedConditions(refusal), [{ field_id: 'research_subject', value: '旧研究主题值' }])
  assert.equal(parseSuspectedConditions({ error: { code: 'version_conflict' } }), null)
  // A refusal without a list still parses as "that error, empty list"; the
  // panel treats it like any other failure instead of opening the second state.
  assert.deepEqual(parseSuspectedConditions({ error: { code: 'condition_change_confirmation_required', details: {} } }), [])
  assert.equal(parseSuspectedConditions(new Error('network')), null)
})

test('a dismissal mirrors the server field ids and requires a reason', () => {
  const suspected = [{ field_id: 'research_subject', value: '旧研究主题值' }, { field_id: 'research_goal', value: '目标' }]
  assert.deepEqual(dismissalFromSuspected(suspected, '  仅改写表述  '), { dismissed_fields: ['research_subject', 'research_goal'], reason: '仅改写表述' })
  assert.equal(dismissalFromSuspected(suspected, '   '), null)
  assert.equal(dismissalFromSuspected([], '理由'), null)
})
