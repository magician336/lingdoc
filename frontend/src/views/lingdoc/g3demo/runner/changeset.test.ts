/**
 * G3 验收台 · 变更集生命周期链的执行器单测。
 *
 * 用假客户端跑完整条链，钉住四件事：
 * 1. **调用顺序**：读影响评估严格早于应用变更。
 * 2. **请求体形状**：`/assess`、`/reverse` 严格是 `{}`；`/reassess` 完全空体。
 * 3. **四条判据的判定**：AC-G3-01 / 02 / 04 / 11 的结论来自响应事实，不是写死的。
 * 4. **非 Owner**：应用与拒绝都返回明确错误码。
 */

import assert from 'node:assert/strict'
import test from 'node:test'
import { createFakeClient } from './fakeHttp'
import { createRunContext } from './runContext'
import {
  G3_CS_STEPS,
  readImpactAssessment,
  reassessChangeSet,
  runChangeSetChain,
  unaffectedReason,
  type G3ImpactAssessmentFact,
} from './changeset'

const PROJECT = 'p1'
const QUESTION = 'c-question'
const METHOD = 'c-method'
const TENANT = '10055'

function basePath(suffix: string): string {
  return `/api/v1/lingdoc/projects/${PROJECT}${suffix}`
}

/** 造一个项目上下文（章节已就位）。 */
function context() {
  return createRunContext({ runId: 'g3r-cs-1', accountEmail: 'g3demo-x@example.com' })
}

/** 影响评估的原始响应：只含 method 章节。 */
const ASSESSMENT_RAW = {
  change_set_id: 'cs-a',
  project_id: PROJECT,
  context_revision: 1,
  input_hash: 'hash-1',
  objects: [
    {
      ref: { kind: 'ChapterVersion', id: METHOD, version: 'v-m1' },
      state: 'CURRENT',
      source_ids: [],
      confirmed: true,
      context_revision: 1,
    },
    {
      ref: { kind: 'ProjectSpec', id: PROJECT, version: '1' },
      state: 'CURRENT',
      source_ids: [],
      confirmed: true,
      context_revision: 1,
    },
  ],
  impacts: [
    {
      target: { kind: 'ChapterVersion', id: METHOD, version: 'v-m1' },
      action: 'REVIEW_REQUIRED',
      certainty: 'definite',
      causes: [
        {
          edge_id: 'explicit:cs-a',
          source: { kind: 'ProjectSpec', id: PROJECT, version: '1' },
          reason: '研究条件变化，需要重新复核',
        },
      ],
    },
  ],
  edges: [],
}

/** 影响评估的解析结果（纯逻辑用）。 */
const ASSESSMENT: G3ImpactAssessmentFact = {
  changeSetId: 'cs-a',
  projectId: PROJECT,
  contextRevision: 1,
  inputHash: 'hash-1',
  objects: [
    { kind: 'ChapterVersion', id: METHOD, version: 'v-m1', state: 'CURRENT', confirmed: true, contextRevision: 1 },
    { kind: 'ProjectSpec', id: PROJECT, version: '1', state: 'CURRENT', confirmed: true, contextRevision: 1 },
  ],
  impacts: [
    {
      target: { kind: 'ChapterVersion', id: METHOD, version: 'v-m1' },
      action: 'REVIEW_REQUIRED',
      certainty: 'definite',
      causes: [
        {
          edgeId: 'explicit:cs-a',
          source: { kind: 'ProjectSpec', id: PROJECT, version: '1' },
          reason: '研究条件变化，需要重新复核',
        },
      ],
    },
  ],
  edges: [],
}

/** 章节版本列表的原始响应。 */
function versions(items: { id: string; specRevision: number; confirmationValid: boolean }[]) {
  return {
    status: 200,
    body: {
      data: {
        items: items.map((item) => ({
          id: item.id,
          chapter_id: item.id === 'v-q1' ? QUESTION : METHOD,
          spec_revision: item.specRevision,
          confirmation_valid: item.confirmationValid,
          body_markdown: '## 标题',
        })),
      },
    },
  }
}

