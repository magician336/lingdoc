import assert from 'node:assert/strict'
import test from 'node:test'
import { parseChapterMarkdown, chapterToMarkdown, inlineToMarkdown, decodeEntities } from './markdown'
import { chaptersToDocJson, docJsonToChapters, selectionCrossesChapter, chapterAtIndex } from './docJson'
import type { PMNode } from './docJson'

test('markdown roundtrip: headings/bold/italic/lists', () => {
  const md = '## 二级标题\n\n普通**加粗**和*斜体*段落。\n\n### 三级\n\n- 甲\n- 乙\n\n1. 一\n2. 二'
  const blocks = parseChapterMarkdown(md)
  assert.equal(chapterToMarkdown(blocks), md)
})

test('bold containing source atom keeps both (golden sources-inside-emphasis)', () => {
  const md = '**证据 [[source:s1]] 重点**与*引用 [[source:s2]]*'
  const blocks = parseChapterMarkdown(md)
  assert.equal(chapterToMarkdown(blocks), md)
  const para = blocks[0]
  assert.equal(para.kind, 'p')
  if (para.kind === 'p') {
    assert.equal(para.inline[0].t, 'bold')
    const bold = para.inline[0]
    if (bold.t === 'bold') assert.ok(bold.children.some(t => t.t === 'src'))
  }
})

test('escapes and entities decode to plain text and reserialize to fixed point', () => {
  const md = '**粗体** 与 \\*星号\\* &amp; &#x1F600; \\&amp; &amp;amp;'
  const blocks = parseChapterMarkdown(md)
  const once = chapterToMarkdown(blocks)
  const twice = chapterToMarkdown(parseChapterMarkdown(once))
  assert.equal(twice, once) // 结构固定点
  assert.equal(decodeEntities('&amp;&#x1F600;&lt;'), '&😀<')
})

test('ordered list start=3 and nesting preserved', () => {
  const md = '3. 第三步\n4. 第四步\n   - 子项甲\n   - 子项乙\n5. 第五步'
  const blocks = parseChapterMarkdown(md)
  const ol = blocks[0]
  assert.equal(ol.kind, 'ol')
  if (ol.kind === 'ol') {
    assert.equal(ol.start, 3)
    assert.equal(ol.items[1].sub?.items.length, 2)
  }
  const back = chapterToMarkdown(blocks)
  const re = parseChapterMarkdown(back)
  const ol2 = re[0]
  assert.equal(ol2.kind, 'ol')
  if (ol2.kind === 'ol') assert.equal(ol2.start, 3)
})

test('citation atom serializes exactly as [[source:id]]', () => {
  const md = '引文说……[[source:S1]]，后续。'
  const blocks = parseChapterMarkdown(md)
  assert.equal(chapterToMarkdown(blocks), md)
  const blocks2 = parseChapterMarkdown('见 [[source:S2]] 与 [[source:S3]]。')
  const para = blocks2[0]
  assert.equal(para.kind, 'p')
  if (para.kind === 'p') {
    const srcs = para.inline.filter(t => t.t === 'src').map(t => (t as { id: string }).id)
    assert.deepEqual(srcs, ['S2', 'S3'])
    assert.equal(inlineToMarkdown(para.inline), '见 [[source:S2]] 与 [[source:S3]]。')
  }
})

test('unsupported markdown preserved verbatim (raw blocks, not washed)', () => {
  const md = '前文段落。\n\n> 引用块不被洗掉\n> 第二行\n\n```go\ncode()\n```\n\n| 表 | 格 |\n|---|---|\n\n后续段落。'
  const blocks = parseChapterMarkdown(md)
  assert.equal(chapterToMarkdown(blocks), md)
  const kinds = blocks.map(b => b.kind)
  assert.deepEqual(kinds, ['p', 'raw', 'raw', 'raw', 'p'])
})

test('docJson: chapters split with boundaries intact', () => {
  const doc = chaptersToDocJson([
    { id: 'c1', title: '第一章', body_markdown: '甲章正文。**粗**' },
    { id: 'c2', title: '第二章', body_markdown: '乙章正文。' },
  ])
  const chapters = docJsonToChapters(doc as PMNode)
  assert.equal(chapters.length, 2)
  assert.equal(chapters[0].chapterId, 'c1')
  assert.equal(chapters[0].markdown, '甲章正文。**粗**')
  assert.equal(chapters[1].markdown, '乙章正文。')
  assert.equal(chapters[0].endIndex < chapters[1].startIndex, true)
})

test('empty chapter yields an empty paragraph (writable entry)', () => {
  const doc = chaptersToDocJson([{ id: 'c1', title: '第一章', body_markdown: '' }])
  const chapters = docJsonToChapters(doc as PMNode)
  assert.equal(chapters.length, 1)
  assert.equal(chapters[0].markdown, '')
  const paragraphs = (doc.content ?? []).filter(n => n.type === 'paragraph')
  assert.equal(paragraphs.length, 1)
})

test('cross-chapter selection detection', () => {
  const doc = chaptersToDocJson([
    { id: 'c1', title: '第一章', body_markdown: '甲。' },
    { id: 'c2', title: '第二章', body_markdown: '乙。' },
  ]) as PMNode
  assert.equal(selectionCrossesChapter(doc, 1, 3), true)
  assert.equal(selectionCrossesChapter(doc, 1, 2), false)
  assert.equal(selectionCrossesChapter(doc, 0, 1), false)
  assert.equal(selectionCrossesChapter(doc, 1, 1), false)
  assert.equal(chapterAtIndex(doc, 3), 'c2')
  assert.equal(chapterAtIndex(doc, 1), 'c1')
})

test('format roundtrip through editor model keeps semantics (heading level + marks)', () => {
  const md = '### 方法\n\n步骤一 **重点** 与 *补充*。\n\n- 列表项'
  const doc = chaptersToDocJson([{ id: 'c1', title: '章', body_markdown: md }])
  const back = docJsonToChapters(doc as PMNode)[0].markdown
  assert.equal(back, md)
})
