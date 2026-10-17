import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import test from 'node:test'

// g5-invitation-workbench-20261014：通知区布局契约测试。
// 背景：invitation-inbox / discarded-drafts 曾作为 .wb 命名网格的无 grid-area 顶层子元素，
// 被自动布局挤压成约 34px 竖列并遮挡接受按钮（F-1）。本测试锁住修复契约，防止回归。
const here = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(join(here, 'Workspace.vue'), 'utf8')

test('notification region is a first-class named grid area on the workbench shell', () => {
  assert.match(source, /grid-template-areas:[^;]*"head head"[^;]*"notify notify"/s, 'desktop .wb grid must own a notify row')
  assert.match(source, /grid-template-areas:\s*"head"\s*"alert"\s*"notify"[^`]*"doc"/s, 'narrow (<=899px) .wb grid must own a notify row')
  assert.match(source, /\.wb-notify\s*\{[^}]*grid-area:\s*notify/s, '.wb-notify must declare grid-area: notify')
})

test('notification region is bounded, scrollable and collapsible so it cannot squeeze the paper column', () => {
  assert.match(source, /\.wb-notify\s*\{[^}]*max-height:/s, 'notify region must have a max-height')
  assert.match(source, /\.wb-notify\s*\{[^}]*overflow-y:\s*auto/s, 'notify region must scroll internally')
  assert.match(source, /notifyOpen/, 'notify region must be collapsible')
  assert.doesNotMatch(source, /class="invitation-inbox"/, 'old unplaced invitation-inbox section must be gone')
  assert.doesNotMatch(source, /class="discarded-drafts"[^>]*grid-area/, 'discarded-drafts must not claim its own grid area')
})

test('invitations render per-view-state with accept only for active ones', () => {
  assert.match(source, /invitationViewState\(invite\)/, 'each invite row must carry its view state')
  assert.match(source, /v-if="invitationViewState\(invite\)\s*===?\s*'active'"/, 'accept button must render only for active invitations')
  assert.match(source, /invitationStatusLabel\(invite\)/, 'each invite row must show a human status label')
  assert.match(source, /invitationScopeDetail\(invite\)/, 'raw scope ids must stay behind a details disclosure')
})

test('narrow screens stack the invite row instead of squeezing it', () => {
  assert.match(source, /@media \(max-width: 640px\)\s*\{[^}]*\.wb-invite\s*\{[^}]*flex-direction:\s*column/s, 'invite row must stack on narrow screens')
})

test('collaboration entry stays in toolbar rather than an unplaced display-contents grid item', () => {
  const header=source.slice(source.indexOf('<header '),source.indexOf('</header>'))
  assert.match(header, /openFullPanel\('members'\)/)
  assert.equal((source.match(/成员与协作权限 →/g)||[]).length, 1)
})
