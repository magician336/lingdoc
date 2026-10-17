// 自定义 Tiptap 节点定义（组件与 schema 构造共用，避免两处漂移）。
import { Node } from '@tiptap/vue-3'

export const SourceRef = Node.create({
  name: 'sourceRef',
  atom: true,
  inline: true,
  group: 'inline',
  addAttributes() { return { id: { default: '' } } },
  parseHTML() { return [{ tag: 'span[data-source-ref]' }] },
  renderHTML({ HTMLAttributes }) {
    const id = String(HTMLAttributes.id ?? '')
    // 正文里只显示短编号/名称；完整 ID 留在技术详情（title）
    const short = id.length > 12 ? id.slice(0, 8) + '…' : id
    return ['span', { ...HTMLAttributes, 'data-source-ref': id, title: `来源 ${id}`, class: 'ld-source' }, `[来源 ${short}]`]
  },
})

export const RawBlock = Node.create({
  name: 'rawBlock',
  atom: true,
  group: 'block',
  addAttributes() { return { text: { default: '' } } },
  parseHTML() { return [{ tag: 'pre[data-raw-block]' }] },
  renderHTML({ HTMLAttributes }) {
    return ['pre', { ...HTMLAttributes, 'data-raw-block': '1', class: 'ld-raw' }, (HTMLAttributes as { text: string }).text]
  },
})

export const ChapterTitle = Node.create({
  name: 'chapterTitle',
  atom: true,
  group: 'block',
  selectable: false,
  addAttributes() { return { chapterId: { default: '' }, title: { default: '' } } },
  parseHTML() { return [{ tag: 'h2[data-chapter-title]' }] },
  renderHTML({ HTMLAttributes }) {
    return ['h2', { ...HTMLAttributes, 'data-chapter-title': HTMLAttributes.chapterId, class: 'ld-chapter-title' }, (HTMLAttributes as { title: string }).title]
  },
})
