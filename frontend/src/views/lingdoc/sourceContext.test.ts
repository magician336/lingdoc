import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import type { Source, SourceContext, SourceContextSegment } from '@/api/lingdoc/workspace'
import {
  CONTEXT_UNAVAILABLE_NOTICE,
  contextLines,
  contextNotice,
  quotedTextOf,
  sourceStatusLabel,
  windowLabel,
} from './sourceContext'

const workspace = readFileSync(new URL('./Workspace.vue', import.meta.url), 'utf8')

function sourceOf(status: Source['status'] = 'available', quotedText = '第三段的正文。'): Source {
  return {
    id: 'source-4',
    project_id: 'p-demo',
    asset_id: 'asset-txt',
    asset_revision: 1,
    locator: 'char:12-24',
    quoted_text: quotedText,
    quoted_text_hash: 'sha256:0',
    status,
  }
}

function segmentOf(overrides: Partial<SourceContextSegment> = {}): SourceContextSegment {
  return { source_id: 'source-3', chunk_index: 3, relation: 'before', verbatim: true, text: '第三段。', ...overrides }
}

function contextOf(overrides: Partial<SourceContext> = {}): SourceContext {
  return {
    source: sourceOf(),
    context_available: true,
    window: { before: 1, after: 1 },
    segments: [segmentOf(), segmentOf({ source_id: 'source-5', chunk_index: 5, relation: 'after', text: '第五段。' })],
    ...overrides,
  }
}

test('an unknown source status is reported by name instead of being dropped', () => {
  assert.equal(sourceStatusLabel('available'), '可以指回原文')
  assert.equal(sourceStatusLabel('stale'), '坐标已不可信')
  assert.equal(sourceStatusLabel('unavailable'), '此刻取不回来')
  // 枚举漂移时不显示比显示原文更糟：用户会以为这条引用还能用。
  assert.equal(sourceStatusLabel('superseded'), 'superseded')
})

test('the segments are laid out in chunk order regardless of what the server sent', () => {
  // 服务端已经给过一遍顺序，这里重排是为了界面不依赖它：靠数组下标暗示顺序，
  // 服务端换个拼接次序（先 after 再 before）就整段错位，而这是最难看出来的一种错。
  const lines = contextLines(contextOf({
    segments: [
      segmentOf({ source_id: 'source-5', chunk_index: 5, relation: 'after', text: '第五段。' }),
      segmentOf({ source_id: 'source-3', chunk_index: 3, relation: 'before', text: '第三段。' }),
    ],
  }))
  assert.deepEqual(lines.map(line => line.id), ['source-3', 'source-5'])
  assert.deepEqual(lines.map(line => line.label), ['第 3 段（前）', '第 5 段（后）'])
})

test('a segment that is not verbatim still shows its text, but says so', () => {
  // 弱档（PDF/DOCX 要重解析）拿不回原文，text 却是分块表里的真实正文。
  // 把它标成「原文」是编的；把它藏起来则是把窗口掏空——两种都不行。
  const [line] = contextLines(contextOf({
    segments: [segmentOf({ verbatim: false, text: '分块表里的正文。' })],
  }))
  assert.equal(line.text, '分块表里的正文。')
  assert.equal(line.verbatim, false)
  assert.match(line.note, /分块记录/)
})

test('a verbatim segment carries no note, and its absence is not a missing text', () => {
  const [verbatimLine, missingLine] = contextLines(contextOf({
    segments: [
      segmentOf({ chunk_index: 3, verbatim: true, text: '第三段。' }),
      segmentOf({ chunk_index: 4, verbatim: false, text: '' }),
    ],
  }))
  // 逐字对上的那一段不需要解释——它本身就说明了自己。
  assert.equal(verbatimLine.note, '')
  // 空 text 不是「没有说明」，是「坐标对不上，只有位置」。两种 false 的措辞必须分开：
  // 一句写错就会把「这段是坐标漂移」说成「这段是弱档」。
  assert.equal(missingLine.text, '')
  assert.match(missingLine.note, /坐标/)
  assert.notEqual(missingLine.note, verbatimLine.note)
})

test('a window with nothing to expand produces no lines and no window label', () => {
  const blocked = contextOf({ context_available: false, segments: [] })
  assert.deepEqual(contextLines(blocked), [])
  assert.deepEqual(contextLines(null), [])
  assert.equal(windowLabel(blocked), '')
  assert.equal(windowLabel(null), '')
  assert.equal(windowLabel(contextOf()), '引用处前后各 1 段')
})

test('the two reasons a reference cannot expand are worded apart', () => {
  // 派生块（摘要、图片说明）本来就没有指向原文的坐标；失效的引用是本来能展开、现在不能了。
  // 混成一句会把「这段是摘要」说成「这条引用坏了」，反过来又会把一次失效说成它没有原文。
  const derived = contextOf({ context_available: false, segments: [], source: sourceOf('available') })
  const broken = contextOf({ context_available: false, segments: [], source: sourceOf('stale') })
  assert.match(contextNotice(derived), /派生/)
  assert.match(contextNotice(broken), /坐标已不可信/)
  assert.notEqual(contextNotice(derived), contextNotice(broken))
  // 展开得了就不该有说明：这条提示只在展不开时说话。
  assert.equal(contextNotice(contextOf()), '')
  assert.equal(contextNotice(null), '')
  assert.match(CONTEXT_UNAVAILABLE_NOTICE, /展不开/)
})

test('a reference with no quote says so instead of rendering blank', () => {
  // 逐字对不上时服务端不给引文。空白读起来像界面坏了，而它其实是一条结论。
  assert.equal(quotedTextOf(sourceOf('available', '第三段的正文。')), '第三段的正文。')
  assert.match(quotedTextOf(sourceOf('stale', '')), /无法取回/)
  assert.equal(quotedTextOf(null), '')
})

test('a window that opens onto nothing is not left looking like a failure', () => {
  // context_available 为真但一段邻居都没有（引用在文档两头）是合法的一屏：
  // 空列表配着「前后各 1 段」的抬头，不说一句就成了「加载失败」的样子。
  const empty = contextOf({ segments: [] })
  assert.deepEqual(contextLines(empty), [])
  assert.equal(contextNotice(empty), '')
  assert.equal(windowLabel(empty), '引用处前后各 1 段')
  assert.match(workspace, /!contextRows\.length/)
})

test('the workspace actually offers the inline panel, not a jump to the knowledge base', () => {
  // 缺口 ① 原来的样子是列表上只有一句引文，展不开也无处可去。
  // 这条断言盯的是那处调用点，以及「不跳页」这个形态本身。
  assert.match(workspace, /getSourceContext/)
  assert.match(workspace, /contextLines\(/)
  assert.match(workspace, /查看原文/)
  assert.match(workspace, /loadSourceContext/)
  assert.doesNotMatch(workspace, /router\.push/)
})
