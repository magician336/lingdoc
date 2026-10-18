export interface ChapterCommitBasis {
  projectId: string
  chapterId: string
  identity: string
  session: number
  specRevision: number
  workingCopyRevision: number
  chapterVersionId: string | null
}

// A human choice belongs to the exact saved copy they read. It cannot approve
// another chapter, an intervening autosave, or a different signed-in identity.
export function sameChapterCommitBasis(read: ChapterCommitBasis, current: ChapterCommitBasis | null): boolean {
  return !!current && read.projectId === current.projectId && read.chapterId === current.chapterId &&
    read.identity === current.identity && read.session === current.session &&
    read.specRevision === current.specRevision && read.workingCopyRevision === current.workingCopyRevision &&
    read.chapterVersionId === current.chapterVersionId
}
