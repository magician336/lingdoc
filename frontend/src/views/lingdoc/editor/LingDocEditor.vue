<script setup lang="ts">
// LingDoc 连续文稿编辑器（Tiptap 3.31.4 真编辑器切片）。
// 合同要点：Markdown 唯一服务器正文；章标题原子、边界不可合并/删除；跨章改写被阻止；
// 各章 900ms CAS 保存队列 + Ctrl+S 冲刷；引用原子序列化 [[source:id]]。
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { EditorContent, Extension, Node, useEditor } from '@tiptap/vue-3'
import StarterKit from '@tiptap/starter-kit'
import { Plugin, PluginKey } from '@tiptap/pm/state'
import { Decoration, DecorationSet } from '@tiptap/pm/view'
import { SourceRef as SharedSourceRef, RawBlock, ChapterTitle } from './nodes'
import type { PMNode } from './docJson'
import { chaptersToDocJson, docJsonToChapters, chapterTitleNodes, type ChapterSpec } from './docJson'
import { ChapterSaveQueue } from './saveQueue'
import type { SaveResult } from './saveQueue'
import { validateCandidate, draftBlockReason } from './guard'
import type { EditorContext, Candidate } from './guard'
import { canWriteChapter, checkChapterWriteTransaction, chapterReadonlyReason } from './editorScope'
import { parseChapterMarkdown, chapterToMarkdown, parseInlineText, plainOf } from './markdown'
import { chapterMaps, savedMarkdownOf, pmRangeToUtf16, semanticText, buildSemanticIndex, findSemanticHits, semanticRangeToMd, mdOffsetToPmPos, markdownRangeToPm } from './pmSerialize'

const props = withDefaults(defineProps<{
  chapters: ChapterSpec[]
  projectId?: string
  chapterWritePermissions?: Record<string, boolean>
  chapterWritePermissionReasons?: Record<string, string>
  confirmed?: Record<string, { chapterVersionId: string }>
  saveChapter?: (chapterId: string, markdown: string, expectedWorkingCopyRevision: number) => Promise<SaveResult>
  baselines?: Record<string, number>
  committedRevisions?: Record<string, number>
  sourceLabels?: Record<string, string>
  reviewAnchors?: Record<string, Array<{id:string;statement:string;anchor?:{status:string;start_utf16:number;end_utf16:number;quote_markdown:string;working_copy_revision:number}|null}>>
}>(), {
  projectId: 'p1',
  chapterWritePermissionReasons: () => ({}),
  confirmed: () => ({}),
  baselines: () => ({}),
  committedRevisions: () => ({}),
  sourceLabels: () => ({}),
  reviewAnchors: () => ({}),
  saveChapter: async () => ({ ok: true, newWorkingCopyRevision: 1 }),
})

const emit = defineEmits<{
  (e: 'saved-all', ok: boolean): void
  (e: 'chapter-dirty', chapterId: string): void
  (e: 'active-chapter', chapterId: string): void
  (e: 'selection-context'): void
  (e: 'source-open', sourceId: string): void
  (e: 'review-open', chapterId: string, itemId: string): void
  (e: 'autosaved-clean'): void
}>()

/* ---------------- 自定义节点 ---------------- */
const SourceRef = SharedSourceRef.extend({
  renderHTML({ HTMLAttributes }) {
    const id = String(HTMLAttributes.id ?? '')
    const label = props.sourceLabels[id] || '来源'
    return ['span', { ...HTMLAttributes, 'data-source-ref': id, title: '点击回看原文', class: 'ld-source' }, `[${label}]`]
  },
})
// Private and synchronous: only synchronizeServer can temporarily authorize hydration.
let applyingExternal = false
const scopeBlockedReason = ref('')

/* ---------------- 编辑器 ---------------- */
function topLevelRange(doc: import('@tiptap/pm/model').Node, from: number, to: number): { from: number; to: number } {
  const $from = doc.resolve(from)
  const $to = doc.resolve(Math.max(to - 1, from))
  return { from: $from.depth === 0 ? 0 : $from.index(0), to: $to.depth === 0 ? doc.childCount - 1 : $to.index(0) }
}

