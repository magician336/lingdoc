import assert from 'node:assert/strict'
import test from 'node:test'
import fs from 'node:fs'
import { fileURLToPath } from 'node:url'
import { parseChapterMarkdown, chapterToMarkdown, plainOf } from './markdown'
import type { Block, InlineToken } from './markdown'
import { chaptersToDocJson, docJsonToChapters, type PMNode } from './docJson'

// Use the same committed semantic fixtures as the DOCX renderer; no local acceptance directory required.
const goldenPath = fileURLToPath(new URL('../../../../../internal/lingdoc/docx/testdata/editor-md-golden.json', import.meta.url))

interface GoldenCase { name: string; body: string; plain?: string[]; source_ids?: string[]; legacy_preserve?: boolean }

function loadGolden(): GoldenCase[] {
  return JSON.parse(fs.readFileSync(goldenPath, 'utf8')) as GoldenCase[]
}

function plainOfBlocks(blocks: Block[]): string[] {
  const out: string[] = []
  let srcSeq = 0
  const inline = (tokens: InlineToken[]): string => tokens.map(t => {
    if (t.t === 'text') return t.s
    if (t.t === 'src') { srcSeq++; return `[${srcSeq}]` }
    return inline(t.children)
  }).join('')
  const walkItems = (items: Array<{ inline: InlineToken[]; sub: unknown }>) => {
    for (const it of items) {
      out.push(inline(it.inline))
      if (it.sub) walkItems((it.sub as { items: Array<{ inline: InlineToken[]; sub: unknown }> }).items)
    }
  }
  for (const b of blocks) {
    if (b.kind === 'p' || b.kind === 'h') out.push(inline(b.inline))
    else if (b.kind === 'ul' || b.kind === 'ol') walkItems(b.items)
    else if (b.kind === 'raw') out.push(...b.text.split('\n'))
  }
  return out.filter(s => s.trim().length > 0)
}

function sourceIds(blocks: Block[]): string[] {
  const ids: string[] = []
  const walk = (tokens: InlineToken[]) => {
    for (const t of tokens) {
      if (t.t === 'src') ids.push(t.id)
      else if (t.t !== 'text') walk(t.children)
    }
  }
  const walkItems = (items: Array<{ inline: InlineToken[]; sub: unknown }>) => {
    for (const it of items) {
      walk(it.inline)
      if (it.sub) walkItems((it.sub as { items: Array<{ inline: InlineToken[]; sub: unknown }> }).items)
    }
  }
  for (const b of blocks) {
    if (b.kind === 'p' || b.kind === 'h') walk(b.inline)
    else if (b.kind === 'ul' || b.kind === 'ol') walkItems(b.items)
  }
  return ids
}

const cases = loadGolden()
assert.ok(cases.length > 0, 'Committed shared golden fixture must not be empty')

for (const c of cases) {
  test(`golden:${c.name} 解析→序列化→重开 结构固定点`, () => {
    const blocks1 = parseChapterMarkdown(c.body)
    const md1 = chapterToMarkdown(blocks1)
    const blocks2 = parseChapterMarkdown(md1)
    const md2 = chapterToMarkdown(blocks2)
    // 固定点：第二次序列化稳定
    assert.equal(md2, md1)
    // 呈现文字一致
    if (c.plain) assert.deepEqual(plainOfBlocks(blocks2), c.plain)
    // 引用原子一致
    if (c.source_ids) assert.deepEqual(sourceIds(blocks2), c.source_ids)
    // 未支持块原样保留（不洗掉）
    if (c.legacy_preserve) {
      const raws = blocks1.filter(b => b.kind === 'raw').map(b => (b as { text: string }).text)
      assert.ok(raws.length >= 2)
      assert.ok(raws.some(r => r.startsWith('> 旧有引用块')))
      assert.ok(raws.some(r => r.includes('| 列一 | 列二 |')))
    }
  })

  test(`golden:${c.name} PM↔Markdown 结构一致`, () => {
    const doc = chaptersToDocJson([{ id: 'c1', title: '章一', body_markdown: c.body }])
    const back = docJsonToChapters(doc as PMNode)[0].markdown
    const re = parseChapterMarkdown(back)
    if (c.plain) assert.deepEqual(plainOfBlocks(re), c.plain)
    if (c.source_ids) assert.deepEqual(sourceIds(re), c.source_ids)
    void plainOf
  })
}

test('golden:numbered-start-and-nested-list 起始号与嵌套保留', () => {
  const c = cases.find(x => x.name === 'numbered-start-and-nested-list')
  assert.ok(c)
  const blocks = parseChapterMarkdown(c.body)
  const ol = blocks[0]
  assert.equal(ol.kind, 'ol')
  if (ol.kind === 'ol') {
    assert.equal(ol.start, 3)
    assert.equal(ol.items.length, 3)
    assert.equal(ol.items[1].sub?.kind, 'ul')
    assert.equal(ol.items[1].sub?.items.length, 2)
    assert.equal(ol.items[2].sub, null)
  }
})
