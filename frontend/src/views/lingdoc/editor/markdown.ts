// 章节 Markdown 双向转换 v2：词法用 marked v17（项目既有依赖），不再用多段正则凑语法。
// 选择 marked 而非 @tiptap/markdown 适配层的理由：合同要求不支持的既有块"原样保留不洗掉"，
// 且 [[source:id]] 原子需要注入强调内部；marked 的 token.raw 给逐字节原文保留，token 树完全可控。
// 结构判据：acceptance/document-editor-20261013/editor-md-golden.json（双方共同 golden）。
import { marked } from 'marked'

export type InlineToken =
  | { t: 'text'; s: string }
  | { t: 'bold'; children: InlineToken[] }
  | { t: 'italic'; children: InlineToken[] }
  | { t: 'src'; id: string }

export interface ListItem { inline: InlineToken[]; sub: { kind: 'ul' | 'ol'; start: number; items: ListItem[] } | null }
export type Block =
  | { kind: 'p'; inline: InlineToken[] }
  | { kind: 'h'; level: 2 | 3 | 4; inline: InlineToken[] }
  | { kind: 'ul'; items: ListItem[] }
  | { kind: 'ol'; start: number; items: ListItem[] }
  | { kind: 'raw'; text: string }

const SRC_RE = /\[\[source:([^\]\s]+)\]\]/g

/* ---------------- 实体解码（marked 不解实体，展示层负责） ---------------- */
const NAMED: Record<string, string> = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'" }
export function decodeEntities(s: string): string {
  return s.replace(/&(#x?[0-9a-fA-F]+|[a-zA-Z]+);/g, (m, body: string) => {
    if (body[0] === '#') {
      const code = body[1]?.toLowerCase() === 'x' ? parseInt(body.slice(2), 16) : parseInt(body.slice(1), 10)
      return Number.isFinite(code) ? String.fromCodePoint(code) : m
    }
    return NAMED[body] ?? m
  })
}

/* ---------------- 行内：marked tokens → InlineToken ---------------- */
type MdInline = { type: string; text?: string; raw?: string; tokens?: MdInline[]; href?: string; title?: string }

function pushText(out: InlineToken[], s: string) {
  if (!s) return
  const last = out[out.length - 1]
  if (last && last.t === 'text') last.s += s
  else out.push({ t: 'text', s })
}

function splitSourceMarkers(text: string, out: InlineToken[]) {
  SRC_RE.lastIndex = 0
  let last = 0
  let m: RegExpExecArray | null
  while ((m = SRC_RE.exec(text))) {
    pushText(out, decodeEntities(text.slice(last, m.index)))
    out.push({ t: 'src', id: m[1] })
    last = m.index + m[0].length
  }
  pushText(out, decodeEntities(text.slice(last)))
}

function inlineFromMarked(tokens: MdInline[] | undefined): InlineToken[] {
  const out: InlineToken[] = []
  for (const tok of tokens ?? []) {
    switch (tok.type) {
      case 'text':
      case 'escape':
        splitSourceMarkers(tok.text ?? '', out)
        break
      case 'strong':
        out.push({ t: 'bold', children: inlineFromMarked(tok.tokens) })
        break
      case 'em':
        out.push({ t: 'italic', children: inlineFromMarked(tok.tokens) })
        break
      case 'del':
        out.push({ t: 'text', s: `~~${plainOf(inlineFromMarked(tok.tokens))}~~` })
        break
      case 'codespan':
        out.push({ t: 'text', s: '`' + (tok.text ?? '') + '`' })
        break
      case 'br':
        pushText(out, '\n')
        break
      case 'link':
        out.push({ t: 'text', s: `[${plainOf(inlineFromMarked(tok.tokens))}](${tok.href ?? ''})` })
        break
      case 'image':
        out.push({ t: 'text', s: tok.raw ?? '' })
        break
      default:
        // html 等不支持的内联：保留原文，序列化时会转义（结构等价，不静默丢弃）
        pushText(out, tok.raw ?? '')
    }
  }
  return out
}

export function plainOf(tokens: InlineToken[]): string {
  return tokens.map(t => t.t === 'text' ? t.s : t.t === 'src' ? '' : plainOf(t.children)).join('')
}

// 公开的单段行内解析（组件验证 quote 语义用）
export function parseInlineText(text: string): InlineToken[] {
  const toks = marked.lexer(text.replace(/\r\n?/g, '\n')) as unknown as MdBlock[]
  const para = toks.find(t => t.type === 'paragraph')
  return inlineFromMarked((para?.tokens as MdInline[] | undefined) ?? [{ type: 'text', text } as MdInline])
}

/* ---------------- 块：marked tokens → Block ---------------- */
type MdBlock = {
  type: string; raw?: string; depth?: number; tokens?: MdBlock[]
  items?: Array<{ tokens?: MdBlock[] }>
  ordered?: boolean; start?: number | string
}