// 章保护：ProseMirror 插件 filterTransaction（v3 的 coreExtensionOptions.delete.filterTransaction 只管删除）。
// 双检：① 删除步骤在"删除前文档"中的范围若覆盖章标题 → 拒绝；② 删除后文档章标题集合/顺序变化 → 拒绝；
// ③ 改写类事务的选区（新文档坐标）跨章 → 拒绝。复制/纯选区事务不受影响。
const guardPlugin = new Plugin({
  key: new PluginKey('ld-chapter-guard'),
  filterTransaction(tr, state) {
    if (!tr.docChanged || applyingExternal) return true
    const permission = checkChapterWriteTransaction(tr, props.chapterWritePermissions)
    if (!permission.allowed) {
      scopeBlockedReason.value = permission.reason === 'readonly'
        ? chapterReadonlyReason(permission.chapterId, props.chapterWritePermissions, props.chapterWritePermissionReasons) || '当前章节只读。'
        : '章标题与边界受保护，请在章节正文内编辑。'
      return false
    }
    scopeBlockedReason.value = ''
    const beforeJson = state.doc.toJSON() as PMNode
    const afterJson = tr.doc.toJSON() as PMNode
    const titlesBefore = chapterTitleNodes(beforeJson).map(t => t.chapterId).join('|')
    const titlesAfter = chapterTitleNodes(afterJson).map(t => t.chapterId).join('|')
    if (titlesBefore !== titlesAfter) return false
    // ① 用删除前坐标检查每一步的删除范围是否覆盖章标题节点
    for (const step of tr.steps) {
      const s = step as unknown as { from?: number; to?: number }
      if (typeof s.from === 'number' && typeof s.to === 'number' && s.to > s.from) {
        const $a = state.doc.resolve(s.from)
        const $b = state.doc.resolve(Math.min(s.to, state.doc.content.size))
        const ia = $a.depth === 0 ? 0 : $a.index(0)
        const ib = $b.depth === 0 ? state.doc.childCount : $b.index(0)
        const hitTitle = chapterTitleNodes(beforeJson).some(t => t.index >= ia && t.index <= ib)
        if (hitTitle) return false
      }
    }
    // ③ 跨章改写
    const { from, to } = topLevelRange(tr.doc, tr.selection.from, tr.selection.to)
    const interior = chapterTitleNodes(afterJson).filter(t => t.index > from && t.index < to).length
    return interior === 0
  },
})

const ChapterGuard = Extension.create({
  name: 'ldChapterGuard',
  addProseMirrorPlugins() { return [guardPlugin] },
})

const ChapterScopeMarkers = Extension.create({
  name: 'ldChapterScopeMarkers',
  addProseMirrorPlugins() { return [new Plugin({ key: new PluginKey('ld-chapter-scope-markers'), props: { decorations(state) {
    if (props.chapterWritePermissions === undefined) return null
    const decorations: Decoration[] = []
    let chapterId: string | null = null
    state.doc.forEach((node, offset) => {
      if (node.type.name === 'chapterTitle') chapterId = String(node.attrs.chapterId || '')
      if (chapterId && !canWriteChapter(props.chapterWritePermissions, chapterId)) decorations.push(Decoration.node(offset, offset + node.nodeSize, {
        class: 'ld-chapter-readonly',
        'data-ld-readonly-chapter': chapterId,
        title: chapterReadonlyReason(chapterId, props.chapterWritePermissions, props.chapterWritePermissionReasons) || '当前章节只读。',
      }))
    })
    return DecorationSet.create(state.doc, decorations)
  } } })] },
})
const ReviewMarkers = Extension.create({
  name: 'ldReviewMarkers',
  addProseMirrorPlugins() { return [new Plugin({key:new PluginKey('ld-review-markers'),props:{decorations(state) {
    const decorations:Decoration[]=[]
    for(const [chapterId,items] of Object.entries(props.reviewAnchors)) for(const item of items) {
      const a=item.anchor
      if(!a || a.status!=='located' || a.working_copy_revision!==props.baselines[chapterId]) continue
      // Cached server anchors belong to the saved body. Hide them while local
      // text differs; exact old quote alone is insufficient around duplicates.
      const saved=props.chapters.find(c=>c.id===chapterId)?.body_markdown
      if(saved===undefined || savedMarkdownOf(state.doc,chapterId)!==canonicalize(saved)) continue
      const range=markdownRangeToPm(state.doc,chapterId,a.start_utf16,a.end_utf16,a.quote_markdown)
      if(!range) continue
      decorations.push(Decoration.inline(range.from,range.to,{class:'ld-review-highlight','data-review-id':item.id}))
      decorations.push(Decoration.widget(range.from,()=>{
        const button=document.createElement('button')
        button.type='button';button.className='ld-review-marker';button.textContent='核查'
        button.setAttribute('aria-label','查看对应待核：'+item.statement)
        button.setAttribute('data-review-id',item.id);button.setAttribute('contenteditable','false')
        button.addEventListener('click',event=>{event.preventDefault();event.stopPropagation();emit('review-open',chapterId,item.id)})
        return button
      },{side:-1,key:chapterId+':'+item.id}))
    }
    return DecorationSet.create(state.doc,decorations)
  }}})] },
})

