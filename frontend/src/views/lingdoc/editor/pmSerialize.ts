// PM 文档 ↔ 章 Markdown 的单一事实来源序列化（带位置映射）。
// 与 markdown.ts 的 Block 路径保持同构；测试强制二者逐字节一致（parity）。
// 选区映射一律以本模块产出的"实际保存正文"为判据，并用 quote===slice 与选中语义双重验证。
import type { Node as PMNodeType } from '@tiptap/pm/model'
import { chapterTitleNodes, docJsonToChapters } from './docJson'
import type { PMNode } from './docJson'
import { decodeEntities } from './markdown'

export interface Span {
  pmStart: number
  pmEnd: number
  mdStart: number
  mdLen: number
  atom: boolean
  // 文本 span：pmCharMap[k] = 第 k 个 PM 字符在 markdown 中的偏移（转义对占 2 md 字符 / 1 PM 字符）
  pmCharMap?: number[]
}

export interface ChapterMap {
  chapterId: string
  markdown: string
  spans: Span[]
}

type PmNode = PMNodeType

function escapeText(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
    .replace(/\*/g, '\\*').replace(/_/g, '\\_').replace(/`/g, '\\`')
}

class Emitter {
  markdown = ''
  spans: Span[] = []
  active: MarkName[] = []
  emit(md: string, pmStart?: number, pmLen?: number, atom = false) {
    if (pmStart !== undefined && pmLen !== undefined && pmLen > 0) {
      const span: Span = { pmStart, pmEnd: pmStart + pmLen, mdStart: this.markdown.length, mdLen: md.length, atom }
      if (!atom) {
        const map: number[] = [this.markdown.length]
        let i = 0
        let pmChars = 0
        while (pmChars < pmLen && i < md.length) {
          if (md[i] === '\\' && i + 1 < md.length) i += 2
          else if (md[i] === '&') {
            const ent = /^&(#x?[0-9a-fA-F]+|[a-zA-Z]+);/.exec(md.slice(i))
            i += ent ? ent[0].length : 1
          }
          else i += 1
          pmChars++
          map.push(this.markdown.length + i)
        }
        span.pmCharMap = map
      }
      this.spans.push(span)
    }
    this.markdown += md
  }
  openClose(eff: MarkName[]) {
    const closing = this.active.filter(m => !eff.includes(m))
    for (const m of [...closing].reverse()) this.emit(WRAP[m][1])
    const opening = eff.filter(m => !this.active.includes(m))
    for (const m of opening) this.emit(WRAP[m][0])
    this.active = eff
  }
}

type MarkName = 'bold' | 'italic'
const WRAP: Record<MarkName, [string, string]> = { bold: ['**', '**'], italic: ['*', '*'] }

function marksOf(node: PmNode, parent: MarkName[]): MarkName[] {
  const own = (node.marks ?? []).map(m => m.type.name).filter((n): n is MarkName => n === 'bold' || n === 'italic')
  const merged = [...parent]
  for (const m of own) if (!merged.includes(m)) merged.push(m)
  // 规范序：bold 在外，与 Block 路径一致
  return merged.sort((a, b) => (a === 'bold' ? 0 : 1) - (b === 'bold' ? 0 : 1))
}

function emitInline(node: PmNode, pos: number, effective: MarkName[], em: Emitter): number {
  const eff = marksOf(node, effective)
  if (node.isText) {
    em.openClose(eff)
    const text = node.text ?? ''
    em.emit(escapeText(text), pos, text.length)
    return pos + text.length
  }
  if (node.type.name === 'sourceRef') {
    em.openClose(eff)
    const s = `[[source:${String(node.attrs.id ?? '')}]]`
    em.emit(s, pos, node.nodeSize, true)
    return pos + node.nodeSize
  }
  if (node.type.name === 'hardBreak') {
    em.openClose([])
    em.emit('\n', pos, node.nodeSize, true)
    return pos + node.nodeSize
  }
  let p = pos + 1
  node.forEach(child => {
    p = emitInline(child, p, eff, em)
  })
  return pos + node.nodeSize
}

function emitParagraphLike(node: PmNode, pos: number, em: Emitter, prefix: string): void {
  em.emit(prefix)
  let p = pos + 1
  node.forEach(child => {
    p = emitInline(child, p, [], em)
  })
  em.openClose([])
  em.emit('\n\n')
}

function emitList(node: PmNode, pos: number, em: Emitter, ordered: boolean, start: number, indent: string): number {
  let p = pos + 1
  let i = 0
  node.forEach(item => {
    const bullet = ordered ? `${start + i}. ` : '- '
    em.emit(indent + bullet)
    let ip = p + 1 // item 内容起始
    item.forEach(child => {
      if (child.type.name === 'paragraph') {
        let pp = ip + 1
        child.forEach(grand => { pp = emitInline(grand, pp, [], em) })
        em.openClose([])
        ip += child.nodeSize
      } else if (child.type.name === 'bulletList' || child.type.name === 'orderedList') {
        em.emit('\n')
        const subStart = child.type.name === 'orderedList' ? Number(child.attrs.start ?? 1) : 1
        ip = emitList(child, ip, em, child.type.name === 'orderedList', subStart, indent + '   ')
      } else {
        ip += child.nodeSize
      }
    })
    i++
    em.emit(i < node.childCount ? '\n' : '')
    p += item.nodeSize
  })
  return pos + node.nodeSize
}

function emitBlock(node: PmNode, pos: number, em: Emitter): number {
  switch (node.type.name) {
    case 'paragraph':
      emitParagraphLike(node, pos, em, '')
      return pos + node.nodeSize
    case 'heading':
      emitParagraphLike(node, pos, em, `${'#'.repeat(Number(node.attrs.level ?? 2))} `)
      return pos + node.nodeSize
    case 'bulletList':
      emitList(node, pos, em, false, 1, '')
      em.emit('\n\n')
      return pos + node.nodeSize
    case 'orderedList': {
      emitList(node, pos, em, true, Number(node.attrs.start ?? 1), '')
      em.emit('\n\n')
      return pos + node.nodeSize
    }
    case 'rawBlock':
      em.emit(String(node.attrs.text ?? ''), pos, node.nodeSize, true)
      em.emit('\n\n')
      return pos + node.nodeSize
    default:
      // 未知块：按原子保留（与 docJson 的 raw 回退一致）
      em.emit('', pos, node.nodeSize, true)
      em.emit('\n\n')
      return pos + node.nodeSize
  }
}

export function chapterMaps(doc: PmNode): ChapterMap[] {
  const json = doc.toJSON() as PMNode
  const titles = chapterTitleNodes(json)
  const out: ChapterMap[] = []
  let topPos = 0
  const childPos: number[] = []
  doc.forEach((_n, _o, i) => { childPos[i] = topPos; topPos += doc.child(i).nodeSize })
  titles.forEach((t, i) => {
    const em = new Emitter()
    const end = i + 1 < titles.length ? titles[i + 1].index : doc.childCount
    for (let k = t.index + 1; k < end; k++) emitBlock(doc.child(k), childPos[k], em)
    out.push({ chapterId: t.chapterId, markdown: em.markdown.replace(/\n\n$/, ''), spans: em.spans })
  })
  return out
}

export function savedMarkdownOf(doc: PmNode, chapterId: string): string | null {
  return chapterMaps(doc).find(c => c.chapterId === chapterId)?.markdown ?? null
}

export type Utf16MapResult = { ok: true; start_utf16: number; end_utf16: number; quote: string } | { ok: false; reason: 'unsafe-range' | 'no-chapter' | 'empty' }

function pmPosToMd(cm: ChapterMap, pmPos: number): number | null {
  for (const sp of cm.spans) {
    if (pmPos >= sp.pmStart && pmPos <= sp.pmEnd) {
      if (sp.atom) return pmPos === sp.pmStart ? sp.mdStart : sp.mdStart + sp.mdLen
      return sp.pmCharMap![pmPos - sp.pmStart]
    }
  }
  return null
}

export function pmRangeToUtf16(doc: PmNode, chapterId: string, from: number, to: number): Utf16MapResult {
  const cm = chapterMaps(doc).find(c => c.chapterId === chapterId)
  if (!cm) return { ok: false, reason: 'no-chapter' }
  if (to <= from) return { ok: false, reason: 'empty' }
  const s = pmPosToMd(cm, from)
  const e = pmPosToMd(cm, to)
  if (s === null || e === null || e < s) return { ok: false, reason: 'unsafe-range' }
  const quote = cm.markdown.slice(s, e)
  if (!quote) return { ok: false, reason: 'empty' }
  return { ok: true, start_utf16: s, end_utf16: e, quote }
}

// 章内纯文本偏移 → PM 位置（查找/替换回设选区用）
export function mdOffsetToPmPos(doc: PmNode, chapterId: string, mdOffset: number): number | null {
  const cm = chapterMaps(doc).find(c => c.chapterId === chapterId)
  if (!cm) return null
  for (const sp of cm.spans) {
    if (!sp.atom && mdOffset >= sp.mdStart && mdOffset <= sp.mdStart + sp.mdLen) {
      const map = sp.pmCharMap!
      let k = 0
      while (k + 1 < map.length && map[k + 1] <= mdOffset) k++
      return sp.pmStart + k
    }
  }
  return null
}

// 去掉反斜杠转义后的"语义文本"（用于验证 quote 正是用户选中的内容）
export function semanticText(s: string): string {
  return decodeEntities(s.replace(/\\([\\`*_[\]{}()#+\-.!~>])/g, '$1'))
}

/* ---------------- 语义索引：跨 marks/转义的查找与选区回设 ---------------- */

export interface SemanticChapter {
  chapterId: string
  semantic: string
  charMap: Array<{ mdStart: number; mdEnd: number }>
}

// 每章：把保存正文按 span 展开成语义纯文本（atoms 不计字符，转义还原），
// 每个语义字符记录其 md 区间（转义字符不单独占用语义位）。
export function buildSemanticIndex(doc: PmNode): SemanticChapter[] {
  return chapterMaps(doc).map(cm => {
    let semantic = ''
    const charMap: Array<{ mdStart: number; mdEnd: number }> = []
    const spans = [...cm.spans].sort((x, y) => x.mdStart - y.mdStart)
    for (const sp of spans) {
      if (sp.atom) continue
      const map = sp.pmCharMap!
      for (let k = 0; k < map.length - 1; k++) {
        charMap.push({ mdStart: map[k], mdEnd: map[k + 1] })
        semantic += cm.markdown.slice(map[k], map[k + 1]).replace(/^\\(.)/, '$1')
      }
    }
    return { chapterId: cm.chapterId, semantic, charMap }
  })
}

export function findSemanticHits(index: SemanticChapter[], needle: string): Array<{ chapterId: string; from: number; to: number }> {
  const hits: Array<{ chapterId: string; from: number; to: number }> = []
  for (const ch of index) {
    let at = 0
    for (;;) {
      const i = ch.semantic.indexOf(needle, at)
      if (i < 0) break
      hits.push({ chapterId: ch.chapterId, from: i, to: i + needle.length })
      at = i + 1
    }
  }
  return hits
}

// 语义字符区间 → md 区间（跨章调用前先按 chapterId 过滤）
export function semanticRangeToMd(ch: SemanticChapter, from: number, to: number): { mdStart: number; mdEnd: number } | null {
  const a = ch.charMap[from]
  const b = ch.charMap[to - 1]
  if (!a || !b) return null
  return { mdStart: a.mdStart, mdEnd: b.mdEnd }
}

// Exact persisted Markdown range -> visible PM range. Formatting delimiters
// have no glyph position; entity/escape/atom interiors are never guessed.
export function markdownRangeToPm(doc: PmNode, chapterId: string, from: number, to: number, quote: string): {from:number;to:number}|null {
  const actual=docJsonToChapters(doc.toJSON() as PMNode).find(c=>c.chapterId===chapterId)?.markdown
  if(actual===undefined || !Number.isInteger(from) || !Number.isInteger(to) || from<0 || to<=from || to>actual.length || actual.slice(from,to)!==quote) return null
  const splitsSurrogate=(at:number)=>at>0 && at<actual.length && /[\uD800-\uDBFF]/.test(actual[at-1]) && /[\uDC00-\uDFFF]/.test(actual[at])
  if(splitsSurrogate(from)||splitsSurrogate(to)) return null
  const cm=chapterMaps(doc).find(c=>c.chapterId===chapterId)
  if(!cm || cm.markdown!==actual) return null
  const ranges:Array<{from:number;to:number}>=[]
  for(const span of cm.spans) {
    const left=Math.max(from,span.mdStart),right=Math.min(to,span.mdStart+span.mdLen)
    if(right<=left) continue
    if(span.atom) {
      if(left!==span.mdStart || right!==span.mdStart+span.mdLen) return null
      ranges.push({from:span.pmStart,to:span.pmEnd})
    } else {
      const a=span.pmCharMap!.indexOf(left),b=span.pmCharMap!.indexOf(right)
      if(a<0||b<0) return null
      ranges.push({from:span.pmStart+a,to:span.pmStart+b})
    }
  }
  if(!ranges.length) return null
  const a=Math.min(...ranges.map(r=>r.from)),b=Math.max(...ranges.map(r=>r.to))
  return b>a?{from:a,to:b}:null
}
