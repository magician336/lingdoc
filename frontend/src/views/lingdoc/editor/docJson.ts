// Block ↔ ProseMirror JSON 转换 + 连续文稿的章切分 + 跨章保护（纯函数，可单测）。
import type { Block, InlineToken, ListItem } from './markdown'
import { parseChapterMarkdown, chapterToMarkdown, emptyBlocks } from './markdown'

export interface ChapterSpec { id: string; title: string; body_markdown: string }

export type PMNode = { type: string; attrs?: Record<string, unknown>; content?: PMNode[]; text?: string; marks?: Array<{ type: string }> }

function inlineToPm(tokens: InlineToken[]): Array<{ type: 'text'; text: string; marks?: Array<{ type: string }> } | { type: 'sourceRef'; attrs: { id: string } }> {
  const out: Array<{ type: 'text'; text: string; marks?: Array<{ type: string }> } | { type: 'sourceRef'; attrs: { id: string } }> = []
  const walk = (list: InlineToken[], marks: Array<{ type: string }>) => {
    for (const t of list) {
      if (t.t === 'src') out.push({ type: 'sourceRef', attrs: { id: t.id }, ...(marks.length ? { marks: [...marks] } : {}) })
      else if (t.t === 'text') out.push({ type: 'text', text: t.s, ...(marks.length ? { marks: [...marks] } : {}) })
      else walk(t.children, [...marks, { type: t.t }])
    }
  }
  walk(tokens, [])
  return out
}

function itemToPm(it: ListItem): PMNode {
  const para: PMNode = { type: 'paragraph', content: inlineToPm(it.inline).length ? inlineToPm(it.inline) : undefined }
  const content: PMNode[] = [para]
  if (it.sub) content.push(listBlockToPm(it.sub.kind, it.sub.start, it.sub.items))
  return { type: 'listItem', content }
}

function listBlockToPm(kind: 'ul' | 'ol', start: number, items: ListItem[]): PMNode {
  return kind === 'ol'
    ? { type: 'orderedList', attrs: { start }, content: items.map(itemToPm) }
    : { type: 'bulletList', content: items.map(itemToPm) }
}

export function blockToPm(block: Block): PMNode {
  switch (block.kind) {
    case 'p': return { type: 'paragraph', content: inlineToPm(block.inline).length ? inlineToPm(block.inline) : undefined }
    case 'h': return { type: 'heading', attrs: { level: block.level }, content: inlineToPm(block.inline) }
    case 'ul': return listBlockToPm('ul', 1, block.items)
    case 'ol': return listBlockToPm('ol', block.start, block.items)
    case 'raw': return { type: 'rawBlock', attrs: { text: block.text } }
  }
}

function pmMarksToInline(node: PMNode, sink: InlineToken[], active: string[]): void {
  const marks = [...active, ...(node.marks?.map(m => m.type) ?? [])]
  const wrap = (tok: InlineToken): InlineToken => {
    // 逆序还原：bold 在外、italic 在内（与序列化方向相反）
    for (const m of [...marks].reverse()) tok = m === 'bold' ? { t: 'bold', children: [tok] } : m === 'italic' ? { t: 'italic', children: [tok] } : tok
    return tok
  }
  if (node.type === 'text') {
    sink.push(wrap({ t: 'text', s: node.text ?? '' }))
  } else if (node.type === 'hardBreak') {
    sink.push({ t: 'text', s: '\n' })
  } else if (node.type === 'sourceRef') {
    sink.push(wrap({ t: 'src', id: String(node.attrs?.id ?? '') }))
  } else if (node.content) {
    for (const c of node.content) pmMarksToInline(c, sink, marks)
  }
}

function listItemFromPm(node: PMNode): ListItem {
  const kids = node.content ?? []
  const para = kids.find(k => k.type === 'paragraph')
  const inline: InlineToken[] = []
  for (const c of para?.content ?? []) pmMarksToInline(c, inline, [])
  const subNode = kids.find(k => k.type === 'bulletList' || k.type === 'orderedList')
  let sub: ListItem['sub'] = null
  if (subNode) {
    const items = (subNode.content ?? []).map(listItemFromPm)
    sub = subNode.type === 'orderedList'
      ? { kind: 'ol', start: Number(subNode.attrs?.start ?? 1), items }
      : { kind: 'ul', start: 1, items }
  }
  return { inline, sub }
}

