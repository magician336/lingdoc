import type { Node as ProseMirrorNode } from '@tiptap/pm/model'
import type { Transaction } from '@tiptap/pm/state'

export type ChapterWritePermissions = Readonly<Record<string, boolean>> | undefined
export interface ChapterWriteDecision { allowed: boolean; chapterId?: string; reason?: 'readonly' | 'invalid-document' | 'chapter-boundary' }

// Only an absent prop preserves the legacy editor contract. In production, a
// missing/unknown entry in the server-derived map never grants write access.
export function canWriteChapter(permissions: ChapterWritePermissions, chapterId: string | null | undefined): boolean {
  return permissions === undefined || !!chapterId && permissions !== null && typeof permissions === 'object' && Object.prototype.hasOwnProperty.call(permissions, chapterId) && permissions[chapterId] === true
}

interface ChapterSlice { id: string; title: ProseMirrorNode; body: ProseMirrorNode[] }
function chapterSlices(doc: ProseMirrorNode): ChapterSlice[] | null {
  const result: ChapterSlice[] = [], seen = new Set<string>()
  let current: ChapterSlice | undefined
  let valid = true
  doc.forEach(node => {
    if (node.type.name === 'chapterTitle') {
      const id = typeof node.attrs.chapterId === 'string' ? node.attrs.chapterId : ''
      if (!id || seen.has(id)) { valid = false; return }
      seen.add(id); current = { id, title: node, body: [] }; result.push(current)
    } else if (current) current.body.push(node)
    else valid = false
  })
  return valid ? result : null
}

function compareChapterWrite(before: ProseMirrorNode, after: ProseMirrorNode, permissions: ChapterWritePermissions): ChapterWriteDecision {
  const original = chapterSlices(before), changed = chapterSlices(after)
  if (!original || !changed) return { allowed: false, reason: 'invalid-document' }
  if (original.length !== changed.length) return { allowed: false, reason: 'chapter-boundary' }
  for (let i = 0; i < original.length; i++) {
    const previous = original[i], next = changed[i]
    // A local edit cannot move, rename or replace an atomic chapter title.
    if (previous.id !== next.id || !previous.title.eq(next.title)) return { allowed: false, chapterId: previous.id, reason: 'chapter-boundary' }
    if (canWriteChapter(permissions, previous.id)) continue
    if (previous.body.length !== next.body.length || previous.body.some((node, n) => !node.eq(next.body[n]))) return { allowed: false, chapterId: previous.id, reason: 'readonly' }
  }
  return { allowed: true }
}

// Inspect each real step document, not only the final selection or a lossy
// Markdown/text projection. Text, marks, nested nodes and atom attributes are
// all covered. A transaction's public metadata can never authorize a write.
export function checkChapterWriteTransaction(transaction: Transaction, permissions: ChapterWritePermissions): ChapterWriteDecision {
  if (!transaction.docChanged || permissions === undefined) return { allowed: true }
  for (let i = 0; i < transaction.docs.length; i++) {
    const result = compareChapterWrite(transaction.docs[i], transaction.docs[i + 1] ?? transaction.doc, permissions)
    if (!result.allowed) return result
  }
  return { allowed: true }
}

export function chapterReadonlyReason(chapterId: string | null | undefined, permissions: ChapterWritePermissions, reasons: Readonly<Record<string, string>> = {}): string | null {
  if (canWriteChapter(permissions, chapterId)) return null
  return chapterId && reasons[chapterId]?.trim() || '当前章节仅可阅读；你没有该章节的正文写入权限。'
}