const editor = useEditor({
  extensions: [
    StarterKit.configure({ heading: { levels: [2, 3, 4] }, blockquote: false, code: false, codeBlock: false, horizontalRule: false, strike: false, link: false, underline: false }),
    SourceRef,
    RawBlock,
    ChapterTitle,
    ChapterGuard,
    ChapterScopeMarkers,
    ReviewMarkers,
  ],
  content: chaptersToDocJson(props.chapters) as never,
  editorProps: {
    handleClick: (_view, _pos, event) => {
      const target = (event.target as HTMLElement)?.closest('[data-source-ref]') as HTMLElement | null
      if (target?.dataset.sourceRef) emit('source-open', target.dataset.sourceRef)
      return false
    },
  },
})

/* ---------------- 保存队列（各章独立 CAS，写入链版） ---------------- */
const chapterMarkdowns = new Map<string, string>()
const savedRevisions = new Map<string, number>()
const committedRevisions = new Map<string, number>()
const queueVersion = ref(0)
const queue = new ChapterSaveQueue(async (ctx, body) => {
  if (!canWriteChapter(props.chapterWritePermissions, ctx.chapterId)) {
    const error = chapterReadonlyReason(ctx.chapterId, props.chapterWritePermissions, props.chapterWritePermissionReasons) || '当前章节只读。'
    scopeBlockedReason.value = error
    return { ok: false, error }
  }
  const r = await props.saveChapter(ctx.chapterId, body, ctx.expectedWorkingCopyRevision)
  if (r.ok) {
    chapterMarkdowns.set(ctx.chapterId, body)
    savedRevisions.set(ctx.chapterId, r.newWorkingCopyRevision ?? ctx.expectedWorkingCopyRevision + 1)
  }
  return r
}, 900)
queue.onChange(() => { queueVersion.value++ })

const allIds = computed(() => props.chapters.map(c => c.id))
const saveLabel = ref('已保存')
const blockReasons = ref<Record<string, string | null>>({})

// 风险4：确认基线取真实正式版本。committedRevision 来自"提交正式版本"（prop），不由确认记录自证。
function chapterBlockReason(chapterId: string): string | null {
  const s = queue.state(chapterId)
  return draftBlockReason({
    dirty: s === 'dirty' || s === 'saving' || s === 'error',
    conflict: s === 'conflict',
    savedRevision: savedRevisions.get(chapterId) ?? props.baselines[chapterId] ?? 0,
    committedRevision: committedRevisions.get(chapterId) ?? props.baselines[chapterId] ?? 0,
  })
}

function refreshLabels() {
  const s = queue.overallState(allIds.value)
  saveLabel.value = { clean: '已保存', dirty: '有待保存修改', saving: '保存中…', error: '保存失败，输入保留', conflict: '版本冲突，输入保留' }[s]
  const next: Record<string, string | null> = {}
  for (const ch of props.chapters) next[ch.id] = chapterBlockReason(ch.id)
  blockReasons.value = next
}

onMounted(() => {
  for (const ch of props.chapters) {
    queue.setBaseline(ch.id, props.baselines[ch.id] ?? 0)
    savedRevisions.set(ch.id, props.baselines[ch.id] ?? 0)
    committedRevisions.set(ch.id, props.committedRevisions[ch.id] ?? props.baselines[ch.id] ?? 0)
    // 风险6：基线用"规范化序列化"结果，未编辑章（字节差异仅归一化）不会被误标脏
    chapterMarkdowns.set(ch.id, canonicalize(ch.body_markdown))
  }
  queue.onChange(refreshLabels)
  refreshLabels()
})

function canonicalize(md: string): string {
  return chapterToMarkdown(parseChapterMarkdown(md))
}

