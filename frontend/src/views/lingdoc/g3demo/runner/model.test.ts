/**
 * G3 验收台 · 真实模型链的纯逻辑单测。
 *
 * 这些断言只覆盖**不需要真实模型**就能定住的部分：候选解析、模型运行标识、
 * 非确定性结论的取值口径。真实调用由 `scripts/impactChains/runModel.ts` 实跑采证。
 */

import assert from 'node:assert/strict'
import test from 'node:test'

import {
  G3_MODEL_STEPS,
  readTurnFact,
  parseChangeDraftSuggestion,
  parseDependencyCandidates,
  normalizeCandidateAction,
  summarizeModelRun,
  runModelChain,
  type G3ModelRunFact,
} from './model'
import type { G3HttpClient, G3HttpRequest, G3HttpResponse } from './httpClient'
import { createRunContext, type G3RunContext } from './runContext'

test('模型链的步骤名与证据规则表一致', () => {
  assert.equal(G3_MODEL_STEPS.ac06Conclusion, '模型-旧异步-结论')
  assert.equal(G3_MODEL_STEPS.ac14Conclusion, '模型-助手草稿-结论')
  assert.equal(G3_MODEL_STEPS.ac15Conclusion, '模型-候选依赖-结论')
})

test('助手轮次事实按响应读出，缺失字段不编造', () => {
  const turn = readTurnFact({
    id: 'turn-1',
    session_id: 'session-1',
    agent: 'task_assistant',
    status: 'completed',
    model_id: 'deepseek-chat',
    run_mode: 'real',
    input_tokens: 120,
    output_tokens: 80,
    version: 2,
    created_at: '2026-10-20T00:00:00Z',
    reply: '好的',
    context: { context_revision: 4, project_version: 7 },
    suggestions: { reply: '好的', change_draft: { fields: { research_subject: 'x' } } },
  })
  assert.equal(turn.id, 'turn-1')
  assert.equal(turn.sessionId, 'session-1')
  assert.equal(turn.status, 'completed')
  assert.equal(turn.modelId, 'deepseek-chat')
  assert.equal(turn.runMode, 'real')
  assert.equal(turn.contextRevision, 4)
  assert.equal(turn.projectVersion, 7)
  assert.equal(turn.reply, '好的')
  assert.equal(turn.suggestions.change_draft !== undefined, true)
})

test('助手没有给出变更草稿候选时如实读成空', () => {
  const turn = readTurnFact({ id: 'turn-2', status: 'completed', suggestions: { reply: '请先澄清' } })
  assert.equal(turn.suggestions.change_draft, undefined)
  const draft = parseChangeDraftSuggestion(turn.suggestions)
  assert.equal(draft, null)
})

test('变更草稿候选按模型给出的键值读出', () => {
  const draft = parseChangeDraftSuggestion({
    change_draft: {
      expected_context_revision: 4,
      reason: '研究对象需要调整',
      fields: { research_subject: '接受过创业教育的本科生' },
      affected_chapter_ids: ['chapter-1'],
    },
  })
  assert.ok(draft)
  assert.equal(draft?.expectedContextRevision, 4)
  assert.equal(draft?.reason, '研究对象需要调整')
  assert.equal(draft?.fields.research_subject, '接受过创业教育的本科生')
  assert.deepEqual(draft?.affectedChapterIds, ['chapter-1'])
})

test('批量候选依赖按模型给出的数组读出，坏条目被丢掉', () => {
  const candidates = parseDependencyCandidates({
    dependency_candidates: [
      {
        source: { kind: 'ProjectSpec', id: 'p1', version: '3' },
        target: { kind: 'TemplateCopy', id: 't1', version: '1' },
        relation: 'spec_regenerates_template',
        action: 'REGENERATE',
        reason: '研究条件变化需要重新生成模板派生内容',
        confidence: 0.8,
        basis: '研究条件字段变更',
      },
      { relation: '缺对象' },
    ],
  })
  assert.equal(candidates.length, 1)
  assert.equal(candidates[0]?.relation, 'spec_regenerates_template')
  assert.equal(candidates[0]?.source.kind, 'ProjectSpec')
  assert.equal(candidates[0]?.confidence, 0.8)
})

test('候选动作不在词表内时归一到人工复核动作', () => {
  assert.equal(normalizeCandidateAction('REGENERATE'), 'REGENERATE')
  assert.equal(normalizeCandidateAction('删库'), 'REVIEW_REQUIRED')
  assert.equal(normalizeCandidateAction(''), 'REVIEW_REQUIRED')
})

test('模型运行标识与耗时按响应汇总，不填假值', () => {
  const run: G3ModelRunFact = {
    runId: 'turn-9',
    sessionId: 'session-9',
    modelId: 'deepseek-chat',
    runMode: 'real',
    status: 'completed',
    failureCode: '',
    durationMs: 4321,
    inputTokens: 10,
    outputTokens: 20,
    startedAt: '2026-10-20T00:00:00Z',
    finishedAt: '2026-10-20T00:00:04Z',
  }
  const text = summarizeModelRun(run)
  assert.equal(text.includes('turn-9'), true)
  assert.equal(text.includes('4321'), true)
  assert.equal(text.includes('deepseek-chat'), true)
})

