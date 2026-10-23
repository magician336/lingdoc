import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import {
  RUNTIME_CATEGORY_LABELS,
  RUNTIME_STATUS_LABELS,
  expandRuntimeCounts,
  projectRuntimeMetrics,
  usedRuntimeCategories,
  usedRuntimeStatuses,
} from './runtimeMetrics'

// The server owns the vocabulary of runtime events.  This test reads the
// server source so the front-end grouping table cannot drift away from it
// unnoticed.  A change to the server vocabulary fails here instead of
// silently turning metrics into empty panels.
const serverSource = readFileSync(new URL('../../../../internal/lingdoc/workspacecore/runtime_control.go', import.meta.url), 'utf8')

function serverVocabulary() {
  const categories = serverSource.match(/slices\.Contains\(\[\]string\{([^}]*)\},\s*event\.Category\)/)
  const statuses = serverSource.match(/slices\.Contains\(\[\]string\{([^}]*)\},\s*event\.Status\)/)
  assert.ok(categories, 'the server runtime event category whitelist must stay readable')
  assert.ok(statuses, 'the server runtime event status whitelist must stay readable')
  const read = (matched: RegExpMatchArray) => [...matched[1].matchAll(/"([^"]+)"/g)].map(item => item[1])
  return { categories: read(categories), statuses: read(statuses) }
}

test('the server vocabulary is really read, not silently skipped', () => {
  const vocabulary = serverVocabulary()
  assert.ok(vocabulary.categories.length > 0, 'the server category whitelist must not be empty')
  assert.ok(vocabulary.statuses.length > 0, 'the server status whitelist must not be empty')
})

test('the front-end grouping table only uses vocabulary the server can produce', () => {
  const vocabulary = serverVocabulary()
  for (const category of usedRuntimeCategories()) {
    assert.ok(vocabulary.categories.includes(category), `grouping category ${category} is not in the server whitelist`)
  }
  for (const status of usedRuntimeStatuses()) {
    assert.ok(vocabulary.statuses.includes(status), `grouping status ${status} is not in the server whitelist`)
  }
  for (const category of Object.keys(RUNTIME_CATEGORY_LABELS)) {
    assert.ok(vocabulary.categories.includes(category), `label category ${category} is not in the server whitelist`)
  }
  for (const status of Object.keys(RUNTIME_STATUS_LABELS)) {
    assert.ok(vocabulary.statuses.includes(status), `label status ${status} is not in the server whitelist`)
  }
})

test('the grouped metrics project the server counts and leave unmeasured items unobserved', () => {
  const items = projectRuntimeMetrics({ 'task/failed': 3, 'task/retry': 2, 'download/rejected': 1, 'dependency/unavailable': 4, 'export/loss': 1 })
  const byKey = new Map(items.map(item => [item.key, item]))
  assert.equal(byKey.get('async_failed')?.total, 3)
  assert.equal(byKey.get('async_retry')?.total, 2)
  assert.equal(byKey.get('stale')?.total, null)
  assert.equal(byKey.get('duplicate')?.total, null)
  assert.equal(byKey.get('permission_rejected')?.total, 1)
  assert.equal(byKey.get('download_reauth')?.total, 1)
  assert.equal(byKey.get('dependency_unavailable')?.total, 4)
  assert.equal(byKey.get('export_loss')?.total, 1)
  assert.equal(byKey.get('preflight')?.total, null)
})

test('a measured zero stays zero while an absent measurement stays unobserved', () => {
  const items = projectRuntimeMetrics({ 'task/failed': 0 })
  const failed = items.find(item => item.key === 'async_failed')
  assert.equal(failed?.total, 0)
  assert.deepEqual(failed?.keys, ['task/failed'])
  const stale = items.find(item => item.key === 'stale')
  assert.equal(stale?.total, null)
  assert.deepEqual(stale?.keys, [])
})

test('download re-authentication stays a subset of permission rejection and revocation', () => {
  const items = projectRuntimeMetrics({ 'download/rejected': 2, 'download/revoked': 1 })
  const byKey = new Map(items.map(item => [item.key, item]))
  assert.equal(byKey.get('permission_rejected')?.total, 2)
  assert.equal(byKey.get('revoked')?.total, 1)
  assert.equal(byKey.get('download_reauth')?.total, 3)
  assert.deepEqual(byKey.get('download_reauth')?.keys, ['download/rejected', 'download/revoked'])
  assert.match(String(byKey.get('download_reauth')?.note), /下集/)
})

test('a metric the server never measures reads as unavailable rather than zero', () => {
  const items = projectRuntimeMetrics({ 'request/success': 9 })
  const allowed = items.find(item => item.key === 'authorization_allow')
  assert.ok(allowed)
  assert.equal(allowed.total, null)
  assert.match(String(allowed.unavailableReason), /未提供/)
  // The wording keeps the G5 attribution so the reader knows which gap this is.
  assert.match(allowed.label, /G5/)
})

test('a denied authorization is counted while the allowed side stays unavailable', () => {
  const items = projectRuntimeMetrics({ 'permission/deny': 4 })
  const denied = items.find(item => item.key === 'authorization_denial')
  const allowed = items.find(item => item.key === 'authorization_allow')
  assert.equal(denied?.total, 4)
  assert.deepEqual(denied?.keys, ['permission/deny'])
  assert.equal(allowed?.total, null)
  assert.match(String(allowed?.unavailableReason), /未提供/)
})

test('a successful preflight is not counted as a preflight problem', () => {
  const items = projectRuntimeMetrics({ 'preflight/success': 5, 'preflight/failed': 2 })
  const preflight = items.find(item => item.key === 'preflight')
  assert.equal(preflight?.total, 2)
  assert.deepEqual(preflight?.keys, ['preflight/failed'])
})

test('a rejected preflight is a problem and also a permission rejection', () => {
  const items = projectRuntimeMetrics({ 'preflight/rejected': 1 })
  const preflight = items.find(item => item.key === 'preflight')
  const rejected = items.find(item => item.key === 'permission_rejected')
  assert.equal(preflight?.total, 1)
  assert.equal(rejected?.total, 1)
})

test('the expanded table keeps the raw server key and labels known vocabulary', () => {
  const rows = expandRuntimeCounts({ 'task/failed': 3, 'request/success': 1, 'unstructured': 2 })
  assert.deepEqual(rows.map(row => row.key), ['request/success', 'task/failed', 'unstructured'])
  assert.equal(rows[1].categoryLabel, '异步任务')
  assert.equal(rows[1].statusLabel, '失败')
  assert.equal(rows[1].count, 3)
  assert.equal(rows[2].categoryLabel, '未分类')
  assert.equal(rows[2].statusLabel, '未标注')
})

test('an empty or missing count set never produces a measured zero', () => {
  for (const counts of [undefined, null, {}]) {
    const items = projectRuntimeMetrics(counts)
    assert.equal(items.length > 0, true)
    for (const item of items) assert.equal(item.total, null)
    assert.equal(expandRuntimeCounts(counts).length, 0)
  }
})