/* ---------------- 编辑 → 各章 dirty（同步标脏，无 300ms 盲区） ---------------- */
function synchronizeServer(chapters: ChapterSpec[], baselines: Record<string, number>, committed: Record<string, number>): boolean {
  const ed = editor.value
  if (!ed || hasUnsaved.value) return false
  const next = ed.schema.nodeFromJSON(chaptersToDocJson(chapters))
  const contentChanged = !ed.state.doc.eq(next)
  applyingExternal = true
  try {
    // Metadata-only readback must not replace the document or move the caret.
    // In particular, clicking a chapter triggers a GET that often returns the
    // same content; dispatching replaceWith then moves input into the last chapter.
    if (contentChanged) ed.view.dispatch(ed.state.tr.replaceWith(0, ed.state.doc.content.size, next.content).setMeta('addToHistory', false))
    for (const ch of chapters) {
      queue.setBaseline(ch.id, baselines[ch.id])
      savedRevisions.set(ch.id, baselines[ch.id])
      committedRevisions.set(ch.id, committed[ch.id] ?? 0)
      chapterMarkdowns.set(ch.id, canonicalize(ch.body_markdown))
    }
  } finally { applyingExternal = false }
  if (contentChanged) {
    bodyVersion.value++
    candidate.value=null
  }
  refreshLabels()
  emit('selection-context')
  return true
}
function focusChapter(chapterId: string) {
  const ed = editor.value
  if (!ed) return false
  let position: number | null = null
  ed.state.doc.forEach((node, offset) => {
    if (node.type.name === 'chapterTitle' && node.attrs.chapterId === chapterId) position = offset + node.nodeSize + 1
  })
  if (position === null) return false
  ed.chain().focus().setTextSelection(Math.min(position, ed.state.doc.content.size - 1)).scrollIntoView().run()
  emit('active-chapter', chapterId)
  return true
}
function currentParagraphSources(): string[] {
  const ed = editor.value
  if (!ed) return []
  const sel = ed.state.selection
  if (!sel.$from.sameParent(sel.$to) || !sel.$from.parent.isTextblock) return []
  const ids = new Set<string>()
  sel.$from.parent.descendants(node => { if (node.type.name === 'sourceRef') ids.add(String(node.attrs.id)); return true })
  return [...ids]
}
function locateMarkdownRange(chapterId: string, from: number, to: number, quote: string): boolean {
  const ed=editor.value
  if(!ed || queue.state(chapterId)!=='clean') return false
  const baseline=props.chapters.find(c=>c.id===chapterId)?.body_markdown
  if(baseline===undefined || savedMarkdownOf(ed.state.doc,chapterId)!==canonicalize(baseline)) return false
  const range=markdownRangeToPm(ed.state.doc,chapterId,from,to,quote)
  return !!range && ed.chain().focus().setTextSelection(range).scrollIntoView().run()
}
watch(() => props.reviewAnchors,()=>{const ed=editor.value;if(ed)ed.view.dispatch(ed.state.tr.setMeta('review-markers-refresh',true))},{deep:true})
watch(() => [props.chapterWritePermissions, props.chapterWritePermissionReasons], () => {
  const ed = editor.value
  if (ed) ed.view.dispatch(ed.state.tr.setMeta('chapter-scope-refresh', true))
}, { deep: true })

function collectChanges(): string[] {
  if (applyingExternal || !editor.value) return []
  const json = editor.value.getJSON() as unknown as PMNode
  const changed: string[] = []
  for (const p of docJsonToChapters(json)) {
    if (p.markdown !== chapterMarkdowns.get(p.chapterId)) {
      queue.markDirty(p.chapterId, p.markdown)
      emit('chapter-dirty', p.chapterId)
      changed.push(p.chapterId)
    }
  }
  return changed
}

function onUpdate() {
  if (applyingExternal) return
  emit('selection-context')
  bodyVersion.value++
  collectChanges()
  refreshLabels()
}

// Ctrl+S：先同步采集当前文档（取消任何在途去抖），再冲刷并返回可 await 结果
function flushSave(): Promise<{ ok: boolean }> {
  collectChanges()
  return queue.flushAll().then(r => { emit('saved-all', r.ok); return { ok: r.ok } })
}

function onKeydown(e: KeyboardEvent) {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
    e.preventDefault()
    void flushSave()
  }
}

