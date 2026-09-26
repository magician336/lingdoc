import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import { chapterCitations, MALFORMED_CITATION_MESSAGE } from './chapterCitations'

const workspace = readFileSync(new URL('./Workspace.vue', import.meta.url), 'utf8')
const chapterCitationsSource = readFileSync(new URL('./chapterCitations.ts', import.meta.url), 'utf8')

function idsOf(body: string): string[] {
  const parsed = chapterCitations(body)
  assert.equal(parsed.kind, 'ok', `expected a clean parse of ${JSON.stringify(body)}`)
  return parsed.kind === 'ok' ? parsed.sourceIds : []
}

test('citations are read from the body in the canonical order', () => {
  // 顺序跟着提交走的话，同一组引用会算出不同的请求体，幂等重放就被判成「同一个键对应
  // 不同请求」。排序去重是与服务端同一条规则，不是显示上的偏好。
  assert.deepEqual(idsOf('[[source:beta]] 与 [[source:alpha]]'), ['alpha', 'beta'])
  assert.deepEqual(idsOf('[[source:beta]] [[source:alpha]] [[source:beta]]'), ['alpha', 'beta'])
  assert.deepEqual(idsOf('[[source:Alpha_2]] [[source:alpha_2]]'), ['Alpha_2', 'alpha_2'])
})

test('a body with no marker submits no citation', () => {
  assert.deepEqual(idsOf(''), [])
  assert.deepEqual(idsOf('没有任何引用的正文'), [])
  // 清空引用是用户明确放弃，不是一种错误：服务端在空集上不复核，这里也不该拦。
  assert.deepEqual(idsOf('正文里那句摘录已经被删掉了'), [])
})

test('a half-written marker is refused before the request leaves the page', () => {
  // 服务端数的是 `[[source:` 的出现次数与完整匹配数，两者不等就判 400。同一套判据
  // 提前在本地跑一遍，用户拿到的是一句说清了哪里不对的话。
  assert.equal(chapterCitations('[[source:s-demo/invalid]]').kind, 'malformed')
  assert.equal(chapterCitations('[[source:]]').kind, 'malformed')
  assert.equal(chapterCitations('[[source:alpha').kind, 'malformed')
  assert.equal(chapterCitations('[[source:alpha]] 还有一处 [[source:').kind, 'malformed')
  assert.match(MALFORMED_CITATION_MESSAGE, /\[\[source:/)
})

test('the marker rule is the server rule, character for character', () => {
  // 两边漂开时的症状最难查：保存按钮亮着，点下去回一个 400，而两边都「有实现」。
  // 所以这条断言盯的是字面量本身，不是行为。用 includes 而不是正则去比对正则：
  // 一层反斜杠已经够绕了，套两层只会让这条守卫自己先坏掉。
  assert.ok(
    chapterCitationsSource.includes('/\\[\\[source:([A-Za-z0-9_-]+)\\]\\]/g'),
    'chapterCitations.ts 的标记正则与约定的那一份不一致',
  )
  const server = readFileSync(
    new URL('../../../../internal/lingdoc/workspacecore/service.go', import.meta.url), 'utf8')
  assert.ok(
    server.includes('regexp.MustCompile(`\\[\\[source:([A-Za-z0-9_-]+)\\]\\]`)'),
    'workspacecore 的标记正则变了，界面这一侧必须跟着改',
  )
})

test('saving sends the citations read from the draft instead of an empty list', () => {
  // 曾经写死 `source_ids: []`：正文里的标记被服务端当成一次不实申报，于是带引用的章节
  // 只能看不能改。这条断言盯的是那处调用点，因为它才是缺陷原来的位置。
  assert.match(workspace, /chapterCitations\(bodyDraft\.value\)/)
  assert.doesNotMatch(workspace, /source_ids: \[\] as string\[\]/)
  // 写坏的标记在本地就拦下：不发请求，给一句比 400 更有用的提示。
  assert.match(workspace, /MALFORMED_CITATION_MESSAGE/)
})
