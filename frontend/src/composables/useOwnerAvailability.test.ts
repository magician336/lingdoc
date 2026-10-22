import assert from 'node:assert/strict'
import test from 'node:test'
import fs from 'node:fs'
import vm from 'node:vm'
import { createRequire } from 'node:module'
const require = createRequire(import.meta.url)
const ts = require('typescript')

/**
 * Owner 可用性事实的界面契约。
 *
 * 这个组合式函数在运行时引入了带路径别名的接口客户端，直接 import 在这条测试
 * 的运行环境里解析不了别名——所以这里转译后注入，仓库里已有同样的做法
 * （ImpactOpsPanel.test.js 加载它的主体模块）。
 *
 * 断言的是**界面看得到的事实**：冻结时入口置灰、原因与恢复指引都在；
 * 没有冻结、或者读出口答不出来时，界面不谎称有冻结。
 */
const SOURCE = new URL('./useOwnerAvailability.ts', import.meta.url)

type PermissionAnswer = { data: { reason_code: string } }
type AvailabilityAnswer = { data: { owner_user_id: string; available: boolean; reason_code: string; recovery_paths: string[] } }

function load(
  read: (projectId: string, capability: string) => Promise<PermissionAnswer>,
  availability: (projectId: string) => Promise<AvailabilityAnswer> = async () => ({
    data: { owner_user_id: 'owner-user', available: true, reason_code: 'allowed', recovery_paths: [] },
  }),
) {
  const exports: Record<string, unknown> = {}
  const vue = {
    ref: (value: unknown) => ({ value }),
    computed: (fn: () => unknown) => ({ get value() { return fn() } }),
  }
  const permissions = { getEffectivePermission: read }
  const collaboration = { getOwnerAvailability: availability }
  const js = ts.transpileModule(fs.readFileSync(SOURCE, 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText
  vm.runInNewContext(js, {
    exports,
    require(name: string) {
      if (name === 'vue') return vue
      if (name === '@/api/lingdoc/permissions') return permissions
      if (name === '@/api/lingdoc/collaboration') return collaboration
      throw new Error(`unexpected import ${name}`)
    },
    console,
  })
  return exports as {
    OWNER_DECISION_UNAVAILABLE_TEXT: string
    useOwnerAvailability: (
      projectId: () => string | undefined,
      options?: { capability?: string; reasonText?: string },
    ) => {
      frozen: { value: boolean }
      reasonCode: { value: string }
      reason: { value: string }
      recovery: { value: string }
      ownerUserId: { value: string }
      ownerUnavailable: { value: boolean }
      recoveryPaths: { value: string[] }
      recoveryTexts: { value: string[] }
      noticeReason: { value: string }
      noticeRecovery: { value: string }
      refresh: () => Promise<void>
    }
  }
}

test('a frozen project disables the Owner-only entries and says why', async () => {
  const asked: string[] = []
  const subject = load(async (projectId, capability) => {
    asked.push(`${projectId}/${capability}`)
    return { data: { reason_code: 'owner_unavailable' } }
  })
  // The Owner-only entries name the decisions they cover, so they pass their own
  // sentence in; the composable itself only holds the generic one.
  const availability = subject.useOwnerAvailability(() => 'project-1', {
    reasonText: subject.OWNER_DECISION_UNAVAILABLE_TEXT,
  })
  await availability.refresh()
  assert.deepEqual(asked, ['project-1/manage'], 'one read answers for every Owner-only entry')
  assert.equal(availability.frozen.value, true)
  assert.equal(availability.reasonCode.value, 'owner_unavailable')
  assert.match(availability.reason.value, /Owner/, 'the reason names the Owner')
  assert.match(availability.reason.value, /立项/, 'the reason names the activation entry')
  assert.match(availability.reason.value, /模板/, 'the reason names the template upgrade entry')
  assert.match(availability.recovery.value, /恢复|转让/, 'the recovery guidance names a way back')
})

test('a project that is not frozen leaves the Owner-only entries alone', async () => {
  const subject = load(async () => ({ data: { reason_code: 'capability_or_scope_denied' } }))
  const availability = subject.useOwnerAvailability(() => 'project-1')
  await availability.refresh()
  assert.equal(availability.frozen.value, false)
  assert.equal(availability.reason.value, '')
  assert.equal(availability.recovery.value, '')
})

test('the read outlet failing leaves the Owner-only entries alone', async () => {
  const subject = load(async () => { throw new Error('synthetic read failure') })
  const availability = subject.useOwnerAvailability(() => 'project-1')
  await availability.refresh()
  assert.equal(availability.frozen.value, false, 'the interface never claims a freeze it could not read')
  assert.equal(availability.reasonCode.value, '')
})

test('no project means no freeze', async () => {
  const subject = load(async () => ({ data: { reason_code: 'owner_unavailable' } }))
  const availability = subject.useOwnerAvailability(() => undefined)
  await availability.refresh()
  assert.equal(availability.frozen.value, false)
})

test('the project level outlet names the Owner and the ways back', async () => {
  const asked: string[] = []
  const subject = load(
    async () => ({ data: { reason_code: 'owner_unavailable' } }),
    async (projectId) => {
      asked.push(projectId)
      return {
        data: {
          owner_user_id: 'owner-user', available: false, reason_code: 'owner_unavailable',
          recovery_paths: ['restore_tenant_identity', 'tenant_owner_nomination'],
        },
      }
    },
  )
  const availability = subject.useOwnerAvailability(() => 'project-1')
  await availability.refresh()
  assert.deepEqual(asked, ['project-1'], 'the project level outlet answers for the project')
  assert.equal(availability.ownerUserId.value, 'owner-user')
  assert.equal(availability.ownerUnavailable.value, true)
  assert.deepEqual(availability.recoveryPaths.value, ['restore_tenant_identity', 'tenant_owner_nomination'])
  assert.equal(availability.recoveryTexts.value.length, 2, 'every way back the outlet names has text the interface holds')
  assert.match(availability.noticeReason.value, /owner-user/, 'the banner names the Owner who is missing')
  assert.match(availability.noticeRecovery.value, /恢复/, 'the banner says how the project gets back to work')
})

test('a healthy project shows no project level banner', async () => {
  const subject = load(
    async () => ({ data: { reason_code: 'allowed' } }),
    async () => ({ data: { owner_user_id: 'owner-user', available: true, reason_code: 'allowed', recovery_paths: [] } }),
  )
  const availability = subject.useOwnerAvailability(() => 'project-1')
  await availability.refresh()
  assert.equal(availability.ownerUnavailable.value, false)
  assert.equal(availability.noticeReason.value, '')
  assert.equal(availability.noticeRecovery.value, '')
})

test('the project level outlet failing leaves the project level banner alone', async () => {
  const subject = load(
    async () => ({ data: { reason_code: 'owner_unavailable' } }),
    async () => { throw new Error('synthetic read failure') },
  )
  const availability = subject.useOwnerAvailability(() => 'project-1')
  await availability.refresh()
  assert.equal(availability.ownerUnavailable.value, false, 'the interface never claims a freeze it could not read')
  assert.equal(availability.noticeReason.value, '')
})