onMounted(() => {
  document.addEventListener('keydown', onKeydown)
  editor.value?.on('update', onUpdate)
  editor.value?.on('selectionUpdate', () => {
    const ch = selectionChapter.value
    if (ch) emit('active-chapter', ch)
    emit('selection-context')
  })
})
// 父级基线/提交基线到达时同步进队列（setBaseline 不降级）
watch(() => props.baselines, (next) => {
  for (const [id, rev] of Object.entries(next)) queue.setBaseline(id, rev)
  refreshLabels()
}, { deep: true })
watch(() => props.committedRevisions, (next) => {
  for (const [id, rev] of Object.entries(next)) committedRevisions.set(id, rev)
  refreshLabels()
}, { deep: true })
// 自动保存转 clean 也同步父状态（不只 Ctrl+S）
queue.onChange(() => {
  if (!hasUnsaved.value) emit('autosaved-clean')
})
onBeforeUnmount(() => {
  document.removeEventListener('keydown', onKeydown)
  queue.dispose()
})

/* ---------------- 格式工具 ---------------- */
const chain = () => editor.value?.chain().focus()
function setHeading(level: 2 | 3 | 4) { chain()?.setHeading({ level }).run() }
function toggleBold() { chain()?.toggleBold().run() }
function toggleItalic() { chain()?.toggleItalic().run() }
function toggleBullet() { chain()?.toggleBulletList().run() }
function toggleOrdered() { chain()?.toggleOrderedList().run() }
function undo() { editor.value?.commands.undo() }
function redo() { editor.value?.commands.redo() }
const canUndo = computed(() => editor.value?.can().undo() ?? false)
const canRedo = computed(() => editor.value?.can().redo() ?? false)

/* ---------------- 查找/替换（语义索引：跨 marks/转义，循环"下一个"） ---------------- */
const findText = ref('')
const replaceText = ref('')
const findStatus = ref('')

let findCursor = -1
function findNext() {
  const ed = editor.value
  if (!ed || !findText.value) return
  const index = buildSemanticIndex(ed.state.doc as never)
  const hits = findSemanticHits(index, findText.value)
  if (!hits.length) { findStatus.value = '未找到'; return }
  findCursor = (findCursor + 1) % hits.length
  const hit = hits[findCursor]
  const ch = index.find(c => c.chapterId === hit.chapterId)!
  const md = semanticRangeToMd(ch, hit.from, hit.to)
  if (!md) { findStatus.value = '命中无法安全映射'; return }
  const pmFrom = mdOffsetToPmPos(ed.state.doc as never, hit.chapterId, md.mdStart)
  const pmTo = mdOffsetToPmPos(ed.state.doc as never, hit.chapterId, md.mdEnd)
  if (pmFrom === null || pmTo === null) { findStatus.value = '命中无法安全映射'; return }
  ed.commands.setTextSelection({ from: pmFrom, to: pmTo })
  ed.commands.scrollIntoView()
  findStatus.value = `已选中第 ${findCursor + 1}/${hits.length} 处`
}
function replaceOne() {
  const ed = editor.value
  if (!ed || !findText.value) return
  if (!selectionWritable.value) { findStatus.value = selectionReadonlyReason.value || '当前章节只读。'; return }
  const { from, to } = ed.state.selection
  const sel = ed.state.doc.textBetween(from, to)
  if (sel !== findText.value) { findStatus.value = '当前选区不是查找词，先查找'; return }
  const before = ed.state.doc
  chain()?.insertContentAt({ from, to }, replaceText.value).run()
  findCursor = -1
  findStatus.value = ed.state.doc.eq(before) ? '未发生正文变更；请检查章节权限与章边界。' : '已替换 1 处'
}
function replaceAll() {
  const ed = editor.value
  if (!ed || !findText.value) return
  const index = buildSemanticIndex(ed.state.doc as never)
  const hits = findSemanticHits(index, findText.value)
  if (!hits.length) { findStatus.value = '未找到'; return }
  if (hits.some(h => h.chapterId !== hits[0].chapterId)) {
    findStatus.value = '替换被章边界保护拒绝（命中跨章），请分章替换'
    return
  }
  if (!canWriteChapter(props.chapterWritePermissions, hits[0].chapterId)) {
    findStatus.value = chapterReadonlyReason(hits[0].chapterId, props.chapterWritePermissions, props.chapterWritePermissionReasons) || '当前章节只读。'
    return
  }
  const ch = index.find(c => c.chapterId === hits[0].chapterId)!
  const pmHits = hits.map(h => {
    const md = semanticRangeToMd(ch, h.from, h.to)
    if (!md) return null
    return {
      from: mdOffsetToPmPos(ed.state.doc as never, ch.chapterId, md.mdStart),
      to: mdOffsetToPmPos(ed.state.doc as never, ch.chapterId, md.mdEnd),
    }
  })
  if (pmHits.some(h => !h || h.from === null || h.to === null)) { findStatus.value = '命中无法安全映射'; return }
  const tr = ed.state.tr
  for (const h of [...pmHits].reverse() as Array<{ from: number; to: number }>) tr.insertText(replaceText.value, h.from, h.to)
  const before = ed.state.doc
  ed.view.dispatch(tr)
  findCursor = -1
  findStatus.value = ed.state.doc.eq(before) ? '未发生正文变更；请检查章节权限与章边界。' : `已替换 ${hits.length} 处`
}

