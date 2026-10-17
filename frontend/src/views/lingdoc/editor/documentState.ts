// Formal identity is semantic. A commit advances the working-copy anchor
// revision while retaining its meaning; no other anchor field is ignored.
export interface DocumentVersion {
  body_markdown: string
  source_ids: string[]
  review_items: unknown[]
  citation_usages?: unknown[]
  current_version_id?: string | null
  base_chapter_version_id?: string | null
}
function stable(value: unknown, anchor = false): unknown {
  if (Array.isArray(value)) return value.map(v => stable(v))
  if (!value || typeof value !== 'object') return value
  return Object.fromEntries(Object.entries(value as Record<string, unknown>)
    .filter(([k]) => !(anchor && k === 'working_copy_revision'))
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([k, v]) => [k, stable(v, k === 'anchor')]))
}
function ordered(values: unknown[], key: string): unknown[] {
  return [...values].sort((a, b) => String((a as Record<string, unknown>)?.[key] ?? '').localeCompare(String((b as Record<string, unknown>)?.[key] ?? '')))
}
export function copyMatchesFormal(copy: DocumentVersion, formal: DocumentVersion): boolean {
  return copy.base_chapter_version_id === formal.current_version_id &&
    copy.body_markdown === formal.body_markdown &&
    JSON.stringify([...copy.source_ids].sort()) === JSON.stringify([...formal.source_ids].sort()) &&
    JSON.stringify(stable(ordered(copy.review_items, 'id'))) === JSON.stringify(stable(ordered(formal.review_items, 'id'))) &&
    JSON.stringify(stable(ordered(copy.citation_usages ?? [], 'source_id'))) === JSON.stringify(stable(ordered(formal.citation_usages ?? [], 'source_id')))
}
export function committedRevision(copy: DocumentVersion & { working_copy_revision: number }, formal: DocumentVersion): number {
  return formal.current_version_id && copyMatchesFormal(copy, formal) ? copy.working_copy_revision : 0
}
export function currentAnchorRange(body: string, anchor: {status: string;start_utf16:number;end_utf16:number;quote_markdown:string}|null|undefined) {
  if (!anchor || anchor.status !== 'located' || anchor.start_utf16 < 0 || anchor.end_utf16 <= anchor.start_utf16 || anchor.end_utf16 > body.length) return null
  return body.slice(anchor.start_utf16,anchor.end_utf16) === anchor.quote_markdown
    ? {from:anchor.start_utf16,to:anchor.end_utf16,quote:anchor.quote_markdown} : null
}

export function sortChaptersByTemplate<T extends {section_id:string}>(chapters:T[],sections:Array<{section_id:string;order?:number}>):T[] {
 const order=new Map([...sections].sort((a,b)=>(a.order??0)-(b.order??0)).map((s,i)=>[s.section_id,i]))
 return chapters.map((chapter,index)=>({chapter,index})).sort((a,b)=>(order.get(a.chapter.section_id)??Number.MAX_SAFE_INTEGER)-(order.get(b.chapter.section_id)??Number.MAX_SAFE_INTEGER)||a.index-b.index).map(x=>x.chapter)
}