function inlineOfBlock(tok: MdBlock): InlineToken[] {
  // paragraph/heading 的内联在 tokens；list_item 的首段在 tokens[0].tokens
  return inlineFromMarked((tok.tokens as MdInline[] | undefined))
}

function listItemFromMarked(it: { tokens?: MdBlock[] }): ListItem {
  const toks = it.tokens ?? []
  const first = toks[0]
  const inline = first && (first.type === 'text' || first.type === 'paragraph')
    ? inlineFromMarked((first.tokens as MdInline[] | undefined) ?? [{ type: 'text', text: (first as { text?: string }).text ?? '' } as MdInline])
    : []
  const subTok = toks.find(t => t.type === 'list') as MdBlock | undefined
  let sub: ListItem['sub'] = null
  if (subTok) {
    sub = subTok.ordered
      ? { kind: 'ol', start: Number(subTok.start ?? 1), items: (subTok.items ?? []).map(listItemFromMarked) }
      : { kind: 'ul', start: 1, items: (subTok.items ?? []).map(listItemFromMarked) }
  }
  return { inline, sub }
}

export function parseChapterMarkdown(md: string): Block[] {
  const normalized = md.replace(/\r\n?/g, '\n')
  const tokens = marked.lexer(normalized) as unknown as MdBlock[]
  const blocks: Block[] = []
  for (const tok of tokens) {
    switch (tok.type) {
      case 'heading': {
        const depth = tok.depth ?? 0
        if (depth >= 2 && depth <= 4) blocks.push({ kind: 'h', level: depth as 2 | 3 | 4, inline: inlineOfBlock(tok) })
        else blocks.push({ kind: 'raw', text: tok.raw ?? '' })
        break
      }
      case 'paragraph':
        blocks.push({ kind: 'p', inline: inlineOfBlock(tok) })
        break
      case 'list':
        if (tok.ordered) blocks.push({ kind: 'ol', start: Number(tok.start ?? 1), items: (tok.items ?? []).map(listItemFromMarked) })
        else blocks.push({ kind: 'ul', items: (tok.items ?? []).map(listItemFromMarked) })
        break
      case 'space':
        break
      default:
        // blockquote / code / table / html / hr 等：token.raw 逐字节保留（去尾部换行属于版式分隔，不洗掉内容），不洗掉
        blocks.push({ kind: 'raw', text: (tok.raw ?? '').replace(/\n+$/, '') })
    }
  }
  return blocks
}

/* ---------------- 序列化 ---------------- */
function escapeText(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
    .replace(/\*/g, '\\*').replace(/_/g, '\\_').replace(/`/g, '\\`')
}

function isWrapped(t: InlineToken): t is { t: 'bold'; children: InlineToken[] } | { t: 'italic'; children: InlineToken[] } {
  return t.t === 'bold' || t.t === 'italic'
}

function mergeAdjacent(tokens: InlineToken[]): InlineToken[] {
  const out: InlineToken[] = []
  for (const t of tokens) {
    const last = out[out.length - 1]
    if (last && isWrapped(t) && isWrapped(last) && t.t === last.t) {
      last.children.push(...t.children)
    } else if (isWrapped(t)) {
      out.push({ ...t, children: [...t.children] })
    } else out.push(t)
  }
  return out.map(t => isWrapped(t) ? { ...t, children: mergeAdjacent(t.children) } : t)
}

export function inlineToMarkdown(tokens: InlineToken[]): string {
  return mergeAdjacent(tokens).map(t => {
    switch (t.t) {
      case 'text': return escapeText(t.s)
      case 'bold': return `**${inlineToMarkdown(t.children)}**`
      case 'italic': return `*${inlineToMarkdown(t.children)}*`
      case 'src': return `[[source:${t.id}]]`
    }
  }).join('')
}

function listToMarkdown(kind: 'ul' | 'ol', start: number, items: ListItem[], indent: string): string {
  return items.map((it, i) => {
    const bullet = kind === 'ol' ? `${start + i}. ` : '- '
    const head = indent + bullet + inlineToMarkdown(it.inline)
    if (!it.sub) return head
    const subMd = listToMarkdown(it.sub.kind, it.sub.start, it.sub.items, indent + '   ')
    return head + '\n' + subMd
  }).join('\n')
}

export function blockToMarkdown(block: Block): string {
  switch (block.kind) {
    case 'p': return inlineToMarkdown(block.inline)
    case 'h': return `${'#'.repeat(block.level)} ${inlineToMarkdown(block.inline)}`
    case 'ul': return listToMarkdown('ul', 1, block.items, '')
    case 'ol': return listToMarkdown('ol', block.start, block.items, '')
    case 'raw': return block.text
  }
}

export function chapterToMarkdown(blocks: Block[]): string {
  return blocks.map(blockToMarkdown).join('\n\n')
}

export function emptyBlocks(): Block[] { return [{ kind: 'p', inline: [] }] }