/** 铺好整条链要用的应答。 */
function seed(fake: ReturnType<typeof createFakeClient>): void {
  fake.reply('/api/v1/version', { status: 401, body: { error: 'unauthorized' } })
  fake.reply('/api/v1/system/info', {
    status: 200,
    body: { data: { commit_id: '7f5fd8c', build_time: '2026-10-19 17:42:56 UTC' } },
  })
  fake.reply('/api/v1/auth/register', {
    status: 201,
    body: { user: { id: 'u1', username: 'g3demo-x', email: 'g3demo-x@example.com', tenant_id: 10055 } },
  })
  fake.replyFor('POST', '/api/v1/auth/login', {
    status: 200,
    body: { token: 'jwt-nonowner', user: { id: 'u2' }, active_tenant: { id: 10055 } },
  })
  fake.reply('/api/v1/lingdoc/projects', {
    status: 201,
    body: {
      data: {
        id: PROJECT,
        name: 'demo',
        status: 'draft',
        project_version: 1,
        spec_revision: 0,
        current_context_revision: 0,
        template_id: 'template-demo',
        template_version: '1',
      },
    },
  })
  fake.reply(basePath('/spec'), {
    status: 200,
    body: {
      data: {
        id: PROJECT,
        status: 'draft',
        project_version: 2,
        spec_revision: 1,
        current_context_revision: 0,
        template_id: 'template-demo',
        template_version: '1',
      },
    },
  })
  fake.reply(basePath('/activation-read'), {
    status: 201,
    body: { data: { id: 'read-1', project_version: 2, spec_revision: 1 } },
  })
  fake.reply(basePath('/activate'), {
    status: 200,
    body: {
      data: {
        id: PROJECT,
        status: 'active',
        project_version: 3,
        spec_revision: 1,
        current_context_revision: 1,
        template_id: 'template-demo',
        template_version: '1',
      },
    },
  })
  // 章节列表：确认有效 → 应用后读视图口径下失效
  fake.reply(basePath('/chapters'), {
    status: 200,
    body: {
      data: [
        {
          id: QUESTION,
          section_id: 'question',
          title: '研究问题',
          current_version_id: 'v-q1',
          body_markdown: '## 研究问题',
          confirmation_valid: true,
        },
        {
          id: METHOD,
          section_id: 'method',
          title: '研究方案',
          current_version_id: 'v-m1',
          body_markdown: '## 研究方案',
          confirmation_valid: true,
        },
      ],
    },
  })
  fake.replyFor('POST', basePath(`/chapters/${QUESTION}/versions`), {
    status: 201,
    body: { data: { id: 'v-q1', section_id: 'question', current_version_id: 'v-q1', confirmation_valid: false } },
  })
  fake.replyFor('POST', basePath(`/chapters/${METHOD}/versions`), {
    status: 201,
    body: { data: { id: 'v-m1', section_id: 'method', current_version_id: 'v-m1', confirmation_valid: false } },
  })
  fake.replyFor('POST', basePath(`/chapters/${QUESTION}/confirmations`), {
    status: 201,
    body: { data: { id: 'c-q1', chapter_version_id: 'v-q1', spec_revision: 1 } },
  })
  fake.replyFor('POST', basePath(`/chapters/${METHOD}/confirmations`), {
    status: 201,
    body: { data: { id: 'c-m1', chapter_version_id: 'v-m1', spec_revision: 1 } },
  })
  // 当前确认：应用前有 → 应用后没有 → 重新确认后有（绑定新规格修订）
  fake.replySequence(basePath(`/chapters/${QUESTION}/confirmations/current`), [
    { status: 200, body: { data: { id: 'c-q1', chapter_version_id: 'v-q1', spec_revision: 1 } } },
    { status: 200, body: { data: null } },
  ])
  fake.replySequence(basePath(`/chapters/${METHOD}/confirmations/current`), [
    { status: 200, body: { data: { id: 'c-m1', chapter_version_id: 'v-m1', spec_revision: 1 } } },
    { status: 200, body: { data: null } },
    { status: 200, body: { data: { id: 'c-m2', chapter_version_id: 'v-m1', spec_revision: 2 } } },
  ])

  // 变更集
  const changeSet = (id: string, extra: Record<string, unknown> = {}) => ({
    status: 201,
    body: {
      data: {
        id,
        project_id: PROJECT,
        status: 'assessed',
        base_context_revision: 1,
        base_spec_revision: 1,
        reason: '探针理由',
        fields: [{ key: 'research_goal', old_value: '旧值', new_value: '新值' }],
        impacts: [
          {
            chapter_id: METHOD,
            chapter_version_id: 'v-m1',
            title: '研究方案',
            reason: '研究条件变化，需要重新复核',
            status: 'open',
          },
        ],
        ...extra,
      },
    },
  })
  fake.replySequence(basePath('/change-sets'), [changeSet('cs-a'), changeSet('cs-b')])
  fake.reply(basePath('/change-sets/cs-a/assess'), changeSet('cs-a'))
  fake.reply(basePath('/change-sets/cs-a/apply'), {
    status: 200,
    body: {
      data: {
        id: 'cs-a',
        status: 'applied',
        base_context_revision: 1,
        target_context_revision: 2,
        base_spec_revision: 1,
        target_spec_revision: 2,
        fields: [],
        impacts: [],
      },
    },
  })
  fake.reply(basePath('/change-sets/cs-a/impact-assessment'), { status: 200, body: { data: ASSESSMENT_RAW } })
  fake.reply(basePath('/change-sets/cs-a'), {
    status: 200,
    body: {
      data: {
        id: 'cs-a',
        project_id: PROJECT,
        status: 'applied',
        base_context_revision: 1,
        target_context_revision: 2,
        base_spec_revision: 1,
        target_spec_revision: 2,
        decision_by: 'u1',
        reason: '探针理由',
        fields: [{ key: 'research_goal', old_value: '旧值', new_value: '新值' }],
        impacts: [
          {
            chapter_id: METHOD,
            chapter_version_id: 'v-m1',
            title: '研究方案',
            reason: '研究条件变化，需要重新复核',
            status: 'open',
          },
        ],
      },
    },
  })
  fake.reply(basePath('/change-sets/cs-b/apply'), {
    status: 409,
    body: { error: { code: 'version_conflict', message: '内容已变化，请先读取当前版本。' } },
  })
  fake.reply(basePath('/change-sets/cs-b'), {
    status: 200,
    body: {
      data: {
        id: 'cs-b',
        status: 'stale',
        stale_reason: 'spec_revision_changed',
        base_context_revision: 1,
        base_spec_revision: 1,
        fields: [],
        impacts: [],
      },
    },
  })
  fake.reply(basePath('/change-sets/cs-b/reassess'), {
    status: 201,
    body: {
      data: {
        id: 'cs-b2',
        status: 'assessed',
        reassessed_from_id: 'cs-b',
        base_context_revision: 2,
        base_spec_revision: 2,
        fields: [],
        impacts: [],
      },
    },
  })
  fake.reply(basePath('/change-sets/cs-a/reverse'), {
    status: 201,
    body: {
      data: {
        id: 'cs-a-reverse',
        status: 'assessed',
        reverses_change_set_id: 'cs-a',
        base_context_revision: 2,
        base_spec_revision: 2,
        fields: [{ key: 'research_goal', old_value: '新值', new_value: '旧值' }],
        impacts: [],
      },
    },
  })
  fake.reply(basePath('/change-sets/cs-a-reverse/impact-assessment'), {
    status: 200,
    body: { data: { ...ASSESSMENT_RAW, change_set_id: 'cs-a-reverse', context_revision: 2 } },
  })
  fake.reply(basePath('/change-sets/cs-a-reverse/apply'), {
    status: 200,
    body: {
      data: {
        id: 'cs-a-reverse',
        status: 'applied',
        base_context_revision: 2,
        target_context_revision: 3,
        base_spec_revision: 2,
        target_spec_revision: 3,
        fields: [],
        impacts: [],
      },
    },
  })
  // 上下文修订：应用后多一条，陈旧应用后不再增加
  fake.reply(basePath('/context-revisions'), {
    status: 200,
    body: { data: [{ revision: 1, spec_revision: 1 }, { revision: 2, spec_revision: 2 }] },
  })
  // 影响任务：只针对受影响章节
  fake.reply(basePath('/impact-tasks'), {
    status: 200,
    body: {
      data: [
        {
          id: 'task-1',
          change_set_id: 'cs-a',
          action: 'REVIEW_REQUIRED',
          severity: 'blocking',
          responsibility_domain: 'B',
          status: 'OPEN',
          target: { kind: 'ChapterVersion', id: METHOD, version: 'v-m1' },
        },
      ],
    },
  })
  // 项目状态：应用前修订 1，应用后修订 2，逆向应用后修订 3
  fake.replySequence(basePath(''), [
    {
      status: 200,
      body: {
        data: {
          id: PROJECT,
          status: 'active',
          project_version: 9,
          spec_revision: 1,
          current_context_revision: 1,
          spec: { research_subject: '普通本科生', research_goal: '旧值' },
        },
      },
    },
    {
      status: 200,
      body: {
        data: {
          id: PROJECT,
          status: 'active',
          project_version: 9,
          spec_revision: 2,
          current_context_revision: 2,
          spec: { research_subject: '普通本科生', research_goal: '新值' },
        },
      },
    },
    {
      status: 200,
      body: {
        data: {
          id: PROJECT,
          status: 'active',
          project_version: 9,
          spec_revision: 2,
          current_context_revision: 2,
          spec: { research_subject: '普通本科生', research_goal: '新值' },
        },
      },
    },
    {
      status: 200,
      body: {
        data: {
          id: PROJECT,
          status: 'active',
          project_version: 10,
          spec_revision: 3,
          current_context_revision: 3,
          spec: { research_subject: '普通本科生', research_goal: '旧值' },
        },
      },
    },
  ])
  // 章节版本列表：受影响章节 有效 → 失效 → 有效；未受影响章节 一直有效
  fake.replySequenceFor('GET', basePath(`/chapters/${METHOD}/versions`), [
    versions([{ id: 'v-m1', specRevision: 1, confirmationValid: true }]),
    versions([{ id: 'v-m1', specRevision: 1, confirmationValid: false }]),
    versions([{ id: 'v-m1', specRevision: 2, confirmationValid: true }]),
  ])
  fake.replySequenceFor('GET', basePath(`/chapters/${QUESTION}/versions`), [
    versions([{ id: 'v-q1', specRevision: 1, confirmationValid: true }]),
    versions([{ id: 'v-q1', specRevision: 1, confirmationValid: true }]),
  ])

  // 非 Owner 探测：同租户、同项目的成员能读，但决定被拒
  fake.reply('/api/v1/tenants/10055/members', { status: 201, body: { data: { user_id: 'u2' } } })
  fake.reply(basePath('/invitations'), {
    status: 201,
    body: { data: { id: 'inv-1', version: 1, assignment: { user_id: 'u2' } } },
  })
  fake.reply(basePath('/invitations/inv-1/accept'), { status: 200, body: { data: { id: 'inv-1' } } })
  fake.replyFor('GET', basePath('/change-sets/cs-a-reverse'), {
    status: 200,
    body: { data: { id: 'cs-a-reverse', status: 'applied' } },
  }, { token: 'jwt-nonowner' })
  fake.replyFor('POST', basePath('/change-sets/cs-a-reverse/apply'), {
    status: 404,
    body: { error: { code: 'not_found', message: '资源不存在或不可访问。' } },
  }, { token: 'jwt-nonowner' })
  fake.replyFor('POST', basePath('/change-sets/cs-a-reverse/reject'), {
    status: 404,
    body: { error: { code: 'not_found', message: '资源不存在或不可访问。' } },
  }, { token: 'jwt-nonowner' })
}

