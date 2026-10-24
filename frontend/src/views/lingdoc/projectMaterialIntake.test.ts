import test from 'node:test'
import assert from 'node:assert/strict'
import { reactive } from 'vue'
import { ProjectMaterialIntake, materialReadiness, type MaterialPorts, type MaterialObservation, type MaterialTrace, type MaterialStorage } from './projectMaterialIntake'
import type { Asset, Source } from '@/api/lingdoc/workspace'

function knowledge() { return { id: 'knowledge-a', knowledge_base_id: 'kb-a', title: 'Synthetic material', parse_status: 'completed' } }
function trace(): MaterialTrace {
  return { knowledge_id: 'knowledge-a', current_attempt: 2, latest_attempt: 2, parse_status: 'completed',
    trace: { name: 'knowledge_processing', kind: 'root', status: 'done', span_id: 'root-real',
      children: [{ name: 'embedding', kind: 'stage', status: 'done', span_id: 'index-real', attempt: 2, knowledge_id: 'knowledge-a' } as any] } }
}
function asset(): Asset { return { id: 'asset-a', project_id: 'project-a', knowledge_id: 'knowledge-a', title: 'Synthetic', processing_state: 'ready', asset_revision: 1 } }
function source(): Source { return { id: 'source-a', project_id: 'project-a', asset_id: 'asset-a', asset_revision: 1, locator: 'paragraph 1', quoted_text: 'Synthetic excerpt', quoted_text_hash: 'hash', status: 'available' } }
function fixture(storage?: MaterialStorage, identity = 'user-a:tenant-a') {
  const calls: Array<{ action: string; args: unknown[] }> = []
  let observation: MaterialObservation = { knowledge: knowledge(), trace: trace(), assets: [] }
  const ports: MaterialPorts = {
    async upload(...args) { calls.push({ action: 'upload', args }); return { knowledge: knowledge(), reused: false } },
    async observe(...args) { calls.push({ action: 'observe', args }); return structuredClone(observation) },
    async bind(...args) { calls.push({ action: 'bind', args }); observation.assets = [asset()]; return asset() },
    async search(...args) { calls.push({ action: 'search', args }); return [source()] },
    newKey() { return 'same-key' },
  }
  const model = new ProjectMaterialIntake('project-a', identity, ports, storage)
  return { model, ports, calls, observation }
}
const file = new File(['synthetic material'], 'paper.txt')
test('Vue reactive controller preserves upload/read/bind observations', async () => {
  const f = fixture()
  const model = reactive(f.model)
  await model.upload('kb-a', file)
  assert.equal(model.rows[0].indexReady, true)
  await model.bind(model.rows[0])
  assert.equal(model.rows[0].asset?.id, 'asset-a')
})
test('forgetting local tracking removes only its reference and invalidates a pending read', async () => {
  const f = fixture()
  return uploaded(f).then(async () => {
    const row = f.model.rows[0], pending = deferred<MaterialObservation>()
    f.ports.observe = () => pending.promise
    const read = f.model.refresh(row)
    f.model.forget(row)
    pending.resolve(f.observation)
    await read
    assert.equal(f.model.rows.length, 0)
    assert.equal(row.indexReady, false)
    assert(!f.calls.some(call => call.action === 'bind'))
  })
})
async function uploaded(f = fixture()) { await f.model.upload('kb-a', file); return f }
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

test('pending, processing, finalizing, failed, cancelled and unknown never imply indexing completion', () => {
  for (const state of ['pending', 'unprocessed', 'processing', 'finalizing', 'failed', 'cancelled', '', 'future']) {
    assert.equal(materialReadiness({ ...knowledge(), parse_status: state }, trace()).ready, false, state)
  }
})
test('parse completed plus a REAL latest-attempt completed embedding stage is positive indexing evidence', () => {
  assert.equal(materialReadiness(knowledge(), trace()).ready, true)
})

