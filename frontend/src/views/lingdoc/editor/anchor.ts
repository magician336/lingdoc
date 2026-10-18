// ReviewAnchor（合同 DTO）与 UTF-16 精确定位/重定位。
// 所有 offset 均为该章 body_markdown 的 UTF-16 索引（JS 字符串索引即 UTF-16 code unit）。
export interface ReviewAnchor {
  start_utf16: number
  end_utf16: number
  quote_markdown: string
  working_copy_revision: number
  chapter_version_id: string | null
  status: 'located' | 'stale'
}

export function quoteOccurrences(md: string, quote: string): number[] {
  if (!quote) return []
  const idx: number[] = []
  let from = 0
  for (;;) {
    const at = md.indexOf(quote, from)
    if (at < 0) break
    idx.push(at)
    from = at + 1
  }
  return idx
}

export type RelocateResult =
  | { status: 'located'; start_utf16: number; end_utf16: number }
  | { status: 'stale' }

// 正文修改时仅原 quote 唯一精确出现才可重定位；0 个或 ≥2 个 → stale，绝不跳错段。
export function relocateAnchor(md: string, anchor: ReviewAnchor): RelocateResult {
  const occ = quoteOccurrences(md, anchor.quote_markdown)
  if (occ.length !== 1) return { status: 'stale' }
  const start = occ[0]
  return { status: 'located', start_utf16: start, end_utf16: start + anchor.quote_markdown.length }
}

// 选中范围 → UTF-16 offset（用于选段改写的 selection 映射）。JS 切片天然按 UTF-16。
export function selectionToUtf16(text: string, selStart: number, selEnd: number): { start_utf16: number; end_utf16: number } {
  return { start_utf16: selStart, end_utf16: selEnd }
}

export function utf16Slice(md: string, start: number, end: number): string {
  return md.slice(start, end)
}
