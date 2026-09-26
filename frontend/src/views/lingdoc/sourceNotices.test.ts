import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import type { Asset } from '@/api/lingdoc/workspace'
import { DENIED_NOTICE, assetLabel, bindingNotice, denyReasonOf, deniedSourcesOf } from './sourceNotices'

const workspace = readFileSync(new URL('./Workspace.vue', import.meta.url), 'utf8')

/**
 * 照 utils/request.ts:219-223 的样子造一个 rejection 对象：响应体被整个摊在了顶层，
 * 所以 `details` 仍然待在 `error` 里面（后端 sendErrorDetails 就是这么发的）。
 */
function rejectionOf(body: unknown) {
  return Object.assign(new Error('Request failed with status code 422'), body)
}

function assetOf(overrides: Partial<Asset> = {}): Asset {
  return {
    id: 'asset-1',
    project_id: 'p-demo',
    knowledge_id: 'kb-doc-9',
    title: '项目建议书',
    asset_revision: 1,
    processing_state: 'ready',
    ...overrides,
  }
}

test('the denied list is read from inside error.details, not off the top level', () => {
  // 这一条是缺口 ② 的全部要害：信息一直在响应里，只是路径比看上去深一层。
  // 照 details 在顶层的写法去读，拿到的永远是空数组——而空数组在界面上等于「没有问题」。
  const error = rejectionOf({
    error: {
      code: 'source_not_authorized',
      message: '请求中存在未获授权的资料，未开始处理。',
      retryable: false,
      details: { denied: [{ asset_id: 'asset-x', reason: 'not_authorized' }] },
    },
    request_id: 'req-1',
  })
  assert.deepEqual(deniedSourcesOf(error), [{ assetId: 'asset-x', reason: 'not_authorized' }])
})

test('a response of any other shape yields no denied list instead of throwing', () => {
  // 这条提示是**补充**：形状对不上时最坏的结果应该是少一条说明，
  // 不是把一次网络失败升级成一次渲染崩溃（用户连错误信息都看不到了）。
  assert.deepEqual(deniedSourcesOf(rejectionOf({})), [])
  assert.deepEqual(deniedSourcesOf(rejectionOf({ error: { details: {} } })), [])
  assert.deepEqual(deniedSourcesOf(rejectionOf({ detail: { denied: [] } })), [])
  assert.deepEqual(deniedSourcesOf(rejectionOf({ error: { details: { denied: 'not_authorized' } } })), [])
  assert.deepEqual(deniedSourcesOf(undefined), [])
  assert.deepEqual(deniedSourcesOf(null), [])
  // 形状对但内容不完整的条目被丢掉——它没有 asset_id，渲染出来只会是一行空白。
  assert.deepEqual(deniedSourcesOf(rejectionOf({
    error: { details: { denied: [{ reason: 'not_ready' }, { asset_id: '' }] } },
  })), [])
})

test('each reason reads in Chinese and carries its own next step', () => {
  const authorized = denyReasonOf('not_authorized')
  const ready = denyReasonOf('not_ready')
  const missing = denyReasonOf('not_found')
  assert.deepEqual(
    [authorized.label, ready.label, missing.label],
    ['未获授权', '尚未就绪', '找不到，或不属于本项目'])
  // 三种原因的对策必须互不相同：都落到同一句「请检查资料」等于没告诉用户该做什么。
  assert.equal(new Set([authorized.next, ready.next, missing.next]).size, 3)
  assert.ok([authorized, ready, missing].every(reason => reason.next.length > 0))
})

test('an unrecognised reason is shown by name rather than explained away', () => {
  // 编一段中文解释等于替服务端猜它的语义，而用户会照着猜出来的对策去操作。
  // 与 accessStatus.ts 的 KNOWN_ACTIONS 同一条规矩：认不出来就原样显示。
  assert.deepEqual(denyReasonOf('quota_exhausted'), {
    label: 'quota_exhausted',
    next: '请检查这一份资料的授权与解析状态。',
  })
  assert.equal(denyReasonOf('').label, '未说明原因')
})

test('binding a not-yet-ready asset explains why it then vanishes from the list', () => {
  // 现在它会 201 成功、随后从列表里消失：listAssets 只回允许集合。不说这一句，
  // 用户看到的是「绑定成功了但什么都没发生，再绑一次还是这样」——像在丢数据。
  const notice = bindingNotice(assetOf({ processing_state: 'processing' }))
  assert.match(notice, /项目建议书/)
  assert.match(notice, /正在解析/)
  assert.match(notice, /不会出现在上面/)
  // 另一个状态换个说法，免得三个状态共用一句读起来像没更新。
  assert.match(bindingNotice(assetOf({ processing_state: 'failed' })), /解析失败/)
  assert.match(bindingNotice(assetOf({ processing_state: 'pending' })), /还没开始解析/)
})

test('a ready asset, or no asset at all, produces no notice', () => {
  assert.equal(bindingNotice(assetOf()), '')
  assert.equal(bindingNotice(null), '')
  assert.equal(bindingNotice(undefined), '')
  // 认不出的状态照样说，不静默——否则服务端多一个状态时，资料又会重新无声消失。
  assert.match(bindingNotice(assetOf({ processing_state: 'quarantined' as Asset['processing_state'] })), /quarantined/)
})

test('an asset with no title falls back to its knowledge id', () => {
  assert.equal(assetLabel(assetOf()), '项目建议书')
  assert.equal(assetLabel(assetOf({ title: '' })), 'kb-doc-9')
  assert.match(bindingNotice(assetOf({ title: '', processing_state: 'pending' })), /kb-doc-9/)
})

test('the workspace renders both notices at their real cause', () => {
  // 盯调用点：模块写好了而界面没接，等于这次修补没发生（accessStatus 那条的老问题）。
  assert.match(workspace, /deniedSourcesOf\(/)
  assert.match(workspace, /denyReasonOf\(/)
  assert.match(workspace, /bindingNotice\(/)
  assert.match(workspace, /DENIED_NOTICE/)
})
