import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const ts = require('typescript') as typeof import('typescript')
const source = fs.readFileSync(new URL('./runtimeControl.ts', import.meta.url), 'utf8')

interface RecordedCall {
  method: string
  url: string
  input?: Record<string, any>
  options?: { headers?: Record<string, string> }
}

// The client cannot be imported directly here: it pulls in the aliased
// request helper, which this test runtime cannot resolve.  So the module is
// transpiled and handed a recording transport instead -- the same seam the
// change-set client test uses.
function harness(result?: unknown) {
  const calls: RecordedCall[] = []
  const exports: Record<string, (...args: any[]) => any> = {}
  const record = (method: string) => async (url: string, input?: Record<string, any>, options?: RecordedCall['options']) => {
    calls.push({ method, url, input, options })
    return result ?? { data: null, request_id: 'synthetic.test', meta: { replayed: false, refresh_required: false } }
  }
  const transport = { get: record('get'), post: record('post'), put: record('put') }
  const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  vm.runInNewContext(compiled, { exports, require(name: string) { assert.equal(name, '@/utils/request'); return transport } })
  return { api: exports, calls }
}

test('every runtime endpoint keeps its exact path and encoding', async () => {
  const { api, calls } = harness()
  const project = 'project/other'
  const projectPath = '/api/v1/lingdoc/projects/project%2Fother'
  await api.listRuntimeControls(project)
  await api.controlRuntime(project, { expected_version: 1, scope: { kind: 'project', value: 'p' }, action: 'pause', reason: 'synthetic.pause' }, 'key-controls')
  await api.listRuntimeEvents(project, 'corr?other')
  await api.getRuntimeMetrics(project)
  await api.listRuntimeAlerts(project)
  await api.saveRuntimeAlertRule(project, { signal: 'dependency/failed', severity: 'P1', action: 'pause', responsible_id: 'owner' }, 'key-alert-rule')
  assert.deepEqual(calls.map(call => call.url), [
    `${projectPath}/runtime/controls`,
    `${projectPath}/runtime/controls`,
    `${projectPath}/runtime/events?correlation_id=corr%3Fother`,
    `${projectPath}/runtime/metrics`,
    `${projectPath}/runtime/alerts`,
    `${projectPath}/runtime/alert-rules`,
  ])
})

test('runtime writes carry an idempotency key and runtime reads carry no request body', async () => {
  const { api, calls } = harness()
  await api.listRuntimeControls('p')
  await api.controlRuntime('p', { expected_version: 1, scope: { kind: 'project', value: 'p' }, action: 'pause', reason: 'synthetic.pause' }, 'key-controls')
  await api.listRuntimeEvents('p')
  await api.saveRuntimeAlertRule('p', { signal: 'dependency/failed', severity: 'P1', action: 'pause', responsible_id: 'owner' }, 'key-alert-rule')
  const reads = calls.filter(call => call.method === 'get')
  const writes = calls.filter(call => call.method === 'post')
  assert.equal(reads.length, 2)
  for (const call of reads) assert.equal(call.input, undefined)
  assert.equal(writes.length, 2)
  for (const call of writes) assert.match(String(call.options?.headers?.['Idempotency-Key']), /^key-/)
})

test('runtime recovery sends existing artifact references and leaves validation to the server', async () => {
  const { api, calls } = harness()
  const input = { expected_version: 4, scope: { kind: 'project', value: 'p' }, action: 'resume', reason: 'synthetic.recovery', snapshot_id: 'snapshot-4', export_id: 'export-4' }
  await api.controlRuntime('p', input, 'recovery-original-001')
  assert.equal(calls[0].input!.recovery_verified, undefined)
  assert.equal(calls[0].input, input)
  assert.equal(calls[0].options?.headers?.['Idempotency-Key'], 'recovery-original-001')
})

test('all runtime scope kinds preserve the exact proposal without asserting permission or recovery', async () => {
  const { api, calls } = harness()
  for (const scope of [{ kind: 'tenant', value: '7' }, { kind: 'project', value: 'p' }, { kind: 'template_version', value: '1' }, { kind: 'ruleset_hash', value: 'actual.hash' }, { kind: 'runtime_mode', value: 'mock' }]) {
    await api.controlRuntime('p', { expected_version: 9, scope, action: 'pause', reason: 'actual.report' }, 'current-scope-key')
  }
  assert.equal(calls.length, 5)
  for (const call of calls) {
    assert.equal(call.input!.expected_version, 9)
    assert.equal(call.input!.recovery_verified, undefined)
    assert.equal(call.input!.permission_granted, undefined)
    assert.equal(call.options?.headers?.['Idempotency-Key'], 'current-scope-key')
  }
})

test('the runtime client passes the server envelope through untouched', async () => {
  const response = { data: [{ id: 'control-1', status: 'PAUSED', scope: { kind: 'project', value: 'p' } }], request_id: 'actual.request', meta: { replayed: false, refresh_required: false } }
  const { api } = harness(response)
  const result = await api.listRuntimeControls('p')
  assert.equal(result, response)
  assert.equal(result.data[0].status, 'PAUSED')
  assert.equal(result.data[0].scope.kind, 'project')
  assert.equal(result.request_id, 'actual.request')
  assert.equal(result.meta.replayed, false)
})