/* ---------------- 选段候选（反例4防线） ---------------- */
const candidate = ref<Candidate | null>(null)
const bodyVersion = ref(0)
// 选区两端必须在同一章；跨章选择直接拒绝生成候选
function selectionChapterPair(): { from: string | null; to: string | null } {
  const ed = editor.value
  if (!ed) return { from: null, to: null }
  const sel = ed.state.selection
  const idxOf = ($p: { depth: number; index: (d: number) => number }) => $p.depth === 0 ? 0 : $p.index(0)
  const json = ed.getJSON() as unknown as PMNode
  const titles = chapterTitleNodes(json)
  const chapterOf = (idx: number) => {
    let cur: string | null = null
    for (const t of titles) { if (t.index <= idx) cur = t.chapterId; else break }
    return cur
  }
  return { from: chapterOf(idxOf(sel.$from)), to: chapterOf(idxOf(sel.$to)) }
}
const selectionChapter = computed(() => {
  const p = selectionChapterPair()
  return p.from && p.from === p.to ? p.from : null
})
const selectionWritable = computed(() => canWriteChapter(props.chapterWritePermissions, selectionChapter.value))
const selectionReadonlyReason = computed(() => chapterReadonlyReason(selectionChapter.value, props.chapterWritePermissions, props.chapterWritePermissionReasons))
const readonlySummary = computed(() => {
  if (props.chapterWritePermissions === undefined) return ''
  const count = props.chapters.filter(chapter => !canWriteChapter(props.chapterWritePermissions, chapter.id)).length
  return count ? (count === props.chapters.length ? '文稿只读：仍可选取文字、查找和回看核查标记。' : `${count} 个章节只读，其余已授权章节可编辑。`) : ''
})
const ctxNow = (): EditorContext => ({
  projectId: props.projectId,
  chapterId: selectionChapter.value ?? props.chapters[0]?.id ?? '',
  bodyVersion: bodyVersion.value,
})
const selectionText = computed(() => {
  const ed = editor.value
  if (!ed || ed.state.selection.empty || !selectionChapter.value) return ''
  return ed.state.doc.textBetween(ed.state.selection.from, ed.state.selection.to, ' ')
})
function buildCandidate() {
  if (!selectionChapter.value) { candidate.value = null; return } // 跨章选择：拒绝
  const v = validateCandidate({ ctx: ctxNow(), quote: selectionText.value, createdAt: Date.now() }, ctxNow())
  if (!v.ok) { candidate.value = null; return }
  candidate.value = { ctx: ctxNow(), quote: selectionText.value, createdAt: Date.now() }
}
const candidateValid = computed(() => validateCandidate(candidate.value, ctxNow()))

