import assert from 'node:assert/strict'
import fs from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const ts = require('typescript')

/**
 * 归档客户端与对外契约。
 *
 * 断言的是**调用方能看见的事实**：归档请求发到哪里、带什么请求体、用哪个
 * 幂等键；以及对外契约发布的那一份形状。客户端与契约各写一遍形状时，漂移
 * 就会变成线上的一次失败，所以这里把它们钉在一起。
 */

const CONTRACT = new URL('../../../../docs/08-本轮实施方案/contracts/openapi.json', import.meta.url)
const CLIENT = new URL('./projectArchive.ts', import.meta.url)
const WORKSPACE_CLIENT = new URL('./workspace.ts', import.meta.url)

type RecordedCall = { url: string; body: unknown; config: { headers: Record<string, string> } }

function loadClient() {
  const calls: RecordedCall[] = []
  const exports: Record<string, unknown> = {}
  const js = ts.transpileModule(fs.readFileSync(CLIENT, 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText
  vm.runInNewContext(js, {
    exports,
    require(name: string) {
      if (name === '@/utils/request') {
        return {
          post(url: string, body: unknown, config: RecordedCall['config']) {
            calls.push({ url, body, config })
            return Promise.resolve({ data: { id: 'project-1', status: 'archived' } })
          },
        }
      }
      throw new Error(`unexpected import ${name}`)
    },
    console,
  })
  return {
    calls,
    archiveProject: (exports as {
      archiveProject: (id: string, version: number, key: string) => unknown
    }).archiveProject,
  }
}

test('the archive client sends the project version and the idempotency key', async () => {
  const client = loadClient()
  await client.archiveProject('project 1', 7, 'archive-key')
  assert.equal(client.calls.length, 1)
  assert.equal(client.calls[0].url, '/api/v1/lingdoc/projects/project%201/archive')
  assert.equal((client.calls[0].body as { expected_project_version: number }).expected_project_version, 7)
  assert.equal(client.calls[0].config.headers['Idempotency-Key'], 'archive-key')
})

test('the published contract names the archive operation and its required inputs', () => {
  const spec = JSON.parse(fs.readFileSync(CONTRACT, 'utf8'))
  const operation = spec.paths['/projects/{projectId}/archive']?.post
  assert.ok(operation, 'the contract must publish the archive endpoint')
  assert.equal(operation.operationId, 'archiveProject')

  const key = operation.parameters.find((parameter: { in: string; name: string }) => parameter.in === 'header' && parameter.name === 'Idempotency-Key')
  assert.equal(key.required, true, 'the archive endpoint requires an idempotency key')
  assert.ok(key.schema.minLength >= 8)

  const request = spec.components.schemas[operation.requestBody.content['application/json'].schema.$ref.split('/').pop()]
  assert.deepEqual(request.required, ['expected_project_version'])
  assert.equal(request.additionalProperties, false)

  assert.equal(operation.responses['200'].content['application/json'].schema.$ref, '#/components/schemas/ProjectResponse')
  assert.ok(operation.responses['404'], 'the archive endpoint publishes the refusal it can answer with')
  assert.ok(operation.responses['409'], 'a stale project version is a conflict')
  assert.ok(operation.responses['422'], 'a draft project cannot be archived')
})

test('the project status vocabulary covers the archived terminal state on both sides', () => {
  const spec = JSON.parse(fs.readFileSync(CONTRACT, 'utf8'))
  const contract = spec.components.schemas.Project.properties.status.enum as string[]

  const source = fs.readFileSync(WORKSPACE_CLIENT, 'utf8')
  const block = source.match(/export interface Project \{[\s\S]*?\n\}/)
  assert.ok(block, 'the frontend client must declare the project shape')
  const status = block![0].match(/^\s*status:\s*(.+)$/m)
  assert.ok(status, 'the project shape must declare its status')
  const frontend = (status![1].match(/'([^']*)'/g) ?? []).map((value) => value.slice(1, -1))

  assert.ok(contract.includes('archived'), 'the contract must be able to publish an archived project')
  assert.deepEqual([...frontend].sort(), [...contract].sort(),
    'the frontend union type and the contract enum must cover the same project states')
})
