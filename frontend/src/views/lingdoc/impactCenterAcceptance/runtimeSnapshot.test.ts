import assert from 'node:assert/strict'
import test from 'node:test'
import {
  G3_SNAPSHOT_HEAD,
  G3_SNAPSHOT_RAW,
  loadG3Snapshot,
  snapshotNotice,
} from './runtimeSnapshot'

const EXPORT_SHA = 'd5a5f160fbe2e45310a74c9adeab4aeb605887ce8100dee80719184b8c8fb3e3'

test('快照装载出来的就是那份历史证据', () => {
  const snapshot = loadG3Snapshot(G3_SNAPSHOT_RAW)
  assert.equal(snapshot.head, G3_SNAPSHOT_HEAD)
  assert.equal(snapshot.head, '7f5fd8c')
  assert.equal(snapshot.mode, '历史快照')
  assert.equal(snapshot.isLive, false)
  assert.equal(snapshot.disclaimer, '历史快照 · 非本次运行')
})

test('通知语必须同时带「历史快照 · 非本次运行」与 head', () => {
  const notice = snapshotNotice(loadG3Snapshot(G3_SNAPSHOT_RAW))
  assert.ok(notice.includes('历史快照 · 非本次运行'), notice)
  assert.ok(notice.includes('7f5fd8c'), notice)
})

test('快照记录的是当时那个租户与操作者，不是当前会话', () => {
  const snapshot = loadG3Snapshot(G3_SNAPSHOT_RAW)
  assert.equal(snapshot.identity.tenantId, 10054)
  assert.equal(snapshot.identity.user, 'lingdoc-f4')
  assert.equal(snapshot.identity.role, 'owner')
  // 免登录快照不假造当前身份。
  assert.equal(snapshot.identity.isCurrentSession, false)
})

test('上下文修订变化被如实记下', () => {
  const snapshot = loadG3Snapshot(G3_SNAPSHOT_RAW)
  assert.equal(snapshot.changeSet.baseContextRevision, 3)
  assert.equal(snapshot.changeSet.targetContextRevision, 4)
  assert.equal(snapshot.project.currentContextRevision, 4)
})

test('影响任务全部闭环，且记下了本次变更集带来的两条', () => {
  const snapshot = loadG3Snapshot(G3_SNAPSHOT_RAW)
  assert.equal(snapshot.impactTasks.length, 6)
  for (const task of snapshot.impactTasks) {
    assert.equal(task.status, 'DONE')
  }
  const fromThisChangeSet = snapshot.impactTasks.filter(
    (task) => task.changeSetId === snapshot.changeSet.id,
  )
  assert.equal(fromThisChangeSet.length, 2)
})

test('导出产物的大小与 SHA-256 与证据一致', () => {
  const snapshot = loadG3Snapshot(G3_SNAPSHOT_RAW)
  const artifact = snapshot.exports.find((item) => item.id === 'export-e1106848ba4033b0c1127eb6dcda0779')
  assert.ok(artifact)
  assert.equal(artifact.status, 'verified')
  assert.equal(artifact.bytes, 2250)
  assert.equal(artifact.fileSha256, EXPORT_SHA)
  assert.equal(artifact.sha256AfterRestart, EXPORT_SHA)
  assert.equal(artifact.identicalAfterRestart, true)
})

test('链路逐步都有方法、路径与结果', () => {
  const snapshot = loadG3Snapshot(G3_SNAPSHOT_RAW)
  assert.ok(snapshot.chain.length >= 10)
  for (const step of snapshot.chain) {
    assert.ok(step.name.length > 0, `第 ${step.step} 步缺名称`)
    assert.ok(step.method.length > 0)
    assert.ok(step.path.length > 0)
    assert.ok(step.result.length > 0)
  }
  // 快照里的每一步都是历史上真实跑过的，不存在「未执行」。
  for (const step of snapshot.chain) {
    assert.ok(!step.result.includes('未执行'), `第 ${step.step} 步不应标未执行`)
  }
})

test('装载器拒绝残缺或不合法的快照', () => {
  const broken = (mutate: (raw: Record<string, unknown>) => void) => {
    const raw = structuredClone(G3_SNAPSHOT_RAW) as unknown as Record<string, unknown>
    mutate(raw)
    return raw
  }

  assert.throws(() => loadG3Snapshot(broken((raw) => { delete raw.head })), /head/)
  assert.throws(() => loadG3Snapshot(broken((raw) => { raw.head = 'not-a-sha' })), /head/)
  assert.throws(() => loadG3Snapshot(broken((raw) => { delete raw.chain })), /chain/)
  assert.throws(() => loadG3Snapshot(broken((raw) => { raw.chain = 'not-an-array' })), /chain/)
  assert.throws(
    () => loadG3Snapshot(broken((raw) => { (raw.change_set as Record<string, unknown>).base_context_revision = 0 })),
    /base_context_revision/,
  )
  assert.throws(
    () => loadG3Snapshot(broken((raw) => { (raw.exports as Array<Record<string, unknown>>)[0].file_sha256 = 'zz' })),
    /file_sha256/,
  )
  assert.throws(() => loadG3Snapshot(undefined), /快照/)
})
