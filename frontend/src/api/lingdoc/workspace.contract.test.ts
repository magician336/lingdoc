import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

/**
 * 变更集状态的前端类型与对外契约必须覆盖同一组状态。
 *
 * 变更集状态在四处被声明：Go 侧的词表、对外契约的枚举、数据库的取值约束、
 * 以及这里的前端联合类型。Go 侧与数据库的漂移由 Go 测试守住，契约与前端
 * 的漂移由这条守住——否则前端会渲染一个服务端永远不会给出的状态，或者
 * 对一个服务端已经给出的状态没有任何分支。
 *
 * 前端目录可能被单独 checkout（只带 `frontend/`），所以读不到仓库根时
 * **显式 skip**，绝不静默通过——静默通过等于没有这条守护。
 */
const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../../../..')
const CONTRACT = resolve(REPO_ROOT, 'docs/08-本轮实施方案/contracts/openapi.json')
const WORKSPACE_API = resolve(dirname(fileURLToPath(import.meta.url)), 'workspace.ts')
const CONTRACT_AVAILABLE = existsSync(CONTRACT)

function contractChangeSetStatuses(): string[] {
  const contract = JSON.parse(readFileSync(CONTRACT, 'utf8'))
  const schema = contract?.components?.schemas?.ChangeSet
  if (!schema) throw new Error('契约里没有 ChangeSet schema')
  const values = schema?.properties?.status?.enum
  if (!Array.isArray(values)) throw new Error('契约的 ChangeSet 没有 status 枚举')
  return values.map((value: unknown) => {
    if (typeof value !== 'string') throw new Error(`变更集状态枚举里出现了非字符串取值 ${String(value)}`)
    return value
  })
}

function frontendChangeSetStatuses(): string[] {
  const source = readFileSync(WORKSPACE_API, 'utf8')
  const block = source.match(/export interface ChangeSet \{[\s\S]*?\n\}/)
  if (!block) throw new Error('前端 API 里没有找到 ChangeSet 接口')
  const status = block[0].match(/^\s*status:\s*(.+)$/m)
  if (!status) throw new Error('ChangeSet 接口里没有 status 声明')
  const values = status[1].match(/'([^']*)'/g)
  if (!values) throw new Error('ChangeSet.status 不是一个字符串联合类型')
  return values.map((value) => value.slice(1, -1))
}

test('变更集状态的前端联合类型与对外契约覆盖同一组状态', (t) => {
  if (!CONTRACT_AVAILABLE) {
    t.skip(`读不到对外契约（${CONTRACT}），跳过漂移核对`)
    return
  }
  const contract = contractChangeSetStatuses()
  const frontend = frontendChangeSetStatuses()
  assert.deepEqual(
    [...frontend].sort(),
    [...contract].sort(),
    '前端联合类型与契约枚举不一致：新增或删除一个状态必须同时改这两处',
  )
})
