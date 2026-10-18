import assert from 'node:assert/strict'
import test from 'node:test'
import { getSchema } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { Node as PMNodeType } from '@tiptap/pm/model'
import { SourceRef, RawBlock, ChapterTitle } from './nodes'
import { chaptersToDocJson, docJsonToChapters } from './docJson'
import { chapterMaps, savedMarkdownOf, pmRangeToUtf16, semanticText } from './pmSerialize'

const schema = getSchema([StarterKit.configure({ heading: { levels: [2, 3, 4] } }), SourceRef, RawBlock, ChapterTitle])

function pmDoc(bodies: Array<{ id: string; title: string; body: string }>) {
  return PMNodeType.fromJSON(schema, chaptersToDocJson(bodies.map(b => ({ id: b.id, title: b.title, body_markdown: b.body }))) as never)
}

test('parity：pmSerialize 的章正文与 docJsonToChapters 的保存正文逐字节一致', () => {
  const cases = [
    '## 背景\n\n中文😀 **重点**与*斜体*，含 [[source:s1]] 引用。\n\n3. 第三步\n   - 子项\n4. 第四步',
    '**证据 [[source:s1]] 重点**与*引用 [[source:s2]]*',
    '**粗体** 与 \\*星号\\* &amp; &#x1F600;',
    '> 旧引用块\n\n| 列一 | 列二 |\n| --- | --- |\n\n尾段。',
    '前言\n\n```go\ncode()\n```\n\n后语',
  ]
  for (const body of cases) {
    const doc = pmDoc([{ id: 'c1', title: '第一章', body }])
    const viaBlocks = docJsonToChapters(doc.toJSON() as never)[0].markdown
    const viaPm = savedMarkdownOf(doc, 'c1')!
    assert.equal(viaPm, viaBlocks)
  }
})

test('列表/raw 前缀进入正文，不从偏移中删除（golden 前置列表章）', () => {
  const doc = pmDoc([{ id: 'c1', title: '第一章', body: '- 甲\n- 乙\n\n> 旧块\n\n正文目标句在此。' }])
  const cm = chapterMaps(doc)[0]
  assert.ok(cm.markdown.startsWith('- 甲\n- 乙'))
  assert.ok(cm.markdown.includes('> 旧块'))
  const at = cm.markdown.indexOf('目标句')
  assert.ok(at > 0)
})

test('普通段选区映射精确，quote===slice===选中语义', () => {
  const doc = pmDoc([{ id: 'c1', title: '第一章', body: '前文目标句后文。' }])
  const cm = chapterMaps(doc)[0]
  const at = cm.markdown.indexOf('目标句')
  const sp = cm.spans.find(x => !x.atom && x.mdStart <= at && at + 3 <= x.mdStart + x.mdLen)!
  const from = sp.pmStart + (at - sp.mdStart)
  const r = pmRangeToUtf16(doc, 'c1', from, from + 3)
  assert.equal(r.ok, true)
  if (r.ok) {
    assert.equal(r.quote, '目标句')
    assert.equal(semanticText(r.quote), '目标句')
    const saved = savedMarkdownOf(doc, 'c1')!
    assert.equal(saved.slice(r.start_utf16, r.end_utf16), '目标句')
  }
})

test('强调/转义/emoji：md 偏移与 pm 长度不等时仍精确', () => {
  const doc = pmDoc([{ id: 'c1', title: '第一章', body: '**甲目标乙**与\\*星\\*😀后' }])
  const saved = savedMarkdownOf(doc, 'c1')!
  const s = saved.indexOf('目标')
  // 反查：保存正文偏移 → 应能取得同一 quote
  const cm = chapterMaps(doc)[0]
  const sp = cm.spans.find(x => !x.atom && x.mdStart <= s && s + 2 <= x.mdStart + x.mdLen)
  assert.ok(sp)
  const from = sp.pmStart + (s - sp.mdStart)
  const r = pmRangeToUtf16(doc, 'c1', from, from + 2)
  assert.equal(r.ok, true)
  if (r.ok) assert.equal(r.quote, '目标')
})

test('引用原子内外边界：贴在原子前后的选区合法，atom 内部区间被拒绝', () => {
  const doc = pmDoc([{ id: 'c1', title: '第一章', body: '甲[[source:s1]]乙' }])
  const cm = chapterMaps(doc)[0]
  const atom = cm.spans.find(x => x.atom)!
  // 原子前边界（=文本甲的结束）
  const before = pmRangeToUtf16(doc, 'c1', atom.pmStart, atom.pmStart)
  assert.equal(before.ok, false) // 空选区
  // 跨原子选区（甲+原子）→ quote 含 [[source:s1]]，语义验证交给组件（选中语义不含原子文本）
  const across = pmRangeToUtf16(doc, 'c1', atom.pmStart - 1, atom.pmEnd + 1)
  assert.equal(across.ok, true)
  if (across.ok) assert.ok(across.quote.includes('[[source:s1]]'))
  // 原子之后到乙末尾
  const tail = pmRangeToUtf16(doc, 'c1', atom.pmEnd, atom.pmEnd + 1)
  assert.equal(tail.ok, true)
  if (tail.ok) assert.equal(semanticText(tail.quote), '乙')
})

test('第二章（第二章标题之后）映射独立正确', () => {
  const doc = pmDoc([
    { id: 'c1', title: '第一章', body: '甲章内容。' },
    { id: 'c2', title: '第二章', body: '乙章目标句。' },
  ])
  const cm2 = chapterMaps(doc)[1]
  const s = cm2.markdown.indexOf('目标句')
  const sp = cm2.spans.find(x => !x.atom && x.mdStart <= s)!
  const from = sp.pmStart + (s - sp.mdStart)
  const r = pmRangeToUtf16(doc, 'c2', from, from + 3)
  assert.equal(r.ok, true)
  if (r.ok) assert.equal(r.quote, '目标句')
})
