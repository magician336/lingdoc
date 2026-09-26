import assert from 'node:assert/strict'
import test from 'node:test'
import { currencyOf, issueTargetLabel, viewOf } from './deliveryState'
import type { ExportArtifact } from '@/api/lingdoc/delivery'

const projectId = 'project-1'
const chapters = [
  { id: 'ch-question', title: '研究问题' },
  { id: 'ch-method', title: '研究方法' },
  { id: 'ch-blank', title: '   ' },
]

function artifact(overrides: Partial<ExportArtifact>): ExportArtifact {
  return {
    id: 'export-1',
    project_id: projectId,
    snapshot_id: 'snapshot-1',
    status: 'verified',
    file_sha256: 'a'.repeat(64),
    download_path: `/api/v1/lingdoc/projects/${projectId}/exports/export-1/file`,
    ...overrides,
  }
}

test('当前性：读到了才敢下结论，没读到就是未知', () => {
  assert.equal(currencyOf({ is_current: true }), '当前内容')
  assert.equal(currencyOf({ is_current: false }), '历史版本')

  // 「还没读」与「读失败了」都走这条路：调用方把失败降级成 undefined。
  assert.equal(currencyOf(undefined), '当前性未知')
  assert.equal(currencyOf(null), '当前性未知')

  // 字段缺席不是 false。用 `!is_current` 去判会把它读成「历史版本」——那是拿一个我们
  // 没有依据的结论去顶替「不知道」，而用户会照着它去重新导出一份本来还当前的文件。
  assert.equal(currencyOf({} as { is_current: boolean }), '当前性未知')
})

test('产物：只有真的有一份文件时才给下载地址', () => {
  const verified = viewOf(artifact({}))
  assert.deepEqual(verified, {
    kind: 'downloadable',
    downloadPath: `/api/v1/lingdoc/projects/${projectId}/exports/export-1/file`,
    fileSHA256: 'a'.repeat(64),
  })

  // 两个可空字段是 required + nullable：缺任何一个都不给按钮。一个指向不存在文件的
  // 地址比 null 更糟——界面会照着它给出一个点了就 422 的下载。
  assert.deepEqual(viewOf(artifact({ file_sha256: null })), { kind: 'unavailable' })
  assert.deepEqual(viewOf(artifact({ download_path: null })), { kind: 'unavailable' })
  assert.deepEqual(viewOf(artifact({ file_sha256: null, download_path: null })), { kind: 'unavailable' })
  assert.deepEqual(viewOf(artifact({ file_sha256: '' })), { kind: 'unavailable' })
})

test('产物：失败带着它自己的恢复建议，说不出原因的失败不猜', () => {
  assert.deepEqual(
    viewOf(artifact({
      status: 'failed',
      file_sha256: null,
      download_path: null,
      error: { code: 'render_failed', message: '文件生成失败。', retryable: true },
    })),
    { kind: 'failed', code: 'render_failed', message: '文件生成失败。', retryable: true },
  )

  // 失败却是不可重试的：界面不能给「再试一次」，那会把用户送进同一个错里。
  const blocked = viewOf(artifact({
    status: 'failed',
    error: { code: 'validation_failed', message: '文件内容与冻结版本不一致，已阻止下载。', retryable: false },
  }))
  assert.equal(blocked.kind, 'failed')
  assert.equal(blocked.kind === 'failed' && blocked.retryable, false)

  // 失败却没有 error 对象：说不出为什么、也不知道该不该重试，宁可不渲染成恢复建议。
  assert.deepEqual(
    viewOf(artifact({ status: 'failed', file_sha256: null, download_path: null })),
    { kind: 'unavailable' },
  )
})

test('产物：还没做出来的与认不出的都不给下载', () => {
  assert.deepEqual(viewOf(undefined), { kind: 'none' })
  assert.deepEqual(viewOf(null), { kind: 'none' })

  // 契约给 startExport 发布的 queued 就是这一条。本仓库的后端同步返回终态，界面上
  // 看不到它，但类型得容得下它——认不出的状态一律不能变成下载按钮。
  assert.deepEqual(
    viewOf(artifact({ status: 'queued', file_sha256: null, download_path: null })),
    { kind: 'unavailable' },
  )
  assert.deepEqual(
    viewOf(artifact({ status: 'something-else' as ExportArtifact['status'] })),
    { kind: 'unavailable' },
  )
})

test('阻断原因能定位到章节、项目，或诚实地退回 ID', () => {
  const label = (targetId: string) => issueTargetLabel({ target_id: targetId }, projectId, chapters)

  assert.equal(label('ch-question'), '研究问题')
  assert.equal(label('ch-method'), '研究方法')

  // 两种项目级发现对用户是同一件事：这个缺陷不属于任何一章。
  assert.equal(label(projectId), '整个项目')
  assert.equal(label('unresolved-project'), '整个项目')

  // 认不出来就原样显示 ID。猜一个章节出来会把定位引到错的地方，比不定位更坏。
  assert.equal(label('ch-deleted'), 'ch-deleted')

  // 标题是空白时落回 ID：界面上一个没写字的标签读起来像界面坏了。
  assert.equal(label('ch-blank'), 'ch-blank')
})
