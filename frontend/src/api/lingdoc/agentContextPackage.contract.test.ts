import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import ts from 'typescript'
import type { AgentContextPackage } from './agentApi'

/**
 * 上下文包的前端类型是对服务端契约的一份手抄，这条守护让它不再是「盲抄」。
 *
 * 真相在服务端：`internal/lingdoc/workspacecore/agent_research_model.go` 的
 * `AgentContextPackage` 决定 JSON 里有哪些键、哪些键可以缺席。前端类型只是那份
 * 形状的读者，读漏一个键不会报错，只会让界面拿不到服务端明明给出来的字段
 * （#435 的规则集基线就是这样漏掉的）。
 *
 * 所以这里逐字段比对两件事：
 *   1. 字段集合相等——服务端加了字段而前端没跟，或者前端凭空多写一个，
 *      都算漂移；
 *   2. 可选性相等——`json:"x,omitempty"` 对应前端的 `x?:`，两者必须同步。
 *      可选性差异只允许出现在 `OPTIONALITY_EXEMPTIONS` 里——白名单是空的，
 *      意味着「默认没有例外」；真要放宽，得在这里写下一个名字，而不是让
 *      比对悄悄变松。
 *
 * 前端目录可能被单独 checkout（只带 `frontend/`），读不到服务端源码时**显式
 * skip**，绝不静默通过——静默通过等于没有这条守护。
 */

type ContextField = { name: string; optional: boolean }

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../../../..')
const SERVER_MODEL = resolve(REPO_ROOT, 'internal/lingdoc/workspacecore/agent_research_model.go')
const CLIENT_API = resolve(dirname(fileURLToPath(import.meta.url)), 'agentApi.ts')
const SERVER_AVAILABLE = existsSync(SERVER_MODEL)

/** 允许可选性不同的字段：默认没有例外，放宽必须显式登记。 */
const OPTIONALITY_EXEMPTIONS: ReadonlySet<string> = new Set<string>()

/** 服务端结构体的字段名与 `omitempty` 语义。 */
function serverContextPackageFields(source = readFileSync(SERVER_MODEL, 'utf8')): ContextField[] {
  const block = source.match(/type AgentContextPackage struct \{[\s\S]*?\n\}/)
  if (!block) throw new Error('服务端契约里没有找到 AgentContextPackage 结构体')

  const fields: ContextField[] = []
  for (const rawLine of block[0].split('\n')) {
    const line = rawLine.replace(/\/\/.*$/, '').trim()
    if (!line || line.startsWith('type ') || line === '}') continue
    const declaration = line.match(/^(\w+)\s+(.+)$/)
    if (!declaration) throw new Error(`服务端的字段声明看不懂：${line}`)
    const tag = declaration[2].match(/json:"([^"]*)"/)
    // 没有 json tag 的字段会以 Go 字段名出现在 JSON 里，那是另一种漂移，
    // 不能当成「没有这个字段」放过。
    if (!tag) throw new Error(`服务端字段 ${declaration[1]} 没有 json tag`)
    const [name, ...options] = tag[1].split(',')
    if (name === '-') continue
    if (!name) throw new Error(`服务端字段 ${declaration[1]} 的 json tag 没有名字`)
    fields.push({ name, optional: options.includes('omitempty') })
  }
  if (!fields.length) throw new Error('服务端 AgentContextPackage 没有解析到任何字段')
  return fields
}

