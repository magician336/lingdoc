import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import type { AccessStatus } from '@/api/lingdoc/workspace'
import { RESTRICTED_NOTICE, isRestricted, recoveryActionsOf } from './accessStatus'

const workspace = readFileSync(new URL('./Workspace.vue', import.meta.url), 'utf8')

function statusOf(contentAccess: AccessStatus['content_access'], actions: string[] = []): AccessStatus {
  return {
    project_id: 'p-demo',
    content_access: contentAccess,
    recovery_actions: actions,
    can_create_project: true,
  }
}

test('only a restricted answer raises the notice', () => {
  // unknown 是「服务端答不出」，不是「受限」。把它算进来，每次读取失败都会弹一条
  // 「资料不可用」——一个我们没有依据下的结论。
  assert.equal(isRestricted(statusOf('restricted', ['restore_source_authorization'])), true)
  assert.equal(isRestricted(statusOf('available')), false)
  assert.equal(isRestricted(statusOf('unknown')), false)
  // 还没读到、或读失败时调用方传 null：不显示，也不崩。
  assert.equal(isRestricted(null), false)
  assert.equal(isRestricted(undefined), false)
})

test('a restricted project gets both recovery paths, in the server order', () => {
  const actions = recoveryActionsOf(
    statusOf('restricted', ['restore_source_authorization', 'create_clean_project']))
  assert.deepEqual(actions.map(action => action.code),
    ['restore_source_authorization', 'create_clean_project'])
  // 两条枚举都翻过一遍，且都带上具体做法——只给动作名等于把 §8 那段话丢给用户自己猜。
  assert.deepEqual(actions.map(action => action.label), ['恢复原资料授权', '新建干净项目'])
  assert.ok(actions.every(action => action.detail.length > 0))
  assert.match(RESTRICTED_NOTICE, /资料/)
})

test('anything other than restricted offers no recovery path', () => {
  assert.deepEqual(recoveryActionsOf(statusOf('available')), [])
  assert.deepEqual(recoveryActionsOf(statusOf('unknown')), [])
  assert.deepEqual(recoveryActionsOf(null), [])
  // 服务端在 available 下多给了一条动作也不显示：动作列表只在受限时才有意义。
  assert.deepEqual(recoveryActionsOf(statusOf('available', ['create_clean_project'])), [])
})

test('an unrecognised action is shown by name instead of being dropped', () => {
  // 枚举漂移时最坏的结果是静默吞掉一条恢复路径：用户看到「受限」，却没有任何可做的事。
  // 认不出来就原样显示，与 deliveryState.ts 的 issueTargetLabel 同一条规矩。
  const actions = recoveryActionsOf(statusOf('restricted', ['export_my_data']))
  assert.deepEqual(actions, [{ code: 'export_my_data', label: 'export_my_data', detail: '' }])
})

test('a restricted answer with no listed action still raises the notice', () => {
  // 提示条归 isRestricted 管，动作列表只管列什么：把显示条件挂在列表长度上，
  // 这一种回答会让整个提示消失。
  const status = statusOf('restricted')
  assert.equal(isRestricted(status), true)
  assert.deepEqual(recoveryActionsOf(status), [])
})

test('the workspace actually asks for the access status and offers a re-check', () => {
  // 缺口 ④ 原来的样子是后端恒答 unknown、界面问都不问：恢复入口在代码里存在，用户看不到。
  // 这条断言盯的就是那处调用点（样板 chapterCitations.test.ts 的源码级回归断言）。
  assert.match(workspace, /getAccessStatus/)
  assert.match(workspace, /isRestricted\(/)
  assert.match(workspace, /recoveryActionsOf\(/)
  assert.match(workspace, /重新检查/)
})