const CHAIN_OPTIONS = {
  projectId: PROJECT,
  chapters: [
    { id: QUESTION, sectionId: 'question', title: '研究问题' },
    { id: METHOD, sectionId: 'method', title: '研究方案' },
  ],
  fieldValues: { research_subject: '普通本科生', research_goal: '旧值' },
  tenantId: TENANT,
}

test('读影响评估严格早于应用变更', async () => {
  const fake = createFakeClient()
  seed(fake)
  const ctx = context()
  await runChangeSetChain(ctx, fake.client, 'jwt-1', CHAIN_OPTIONS)

  const assessmentIndex = ctx.steps.findIndex((step) => step.name === G3_CS_STEPS.readAssessment)
  const applyIndex = ctx.steps.findIndex((step) => step.name === G3_CS_STEPS.apply)
  assert.ok(assessmentIndex >= 0, '本轮没有读影响评估这一步')
  assert.ok(applyIndex >= 0, '本轮没有应用变更这一步')
  assert.ok(assessmentIndex < applyIndex, '读影响评估必须早于应用变更')
})

test('assess 与 reverse 传空对象，reassess 完全空体', async () => {
  const fake = createFakeClient()
  seed(fake)
  const ctx = context()
  await runChangeSetChain(ctx, fake.client, 'jwt-1', CHAIN_OPTIONS)

  const assess = fake.calls.find((call) => call.path.endsWith('/assess'))
  assert.ok(assess, '没有发出 assess 请求')
  assert.deepEqual(assess.body, {}, 'assess 的请求体必须严格是 {}')

  const reverse = fake.calls.find((call) => call.path.endsWith('/reverse'))
  assert.ok(reverse, '没有发出 reverse 请求')
  assert.deepEqual(reverse.body, {}, 'reverse 的请求体必须严格是 {}')

  const reassess = fake.calls.find((call) => call.path.endsWith('/reassess'))
  assert.ok(reassess, '没有发出 reassess 请求')
  assert.equal(reassess.body, undefined, 'reassess 必须完全空体')
})