// 选段改写前置：PM 选区 → 该章 markdown UTF-16 区间。
// 判据 = pmSerialize 产出的"实际保存正文"（与 docJsonToChapters 同源）；三重验证：
// ① quote === 保存正文 slice；② quote 语义文本 === 用户选中的语义内容；③ quote 在保存正文中唯一。
function selectionToUtf16() {
  const ed = editor.value
  if (!ed) return { ok: false as const, reason: 'no-editor' }
  const ch = selectionChapter.value
  if (!ch) return { ok: false as const, reason: 'cross-chapter-selection' }
  const sel = ed.state.selection
  const actual = docJsonToChapters(ed.getJSON() as PMNode).find(c => c.chapterId === ch)?.markdown
  const mappedBody = savedMarkdownOf(ed.state.doc as never, ch)
  if (actual !== mappedBody) return { ok: false as const, reason: 'serialization-mismatch' }
  const r = pmRangeToUtf16(ed.state.doc as never, ch, sel.from, sel.to)
  if (!r.ok) return { ok: false as const, reason: r.reason }
  const saved = savedMarkdownOf(ed.state.doc as never, ch)
  if (saved === null) return { ok: false as const, reason: 'no-chapter' }
  if (saved.slice(r.start_utf16, r.end_utf16) !== r.quote) return { ok: false as const, reason: 'slice-mismatch' }
  const selectedPlain = ed.state.doc.textBetween(sel.from, sel.to, ' ')
  // 语义判据：quote 作为行内 markdown 解析后的纯文本 === 用户选中的纯文本（剥离 marks/转义/实体）
  const quotePlain = (q: string) => plainOf(parseInlineText(q))
  const countOcc = (q: string) => { let occ = 0; let at = 0; for (;;) { const i = saved.indexOf(q, at); if (i < 0) break; occ++; at = i + 1 } return occ }
  let quote = r.quote
  let s0 = r.start_utf16
  let e0 = r.end_utf16
  if (quotePlain(quote) !== selectedPlain) {
    // 选区恰好覆盖 mark 包裹文本时，md 切片边界落在 marks 内侧（如 '**工程'）。
    // 有界小搜索：前后各扩 ≤4 字符，取"语义===选中内容 + 唯一出现"的最小扩展，不猜。
    let best: { cs: number; ce: number } | null = null
    for (let ds = 0; ds <= 4; ds++) {
      for (let de = 0; de <= 4; de++) {
        const cs = s0 - ds
        const ce = e0 + de
        if (cs < 0 || ce > saved.length) continue
        const cand = saved.slice(cs, ce)
        if (quotePlain(cand) === selectedPlain && countOcc(cand) === 1) {
          if (!best || (ds + de) < (s0 - best.cs) + (best.ce - e0)) best = { cs, ce }
        }
      }
    }
    if (best) { quote = saved.slice(best.cs, best.ce); s0 = best.cs; e0 = best.ce }
  }
  if (quotePlain(quote) !== selectedPlain) return { ok: false as const, reason: 'selection-mismatch' }
  if (countOcc(quote) !== 1) return { ok: false as const, reason: 'ambiguous-quote' }
  return { ok: true as const, chapterId: ch, start_utf16: s0, end_utf16: e0, quote }
}

const hasUnsaved = computed(() => {
  void queueVersion.value
  return allIds.value.some(id => {
  const s = queue.state(id)
  return s === 'dirty' || s === 'saving' || s === 'error' || s === 'conflict'
  })
})
function currentMarkdown(chapterId: string): string | null {
  return editor.value ? docJsonToChapters(editor.value.getJSON() as PMNode).find(c => c.chapterId === chapterId)?.markdown ?? null : null
}

async function pauseForNavigation() {
  editor.value?.setEditable(false)
  await queue.pauseAndWait()
}
defineExpose({ pauseForNavigation, flushSave, queue, editor, buildCandidate, candidate, candidateValid, blockReasons, selectionToUtf16, collectChanges, hasUnsaved, currentMarkdown, synchronizeServer, focusChapter, currentParagraphSources, locateMarkdownRange })

const showFind = ref(false)
</script>