test('parsed-but-not-indexed preserves the actual index failure cause without declaring processing complete', () => {
  const t = trace()
  Object.assign(t.trace!.children![0], { status: 'failed', error_message: 'embedding model unavailable' })
  const result = materialReadiness(knowledge(), t)
  assert.equal(result.ready, false)
  assert.match(result.label, /解析完成，但索引失败/)
  assert.match(result.label, /embedding model unavailable/)
  assert(!result.label.includes('处理完成'))
})
test('legacy synthesized done placeholders do not prove indexing, even with current_attempt > 0', () => {
  const t = trace()
  t.trace!.children![0].span_id = ''
  assert.equal(materialReadiness(knowledge(), t).ready, false)
})
test('unknown, failed, skipped, pending, running and cancelled index stages do not unlock binding', () => {
  for (const status of ['unknown', 'failed', 'skipped', 'pending', 'running', 'cancelled']) {
    const t = trace(); t.trace!.children![0].status = status
    assert.equal(materialReadiness(knowledge(), t).ready, false, status)
  }
})
test('wrong knowledge and stale/missing/contradictory attempt observations are refused', () => {
  for (const patch of [
    { knowledge_id: 'other' }, { current_attempt: 0 }, { current_attempt: 1 },
    { latest_attempt: 3 }, { parse_status: 'processing' },
  ]) assert.equal(materialReadiness(knowledge(), { ...trace(), ...patch }).ready, false)
  const old = trace(); (old.trace!.children![0] as any).attempt = 1
  assert.equal(materialReadiness(knowledge(), old).ready, false)
  const foreign = trace(); (foreign.trace!.children![0] as any).knowledge_id = 'other'
  assert.equal(materialReadiness(knowledge(), foreign).ready, false)
  const duplicate = trace(); duplicate.trace!.children!.push(duplicate.trace!.children![0])
  assert.equal(materialReadiness(knowledge(), duplicate).ready, false)
})
test('upload acceptance does not imply binding or a search hit', async () => {
  const { model, calls } = await uploaded()
  assert.equal(model.rows.length, 1)
  assert.equal(model.rows[0].asset, undefined)
  assert.deepEqual(model.rows[0].hits, [])
  assert(!calls.some(call => call.action === 'bind' || call.action === 'search'))
  assert.match(model.rows[0].notice, /文件已上传/)
})
test('duplicate uploads reuse a single row and the original binding key', async () => {
  const f = await uploaded()
  f.ports.upload = async () => ({ knowledge: knowledge(), reused: true })
  await f.model.upload('kb-a', file)
  assert.equal(f.model.rows.length, 1)
  assert.equal(f.model.rows[0].ref.key, 'same-key')
  assert.match(f.model.rows[0].notice, /复用/)
})
test('a wrong-KB duplicate/upload response is not stored or bound', async () => {
  const f = fixture()
  f.ports.upload = async () => ({ knowledge: { ...knowledge(), knowledge_base_id: 'foreign' }, reused: true })
  await f.model.upload('kb-a', file)
  assert.equal(f.model.rows.length, 0)
  assert.match(f.model.uploadError, /不匹配/)
})
test('upload denied or response lost is not reported as successful project binding', async () => {
  const f = fixture()
  f.ports.upload = async () => { throw Error('403 synthetic denial') }
  await f.model.upload('kb-a', file)
  assert.equal(f.model.rows.length, 0)
  assert.match(f.model.uploadError, /未确认项目绑定/)
})
test('accepted upload is retained when the subsequent status/permission read fails', async () => {
  const f = fixture()
  f.ports.observe = async () => { throw Error('read unavailable') }
  await f.model.upload('kb-a', file)
  assert.equal(f.model.rows.length, 1)
  assert.equal(f.model.rows[0].indexReady, false)
  assert.equal(f.model.rows[0].knowledge, undefined)
  assert.match(f.model.rows[0].readError, /unavailable/)
})
test('binding failure keeps the upload and reuses the same idempotency key; successful rebind is a read-only no-op', async () => {
  const f = await uploaded()
  let attempts = 0
  const keys: string[] = []
  f.ports.bind = async (_p, _k, key) => {
    keys.push(key)
    if (++attempts === 1) throw Error('synthetic lost response')
    f.observation.assets = [asset()]
    return asset()
  }
  const row = f.model.rows[0]
  await f.model.bind(row)
  assert.match(row.actionError, /same|幂等键/)
  assert.equal(f.model.rows.length, 1)
  await f.model.bind(row)
  assert.deepEqual(keys, ['same-key', 'same-key'])
  assert.equal(row.asset?.id, 'asset-a')
  await f.model.bind(row)
  assert.equal(keys.length, 2)
})
test('binding always re-reads current state; a previously ready row cannot bypass a failed index', async () => {
  const f = await uploaded()
  f.observation.trace.trace!.children![0].status = 'failed'
  await f.model.bind(f.model.rows[0])
  assert(!f.calls.some(call => call.action === 'bind'))
  assert.equal(f.model.rows[0].indexReady, false)
})
test('foreign binding responses are rejected without claiming success', async () => {
  const f = await uploaded()
  f.ports.bind = async () => ({ ...asset(), project_id: 'other-project' })
  await f.model.bind(f.model.rows[0])
  assert.match(f.model.rows[0].actionError, /不属于/)
  assert.equal(f.model.rows[0].asset, undefined)
})
test('search uses ONLY the current project and freshly read bound asset', async () => {
  const f = await uploaded()
  await f.model.bind(f.model.rows[0])
  f.model.rows[0].query = 'synthetic'
  await f.model.search(f.model.rows[0])
  assert.deepEqual(f.calls.find(call => call.action === 'search')?.args, ['project-a', 'synthetic', 'asset-a'])
  assert.equal(f.model.rows[0].hits.length, 1)
})
test('unbound material cannot cause any project retrieval request', async () => {
  const f = await uploaded()
  f.model.rows[0].query = 'synthetic'
  await f.model.search(f.model.rows[0])
  assert(!f.calls.some(call => call.action === 'search'))
})
test('cross-project, wrong-asset and unavailable search results never render', async () => {
  for (const patch of [{ project_id: 'foreign' }, { asset_id: 'foreign' }, { status: 'stale' as const }]) {
    const f = await uploaded(); await f.model.bind(f.model.rows[0])
    f.ports.search = async () => [{ ...source(), ...patch }]
    f.model.rows[0].query = 'synthetic'
    await f.model.search(f.model.rows[0])
    assert.equal(f.model.rows[0].hits.length, 0)
    assert.match(f.model.rows[0].actionError, /拒绝展示/)
  }
})
test('read failure clears sensitive title, previous search excerpts and readiness, not an independent submit error', async () => {
  const f = await uploaded(); const row = f.model.rows[0]
  row.hits = [source()]; row.actionError = 'independent submit failure'
  f.ports.observe = async () => { throw Error('permission removed') }
  await f.model.refresh(row)
  assert.equal(row.knowledge, undefined)
  assert.deepEqual(row.hits, [])
  assert.equal(row.indexReady, false)
  assert.equal(row.actionError, 'independent submit failure')
})
test('late successful read cannot clear a newer failure or restore previous permissions', async () => {
  const f = await uploaded(); const row = f.model.rows[0]
  const old = deferred<MaterialObservation>()
  let count = 0
  f.ports.observe = async () => { if (++count === 1) return old.promise; throw Error('new permission denial') }
  const first = f.model.refresh(row)
  await f.model.refresh(row)
  old.resolve(structuredClone(f.observation))
  await first
  assert.equal(row.indexReady, false)
  assert.match(row.readError, /new permission denial/)
})
test('references survive refresh but title/content/auth are never persisted, and fresh reads are mandatory', async () => {
  const cache = new Map<string, string>()
  const storage = { getItem: (key: string) => cache.get(key) || null, setItem: (key: string, value: string) => { cache.set(key, value) } }
  const f = await uploaded(fixture(storage))
  assert(![...cache.values()].join('').includes('Synthetic'))
  const restored = fixture(storage).model
  assert.equal(restored.rows.length, 1)
  assert.equal(restored.rows[0].knowledge, undefined)
  assert.equal(restored.rows[0].indexReady, false)
  assert.equal(fixture(storage, 'other-user:tenant-a').model.rows.length, 0)
  assert.equal(new ProjectMaterialIntake('other-project', 'user-a:tenant-a', f.ports, storage).rows.length, 0)
})
test('malformed storage and write failures are recoverable, not false persistence success', async () => {
  const storage = { getItem: () => 'not-json', setItem: () => { throw Error('quota') } }
  const f = await uploaded(fixture(storage))
  assert.equal(f.model.rows.length, 1)
  assert.match(f.model.storageWarning, /无法保存/)
})
test('disposed old-project upload cannot populate the new context or start follow-up reads', async () => {
  const f = fixture()
  const pending = deferred<{ knowledge: ReturnType<typeof knowledge>; reused: boolean }>()
  f.ports.upload = () => pending.promise
  const request = f.model.upload('kb-a', file)
  f.model.dispose()
  pending.resolve({ knowledge: knowledge(), reused: false })
  await request
  assert.equal(f.model.rows.length, 0)
  assert(!f.calls.some(call => call.action === 'observe'))
})
test('disposed in-flight retrieval never returns snippets to an old project view', async () => {
  const f = await uploaded(); await f.model.bind(f.model.rows[0])
  const pending = deferred<Source[]>()
  f.ports.search = () => pending.promise
  const row = f.model.rows[0]; row.query = 'synthetic'
  const request = f.model.search(row)
  await Promise.resolve(); await Promise.resolve()
  f.model.dispose()
  pending.resolve([source()])
  await request
  assert.deepEqual(row.hits, [])
})