test('单读影响评估也走同一条路径', async () => {
  const fake = createFakeClient()
  seed(fake)
  const ctx = context()
  const assessment = await readImpactAssessment(ctx, fake.client, 'jwt-1', PROJECT, 'cs-a')
  assert.equal(assessment.changeSetId, 'cs-a')
  assert.equal(assessment.impacts.length, 1)
  assert.equal(assessment.impacts[0].certainty, 'definite')
  assert.equal(assessment.impacts[0].causes[0].reason, '研究条件变化，需要重新复核')
})

test('重评估走空体端点', async () => {
  const fake = createFakeClient()
  seed(fake)
  const ctx = context()
  const fact = await reassessChangeSet(ctx, fake.client, 'jwt-1', PROJECT, 'cs-b')
  assert.equal(fact.id, 'cs-b2')
  assert.equal(fact.reassessedFromId, 'cs-b')
  const call = fake.calls.find((item) => item.path.endsWith('/reassess'))
  assert.equal(call?.body, undefined)
})

test('四条判据都由响应事实判定', async () => {
  const fake = createFakeClient()
  seed(fake)
  const ctx = context()
  const result = await runChangeSetChain(ctx, fake.client, 'jwt-1', CHAIN_OPTIONS)

  const byId = new Map(result.verdicts.map((verdict) => [verdict.id, verdict]))
  for (const id of ['AC-G3-01', 'AC-G3-02', 'AC-G3-04', 'AC-G3-11']) {
    assert.ok(byId.has(id), `缺少 ${id} 的判定`)
    assert.equal(byId.get(id)?.holds, true, `${id} 判定不成立：${byId.get(id)?.detail}`)
  }
  // 每条判定都要带方法、路径、状态码与关键响应字段
  for (const verdict of result.verdicts) {
    assert.ok(verdict.evidence.length > 0, `${verdict.id} 没有可点击证据`)
    assert.match(verdict.evidence.join('\n'), /HTTP \d+/)
  }
})