<template>
  <div class="ld-editor" data-testid="ld-editor">
    <div class="ld-toolbar" role="toolbar" aria-label="格式工具">
      <button type="button" @click="undo" :disabled="!canUndo" title="撤销">↩</button>
      <button type="button" @click="redo" :disabled="!canRedo" title="重做">↪</button>
      <span class="ld-sep"></span>
      <button type="button" @click="setHeading(2)" :disabled="!selectionWritable" :title="selectionReadonlyReason || undefined">H2</button>
      <button type="button" @click="setHeading(3)" :disabled="!selectionWritable" :title="selectionReadonlyReason || undefined">H3</button>
      <button type="button" @click="setHeading(4)" :disabled="!selectionWritable" :title="selectionReadonlyReason || undefined">H4</button>
      <button type="button" @click="toggleBold" :disabled="!selectionWritable" :title="selectionReadonlyReason || undefined"><strong>B</strong></button>
      <button type="button" @click="toggleItalic" :disabled="!selectionWritable" :title="selectionReadonlyReason || undefined"><em>I</em></button>
      <button type="button" @click="toggleBullet" :disabled="!selectionWritable" :title="selectionReadonlyReason || undefined">• 列表</button>
      <button type="button" @click="toggleOrdered" :disabled="!selectionWritable" :title="selectionReadonlyReason || undefined">1. 列表</button>
      <span class="ld-sep"></span>
      <button type="button" @click="showFind = !showFind" :aria-pressed="showFind">查找/替换</button>
      <span class="ld-save" role="status">{{ saveLabel }}</span>
    </div>
    <div v-if="showFind" class="ld-find">
      <input v-model="findText" placeholder="查找" aria-label="查找" @keyup.enter="findNext">
      <input v-model="replaceText" placeholder="替换为" aria-label="替换为">
      <button type="button" @click="findNext">下一个</button>
      <button type="button" @click="replaceOne" :disabled="!selectionWritable">替换</button>
      <button type="button" @click="replaceAll">全部替换</button>
      <span class="muted">{{ findStatus }}</span>
    </div>
    <p v-if="readonlySummary" class="ld-readonly-summary" role="status">{{ readonlySummary }}</p>
    <p v-if="selectionReadonlyReason || scopeBlockedReason" class="ld-readonly-reason" role="status">{{ scopeBlockedReason || selectionReadonlyReason }}</p>
    <EditorContent :editor="editor" class="ld-paper" />
    <p v-if="candidate && !candidateValid.ok" class="ld-dead" role="status">
      改写候选已失效（{{ candidateValid.reason }}）：请重新选择当前章文本后再改写。
    </p>
  </div>
</template>

<style>
.ld-editor { display: grid; gap: 8px }
.ld-toolbar { display: flex; gap: 4px; align-items: center; flex-wrap: wrap; font-family: system-ui, sans-serif }
.ld-toolbar button { border: 1px solid #cfd3c8; background: #fff; border-radius: 7px; padding: 4px 10px; font-size: 13px }
.ld-toolbar button:disabled { opacity: .4 }
.ld-sep { width: 1px; height: 18px; background: #dbe2d8; margin: 0 4px }
.ld-save { margin-left: auto; font-size: 12.5px; color: #4a5148 }
.ld-find { display: flex; gap: 6px; align-items: center; font-family: system-ui, sans-serif }
.ld-find input { border: 1px solid #cfd3c8; border-radius: 7px; padding: 4px 9px; font-size: 13px }
.ld-paper .tiptap { background: #fff; border: 1px solid #dbe2d8; border-radius: 8px; padding: 40px 52px; min-height: 50vh; outline: none; font: 15px/1.85 "Songti SC", "SimSun", Georgia, serif }
.ld-paper .tiptap p { margin: 0 0 12px; text-indent: 2em }
.ld-paper .tiptap h2.ld-chapter-title { font-size: 19px; border-bottom: 1px solid #dbe2d8; padding-bottom: 6px; margin: 26px 0 14px }
.ld-paper .tiptap h2.ld-chapter-title:first-child { margin-top: 0 }
.ld-review-highlight { background:#fff5d9; border-bottom:1px dashed #c18a32; }
.ld-review-marker { display:inline-flex; font:11px/1.5 system-ui,sans-serif; text-indent:0; vertical-align:baseline; padding:1px 5px; margin:0 4px 0 0; border:1px solid #d6b169; border-radius:4px; background:#fff8e8; color:#77551c; cursor:pointer; user-select:none; }
.ld-source { color: #276555; font-size: .85em; border: 1px dashed #9cc4b6; border-radius: 4px; padding: 0 3px; font-family: system-ui, sans-serif }
.ld-raw { background: #f6f7f3; border: 1px solid #e2e4dc; border-radius: 6px; padding: 8px 10px; font-size: 13px; white-space: pre-wrap; font-family: ui-monospace, Consolas, monospace }
.ld-readonly-summary, .ld-readonly-reason { margin: 0; padding: 7px 10px; border-radius: 6px; background: #f2f5f1; color: #596257; font: 12.5px/1.6 system-ui, sans-serif }
.ld-readonly-reason { background: #fff7e7; color: #725925 }
.ld-paper .tiptap .ld-chapter-readonly { cursor: text }
.ld-paper .tiptap h2.ld-chapter-title.ld-chapter-readonly::after { content: '只读'; margin-left: 10px; padding: 2px 6px; border-radius: 4px; background: #eef1eb; color: #66715f; font: 11px/1.4 system-ui, sans-serif }
.ld-dead { color: #5b3e91; font-size: 12.5px; font-family: system-ui, sans-serif; margin: 0 }
</style>
