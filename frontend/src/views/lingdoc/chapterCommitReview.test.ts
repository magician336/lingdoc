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