test('非 Owner 调用应用与拒绝都拿到明确错误码', async () => {
  const fake = createFakeClient()
  seed(fake)
  const ctx = context()
  const result = await runChangeSetChain(ctx, fake.client, 'jwt-1', CHAIN_OPTIONS)

  assert.ok(result.permissionProbe, '缺少权限探测结果')
  assert.equal(result.permissionProbe.read.status, 200, '非 Owner 应当能读到变更集')
  assert.equal(result.permissionProbe.apply.status, 404)
  assert.equal(result.permissionProbe.apply.code, 'not_found')
  assert.equal(result.permissionProbe.reject.status, 404)
  assert.equal(result.permissionProbe.reject.code, 'not_found')
})

test('未受影响理由取自影响评估响应，服务端没给就如实标注', () => {
  const reason = unaffectedReason({
    chapterId: QUESTION,
    sectionId: 'question',
    assessment: ASSESSMENT,
    changeSetImpacts: [{ chapter_id: METHOD, reason: '研究条件变化，需要重新复核' }],
    impactTaskChapterIds: [METHOD],
    versionConfirmationValid: true,
  })
  assert.equal(reason.serverReason, null, '服务端没有给出未受影响理由')
  assert.match(reason.text, /服务端未提供未受影响理由/)
  assert.match(reason.text, /question/)
})

test('服务端给出理由时原样引用', () => {
  const reason = unaffectedReason({
    chapterId: QUESTION,
    sectionId: 'question',
    assessment: {
      ...ASSESSMENT,
      objects: [
        ...ASSESSMENT.objects,
        {
          kind: 'ChapterVersion',
          id: QUESTION,
          version: 'v-q1',
          state: 'CURRENT',
          confirmed: true,
          contextRevision: 1,
        },
      ],
    },
    changeSetImpacts: [],
    impactTaskChapterIds: [],
    versionConfirmationValid: true,
  })
  assert.equal(reason.serverReason, '评估中出现的对象状态：ChapterVersion/c-question · CURRENT')
})