export function pmToBlock(node: PMNode): Block {
  switch (node.type) {
    case 'paragraph': {
      const sink: InlineToken[] = []
      for (const c of node.content ?? []) pmMarksToInline(c, sink, [])
      return { kind: 'p', inline: sink }
    }
    case 'heading':
      return { kind: 'h', level: (node.attrs?.level as 2 | 3 | 4) ?? 2, inline: (node.content ?? []).flatMap(c => { const s: InlineToken[] = []; pmMarksToInline(c, s, []); return s }) }
    case 'bulletList':
    case 'orderedList': {
      const items = (node.content ?? []).map(listItemFromPm)
      return node.type === 'bulletList' ? { kind: 'ul', items } : { kind: 'ol', start: Number(node.attrs?.start ?? 1), items }
    }
    case 'rawBlock':
      return { kind: 'raw', text: String(node.attrs?.text ?? '') }
    default:
      return { kind: 'raw', text: '' }
  }
}

/* ---------------- 连续文稿：章标题节点 + 各章内容 ---------------- */

export function chaptersToDocJson(chapters: ChapterSpec[]): { type: 'doc'; content: PMNode[] } {
  const content: PMNode[] = []
  for (const ch of chapters) {
    content.push({ type: 'chapterTitle', attrs: { chapterId: ch.id, title: ch.title } })
    const blocks = parseChapterMarkdown(ch.body_markdown || '')
    const use = blocks.length ? blocks : emptyBlocks()
    for (const b of use) content.push(blockToPm(b))
  }
  return { type: 'doc', content }
}

export interface ChapterDocRange { chapterId: string; title: string; startIndex: number; endIndex: number; markdown: string; dirty: boolean }

// 切分整份 JSON 为各章 markdown（用于保存）。空章输出空串（不写 server 时无操作）。
export function docJsonToChapters(doc: PMNode): ChapterDocRange[] {
  const out: ChapterDocRange[] = []
  const content = doc.content ?? []
  let cur: ChapterDocRange | null = null
  const push = () => { if (cur) { cur.markdown = chapterToMarkdown(curBlocks); out.push(cur) } }
  let curBlocks: Block[] = []
  content.forEach((node, i) => {
    if (node.type === 'chapterTitle') {
      push()
      cur = { chapterId: String(node.attrs?.chapterId ?? ''), title: String(node.attrs?.title ?? ''), startIndex: i, endIndex: i, markdown: '', dirty: false }
      curBlocks = []
    } else if (cur) {
      curBlocks.push(pmToBlock(node))
      cur.endIndex = i
    }
  })
  push()
  return out
}

export function chapterTitleNodes(doc: PMNode): Array<{ chapterId: string; index: number }> {
  return (doc.content ?? [])
    .map((n, i) => ({ n, i }))
    .filter(x => x.n.type === 'chapterTitle')
    .map(x => ({ chapterId: String(x.n.attrs?.chapterId ?? ''), index: x.i }))
}

// 章在 doc.content 中的 [start,end) 区间（含标题节点）
export function chapterIndexRange(doc: PMNode, chapterId: string): { start: number; end: number } | null {
  const titles = chapterTitleNodes(doc)
  const at = titles.findIndex(t => t.chapterId === chapterId)
  if (at < 0) return null
  const start = titles[at].index
  const end = at + 1 < titles.length ? titles[at + 1].index : (doc.content ?? []).length
  return { start, end }
}

/* ---------------- 跨章保护 ---------------- */

export interface SelectionRange { from: number; to: number }

// ProseMirror 顶层 index 区间是否跨章（from/to 为顶层节点区间）。
export function selectionCrossesChapter(doc: PMNode, from: number, to: number): boolean {
  const titles = chapterTitleNodes(doc)
  if (titles.length < 2) return false
  // 跨章 = 有章标题节点严格位于选中顶层区间内部（选中整章标题本身不算跨章，标题是原子）
  const hit = titles.filter(t => t.index > from && t.index < to).length
  return hit > 0
}

// 给定顶层 index，返回所属章
export function chapterAtIndex(doc: PMNode, index: number): string | null {
  const titles = chapterTitleNodes(doc)
  let cur: string | null = null
  for (const t of titles) {
    if (t.index <= index) cur = t.chapterId
    else break
  }
  return cur
}