/** 前端接口的字段名与 `?` 语义。 */
function clientContextPackageFields(source = readFileSync(CLIENT_API, 'utf8')): ContextField[] {
  const file = ts.createSourceFile(CLIENT_API, source, ts.ScriptTarget.ES2022, true)
  const declaration = file.statements.find(
    (statement): statement is ts.InterfaceDeclaration =>
      ts.isInterfaceDeclaration(statement) && statement.name.text === 'AgentContextPackage',
  )
  if (!declaration) throw new Error('前端 API 里没有找到 AgentContextPackage 接口')

  const fields: ContextField[] = []
  for (const member of declaration.members) {
    if (!ts.isPropertySignature(member) || !member.name) throw new Error('AgentContextPackage 里出现了非字段成员')
    fields.push({
      name: member.name.getText(file).replace(/^['"]|['"]$/g, ''),
      optional: Boolean(member.questionToken),
    })
  }
  if (!fields.length) throw new Error('前端 AgentContextPackage 没有解析到任何字段')
  return fields
}

/** 返回漂移清单；空数组表示两边逐字段一致。 */
function contextPackageDrift(
  server: ContextField[],
  client: ContextField[],
  exemptions: ReadonlySet<string> = OPTIONALITY_EXEMPTIONS,
): string[] {
  const drift: string[] = []
  const serverNames = new Set(server.map((field) => field.name))
  const clientNames = new Set(client.map((field) => field.name))

  for (const field of server) {
    if (!clientNames.has(field.name)) drift.push(`前端缺字段 ${field.name}`)
  }
  for (const field of client) {
    if (!serverNames.has(field.name)) drift.push(`前端多出字段 ${field.name}`)
  }
  for (const field of server) {
    const counterpart = client.find((candidate) => candidate.name === field.name)
    if (!counterpart || exemptions.has(field.name)) continue
    if (counterpart.optional !== field.optional) {
      drift.push(
        `字段 ${field.name} 可选性不一致：服务端${field.optional ? '可选' : '必填'}，前端${counterpart.optional ? '可选' : '必填'}`,
      )
    }
  }
  return drift
}

test('前端上下文包类型的字段集合与服务端契约逐字段相等', (t) => {
  if (!SERVER_AVAILABLE) {
    t.skip(`读不到服务端契约（${SERVER_MODEL}），跳过字段比对`)
    return
  }
  assert.deepEqual(
    contextPackageDrift(serverContextPackageFields(), clientContextPackageFields()),
    [],
    '前端 AgentContextPackage 与服务端契约漂移：两边必须一起改',
  )
})

test('规则集基线两个字段前端可读：与服务端同名', (t) => {
  if (!SERVER_AVAILABLE) {
    t.skip(`读不到服务端契约（${SERVER_MODEL}），跳过基线字段核对`)
    return
  }
  const client = clientContextPackageFields()
  const baseline = client.filter((field) => field.name === 'ruleset_id' || field.name === 'ruleset_version')
  assert.deepEqual(baseline, [
    { name: 'ruleset_id', optional: true },
    { name: 'ruleset_version', optional: true },
  ])
})

test('服务端多一个字段、前端没跟上时比对变红', () => {
  const drift = contextPackageDrift(
    [
      { name: 'project_id', optional: false },
      { name: 'ruleset_id', optional: true },
    ],
    [{ name: 'project_id', optional: false }],
  )
  assert.deepEqual(drift, ['前端缺字段 ruleset_id'])
})

test('前端凭空多写一个服务端没有的字段时比对变红', () => {
  const drift = contextPackageDrift(
    [{ name: 'project_id', optional: false }],
    [
      { name: 'project_id', optional: false },
      { name: 'rule_set_id', optional: true },
    ],
  )
  assert.deepEqual(drift, ['前端多出字段 rule_set_id'])
})

test('可选性漂移默认变红，只有显式登记才放宽', () => {
  const server: ContextField[] = [{ name: 'task_id', optional: true }]
  const client: ContextField[] = [{ name: 'task_id', optional: false }]
  assert.deepEqual(contextPackageDrift(server, client), [
    '字段 task_id 可选性不一致：服务端可选，前端必填',
  ])
  assert.deepEqual(contextPackageDrift(server, client, new Set(['task_id'])), [])
})

test('白名单只放宽可选性，不掩盖字段缺失', () => {
  const drift = contextPackageDrift([{ name: 'ruleset_version', optional: true }], [], new Set(['ruleset_version']))
  assert.deepEqual(drift, ['前端缺字段 ruleset_version'])
})

/**
 * 类型层断言：服务端给了规则集基线，前端的上下文包类型必须真的收得下。
 * `npm run type-check` 在编译期就会挡住这条，比运行时更早。
 */
const contextPackageWithBaseline: AgentContextPackage = {
  project_id: 'project-1',
  project_version: 3,
  spec_revision: 2,
  context_revision: 5,
  template_id: 'template-1',
  template_version: '4',
  ruleset_id: 'ruleset-1',
  ruleset_version: '7',
  source_refs: [],
  card_refs: [],
  selection_reasons: [],
  authorization_state: 'allowed',
  output_constraints: [],
  correlation_id: 'correlation-1',
}

test('上下文包类型收得下规则集基线，值原样可读', () => {
  assert.equal(contextPackageWithBaseline.ruleset_id, 'ruleset-1')
  assert.equal(contextPackageWithBaseline.ruleset_version, '7')
})
