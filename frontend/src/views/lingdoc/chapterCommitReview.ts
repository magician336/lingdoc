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

// The wording-commit condition gate (#753): a registered condition value that
// vanished from the text is a SUSPICION the server reports, never a verdict.
// The client only parses what the server saw and mirrors the same field ids
// back on the audited dismissal — it never judges condition changes itself.
export interface SuspectedConditionChange {
  field_id: string
  value: string
}

export interface ConditionChangeDismissalInput {
  dismissed_fields: string[]
  reason: string
}

export function parseSuspectedConditions(error: unknown): SuspectedConditionChange[] | null {
  const item = error as { error?: { code?: string; details?: { suspected_condition_changes?: SuspectedConditionChange[] } } }
  if (item?.error?.code !== 'condition_change_confirmation_required') return null
  const suspected = item.error.details?.suspected_condition_changes
  return Array.isArray(suspected) && suspected.length > 0 ? suspected : []
}

export function dismissalFromSuspected(suspected: SuspectedConditionChange[], reason: string): ConditionChangeDismissalInput | null {
  const trimmed = reason.trim()
  if (suspected.length === 0 || trimmed === '') return null
  return { dismissed_fields: suspected.map(item => item.field_id), reason: trimmed }
}
