import assert from 'node:assert/strict'
import test from 'node:test'
import { getSchema } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { EditorState, Plugin, TextSelection } from '@tiptap/pm/state'
import { history, undo, redo, closeHistory } from '@tiptap/pm/history'
import { SourceRef, RawBlock, ChapterTitle } from './nodes'
import { chaptersToDocJson } from './docJson'
import { canWriteChapter, chapterReadonlyReason, checkChapterWriteTransaction, type ChapterWritePermissions } from './editorScope'

const schema = getSchema([StarterKit.configure({ heading: { levels: [2, 3, 4] } }), SourceRef, RawBlock, ChapterTitle])
function document(bodies = ['可编辑章节', '只读章节']) {
  return schema.nodeFromJSON(chaptersToDocJson(bodies.map((body_markdown, index) => ({ id: `c${index + 1}`, title: `第${index + 1}章`, body_markdown }))))
}
function position(doc: ReturnType<typeof document>, id: string) {
  let current = '', found = -1
  doc.forEach((node, offset) => {
    if (node.type.name === 'chapterTitle') current = node.attrs.chapterId
    else if (current === id && found < 0) found = offset + 1
  })
  assert(found > 0); return found
}
function state(permissions: ChapterWritePermissions = { c1: true, c2: false }, doc = document()) {
  return EditorState.create({ schema, doc, plugins: [new Plugin({ filterTransaction: transaction => checkChapterWriteTransaction(transaction, permissions).allowed })] })
}

test('only undefined retains legacy compatibility; absent, false and inherited entries never authorize', () => {
  assert.equal(canWriteChapter(undefined, 'c2'), true)
  assert.equal(canWriteChapter({}, 'c2'), false)
  assert.equal(canWriteChapter({ c1: true }, 'c2'), false)
  assert.equal(canWriteChapter(Object.create({ c2: true }), 'c2'), false)
  assert.equal(canWriteChapter({ c2: false }, 'c2'), false)
  assert.equal(canWriteChapter({ c2: true }, 'c2'), true)
  assert.equal(chapterReadonlyReason('c2', {}, { c2: 'Observer 只能阅读此章' }), 'Observer 只能阅读此章')
})
test('read-only selection, caret navigation and metadata-only anchor refresh remain usable', () => {
  let s = state({})
  const at = position(s.doc, 'c2')
  s = s.apply(s.tr.setSelection(TextSelection.create(s.doc, at, at + 2)).setMeta('review-markers-refresh', true))
  assert.equal(s.selection.from, at); assert.equal(s.selection.to, at + 2)
})
test('a body edit in the explicitly writable chapter succeeds without touching the readonly chapter', () => {
  const s = state(), next = s.apply(s.tr.insertText('新', position(s.doc, 'c1')))
  assert.notEqual(next.doc, s.doc); assert.match(next.doc.textContent, /新可编辑章节/)
})
test('a read-only chapter edit is rejected even with the final selection in the writable chapter', () => {
  const s = state(), tr = s.tr.insertText('越权', position(s.doc, 'c2'))
  tr.setSelection(TextSelection.create(tr.doc, position(tr.doc, 'c1')))
  assert.equal(s.apply(tr).doc, s.doc)
})
test('one transaction touching writable and readonly bodies is rejected atomically', () => {
  const s = state(), tr = s.tr.insertText('只读修改', position(s.doc, 'c2')).insertText('合法修改', position(s.doc, 'c1'))
  assert.equal(s.apply(tr).doc, s.doc)
})
test('formatting with unchanged text is still a forbidden readonly-body modification', () => {
  const s = state(), from = position(s.doc, 'c2'), tr = s.tr.addMark(from, from + 2, schema.marks.bold.create())
  assert.equal(tr.doc.textContent, s.doc.textContent); assert.equal(s.apply(tr).doc, s.doc)
})
test('source-reference and raw-block atom attributes cannot bypass readonly protection', () => {
  const s = state({ c1: true }, document(['授权', '甲[[source:original]]乙\n\n> 原始块']))
  const atoms: number[] = []
  s.doc.descendants((node, pos) => { if (node.type.name === 'sourceRef' || node.type.name === 'rawBlock') atoms.push(pos) })
  assert.equal(atoms.length, 2)
  for (const at of atoms) {
    const node = s.doc.nodeAt(at)!, attrs = { ...node.attrs, ...(node.type.name === 'sourceRef' ? { id: 'different' } : { text: '> different' }) }
    assert.equal(s.apply(s.tr.setNodeMarkup(at, undefined, attrs)).doc, s.doc)
  }
})
test('an intermediate readonly write is rejected even if a later step restores the final document', () => {
  const s = state(), at = position(s.doc, 'c2'), tr = s.tr.insertText('X', at).delete(at, at + 1)
  assert(tr.doc.eq(s.doc)); assert.equal(checkChapterWriteTransaction(tr, { c1: true }).allowed, false)
})
test('size changes in a preceding authorized chapter do not deny unchanged following readonly nodes', () => {
  const s = state(), tr = s.tr.insertText('很长的新正文😀', position(s.doc, 'c1'))
  assert.equal(checkChapterWriteTransaction(tr, { c1: true }).allowed, true)
  assert.notEqual(s.apply(tr).doc, s.doc)
})
test('arbitrary external snapshot metadata does not authorize document replacement', () => {
  const s = state({}), tr = s.tr.replaceWith(0, s.doc.content.size, document(['非法重载', '非法重载']).content)
  tr.setMeta('ld-authorized-external-snapshot', true).setMeta('ld-authorized-external-snapshot$', true).setMeta('applyingExternal', true)
  assert.equal(s.apply(tr).doc, s.doc)
})
test('authorized undo/redo works; revoking access rejects undo without changing the saved history document', () => {
  let permissions: ChapterWritePermissions = { c1: true, c2: false }
  let s = EditorState.create({ schema, doc: document(), plugins: [history(), new Plugin({ filterTransaction: tr => checkChapterWriteTransaction(tr, permissions).allowed })] })
  const original = s.doc
  s = s.apply(closeHistory(s.tr).insertText('编辑', position(s.doc, 'c1')))
  const changed = s.doc, dispatch = (tr: Parameters<typeof checkChapterWriteTransaction>[0]) => { s = s.apply(tr) }
  undo(s, dispatch); assert(s.doc.eq(original))
  redo(s, dispatch); assert(s.doc.eq(changed))
  permissions = {}; undo(s, dispatch); assert(s.doc.eq(changed))
  permissions = { c1: true }; undo(s, dispatch); assert(s.doc.eq(original))
})
test('malformed identity, duplicate chapter IDs, title replacement and chapter deletion are rejected', () => {
  const s = state({ c1: true, c2: true })
  let second = -1; s.doc.forEach((node, offset) => { if (node.attrs.chapterId === 'c2') second = offset })
  assert.equal(s.apply(s.tr.setNodeMarkup(second, undefined, { chapterId: 'c1', title: '第2章' })).doc, s.doc)
  assert.equal(s.apply(s.tr.setNodeMarkup(second, undefined, { chapterId: 'c2', title: '改名' })).doc, s.doc)
  assert.equal(s.apply(s.tr.delete(second, second + 1)).doc, s.doc)
  assert.equal(s.apply(s.tr.insert(0, schema.nodes.paragraph.create())).doc, s.doc)
})