/**
 * 造一个「模型不可用」的假客户端：章节相关的写接口照常成功，
 * 但**每一次助手轮次**都被服务端按 503 拒掉（缺外部凭据 / 模型能力不可用）。
 */
function modelUnavailableClient(state: { revision: number }): G3HttpClient {
  const project = () => ({
    data: {
      id: 'p1',
      status: 'active',
      project_version: 3,
      spec_revision: 2,
      current_context_revision: state.revision,
      spec: { research_subject: '普通本科生', research_goal: '研究目标' },
      template_id: 'template-demo',
      template_copy_version: 1,
      template_copy: { ruleset_hash: 'h' },
    },
  })
  return async (request: G3HttpRequest): Promise<G3HttpResponse> => {
    const path = request.path
    const method = request.method.toUpperCase()
    const ok = (body: unknown): G3HttpResponse => ({ status: 200, ok: true, body, durationMs: 1 })
    if (method === 'GET' && /\/projects\/p1$/.test(path)) return ok(project())
    if (method === 'POST' && /\/chapters\/[^/]+\/versions$/.test(path)) {
      return ok({ data: { id: 'chapter-1', current_version_id: `v-${state.revision}` } })
    }
    if (method === 'POST' && /\/chapters\/[^/]+\/confirmations$/.test(path)) {
      return ok({ data: { id: 'c-1', chapter_version_id: `v-${state.revision}`, spec_revision: 2 } })
    }
    if (method === 'POST' && /\/agents\/task_assistant\/sessions$/.test(path)) {
      return ok({ data: { id: 's1', agent: 'task_assistant' } })
    }
    if (method === 'POST' && /\/agent-sessions\/[^/]+\/turns$/.test(path)) {
      // 模型不可用 / 缺外部凭据：服务端按 503 拒掉。
      return {
        status: 503,
        ok: false,
        body: { error: { code: 'dependency_unavailable', message: '所需资料、搜索或模型能力暂不可用。' } },
        durationMs: 1,
      }
    }
    if (method === 'POST' && /\/change-sets$/.test(path)) {
      return ok({ data: { id: `cs-${state.revision}`, status: 'assessed', base_context_revision: state.revision } })
    }
    if (method === 'POST' && /\/change-sets\/[^/]+\/assess$/.test(path)) {
      return ok({ data: { id: 'cs-1', status: 'assessed' } })
    }
    if (method === 'GET' && /\/change-sets\/[^/]+\/impact-assessment$/.test(path)) {
      return ok({ data: { change_set_id: 'cs-1', objects: [], impacts: [] } })
    }
    if (method === 'POST' && /\/change-sets\/[^/]+\/apply$/.test(path)) {
      state.revision += 1
      return ok({ data: { id: 'cs-1', status: 'applied', target_context_revision: state.revision } })
    }
    if (method === 'GET' && /\/chapters$/.test(path)) {
      return ok({ data: [{ id: 'chapter-1', section_id: 'question', title: '研究问题' }] })
    }
    if (method === 'GET' && /\/chapters\/[^/]+\/versions$/.test(path)) {
      return ok({ data: [{ id: `v-${state.revision}`, spec_revision: 2 }] })
    }
    if (method === 'GET' && /\/chapters\/[^/]+\/confirmations\/current$/.test(path)) {
      return ok({ data: null })
    }
    return { status: 404, ok: false, body: { error: { code: 'not_found' } }, durationMs: 1 }
  }
}

test('模型不可用时：三条结论标 NOT RUN 并写明缺外部凭据，其余判定照常', async () => {
  const state = { revision: 1 }
  const ctx: G3RunContext = createRunContext({ runId: 'g3r-model-na', accountEmail: 'a@example.com' })
  const result = await runModelChain(ctx, modelUnavailableClient(state), 'token', {
    projectId: 'p1',
    chapters: [
      { id: 'chapter-1', sectionId: 'question', title: '研究问题' },
      { id: 'chapter-2', sectionId: 'method', title: '研究方法' },
    ],
    operatorId: 'user-1',
  })

  assert.equal(result.modelAvailable, false)
  assert.match(result.unavailableReason, /缺外部凭据|模型不可用/)
  for (const verdict of result.verdicts) {
    assert.equal(verdict.holds, false, `${verdict.id} 在模型不可用时不该判定成立`)
    assert.equal(verdict.status, 'NOT RUN', `${verdict.id} 在模型不可用时必须标 NOT RUN`)
  }
  // 三个场景的结论步骤仍然留痕：读者能看到「跑过、但没跑成」。
  const stepNames = ctx.steps.map((step) => step.name)
  for (const name of [
    G3_MODEL_STEPS.ac06Conclusion,
    G3_MODEL_STEPS.ac14Conclusion,
    G3_MODEL_STEPS.ac15Conclusion,
  ]) {
    assert.equal(stepNames.includes(name), true, `缺少场景结论步骤：${name}`)
  }
})
