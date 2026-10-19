/**
 * G3 验收台 · 隔离项目：建项 → 写规格 → 登记立项阅读 → 激活 → 章节就绪。
 *
 * 顺序不是审美问题，是状态机的前置条件（`internal/lingdoc/workspace/http.go:304-307`）：
 * 建项 → 写规格 → 登记立项阅读 → 激活，激活才自动创建 `question` / `method` 两个章节。
 * 少一步、错一步，拿到的不是「更详细的报错」，而是 409/422。
 *
 * **每次运行都新建项目**：项目名带运行标识，不复用历史项目；演示账号本身可以跨运行复用。
 */

import { envelopeData, type G3HttpClient } from './httpClient'
import { makeIdempotencyKey, runStep, type G3ChapterFact, type G3RunContext } from './runContext'

/** 演示模板。 */
export const G3_DEMO_TEMPLATE_ID = 'template-demo'
export const G3_DEMO_TEMPLATE_VERSION = '1'

/** 演示模板的必填字段。 */
export const G3_DEMO_REQUIRED_FIELDS = ['research_subject', 'research_goal'] as const

/** 演示模板激活后自动创建的两个章节（section_id）。 */
export const G3_DEMO_REQUIRED_SECTIONS = ['question', 'method'] as const

/** 项目事实。 */
export interface G3ProjectFact {
  id: string
  name: string
  status: string
  projectVersion: number
  specRevision: number
  currentContextRevision: number
  templateId: string
  templateVersion: string
}

/** 把后端返回的项目对象读成项目事实。 */
export function readProjectFact(data: unknown): G3ProjectFact {
  const raw = (data ?? {}) as Record<string, unknown>
  return {
    id: String(raw.id ?? ''),
    name: String(raw.name ?? ''),
    status: String(raw.status ?? ''),
    projectVersion: Number(raw.project_version ?? 0),
    specRevision: Number(raw.spec_revision ?? 0),
    currentContextRevision: Number(raw.current_context_revision ?? 0),
    templateId: String(raw.template_id ?? ''),
    templateVersion: String(raw.template_version ?? ''),
  }
}

/** 建项。 */
export async function createProject(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  options: { name: string; templateId?: string; templateVersion?: string; idemPrefix?: string },
): Promise<G3ProjectFact> {
  const templateId = options.templateId ?? G3_DEMO_TEMPLATE_ID
  const templateVersion = options.templateVersion ?? G3_DEMO_TEMPLATE_VERSION
  const { data } = await runStep(ctx, client, {
    name: '建项',
    method: 'POST',
    path: '/api/v1/lingdoc/projects',
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, `${options.idemPrefix ?? ''}create-project`),
    body: { name: options.name, template_id: templateId, template_version: templateVersion },
    conclusion: `项目已建立（HTTP {status}）：${options.name}`,
    detail: (body) => {
      const project = (body as { data?: Record<string, unknown> } | null)?.data
      if (!project) return undefined
      return {
        project_id: project.id,
        status: project.status,
        project_version: project.project_version,
        spec_revision: project.spec_revision,
        template_id: project.template_id,
        template_version: project.template_version,
      }
    },
  })

  const project = readProjectFact(envelopeData(data))
  ctx.projectId = project.id
  ctx.projectName = project.name
  return project
}

/** 写规格。 */
export async function saveProjectSpec(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  options: { expectedSpecRevision: number; fields: Record<string, string>; idemPrefix?: string },
): Promise<G3ProjectFact> {
  const { data } = await runStep(ctx, client, {
    name: '写规格',
    method: 'PUT',
    path: `/api/v1/lingdoc/projects/${encodeURIComponent(projectId)}/spec`,
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, `${options.idemPrefix ?? ''}save-spec`),
    body: {
      expected_spec_revision: options.expectedSpecRevision,
      fields: options.fields,
    },
    conclusion: `规格已写入（HTTP {status}）：${Object.keys(options.fields).join('、')}`,
    detail: (body) => {
      const project = (body as { data?: Record<string, unknown> } | null)?.data
      if (!project) return undefined
      return {
        spec_revision: project.spec_revision,
        project_version: project.project_version,
        spec_fields: Object.keys((project.spec ?? {}) as Record<string, unknown>),
      }
    },
  })

  return readProjectFact(envelopeData(data))
}

/** 登记立项阅读，返回回执 id。 */
export async function recordActivationRead(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  options: { expectedProjectVersion: number; idemPrefix?: string },
): Promise<string> {
  const { data } = await runStep(ctx, client, {
    name: '登记立项阅读',
    method: 'POST',
    path: `/api/v1/lingdoc/projects/${encodeURIComponent(projectId)}/activation-read`,
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, `${options.idemPrefix ?? ''}activation-read`),
    body: { expected_project_version: options.expectedProjectVersion },
    conclusion: '立项阅读已登记（HTTP {status}）',
    detail: (body) => {
      const receipt = (body as { data?: Record<string, unknown> } | null)?.data
      if (!receipt) return undefined
      return {
        activation_read_id: receipt.id,
        project_version: receipt.project_version,
        spec_revision: receipt.spec_revision,
      }
    },
  })

  const receipt = (envelopeData(data) ?? {}) as Record<string, unknown>
  return String(receipt.id ?? '')
}

/** 激活项目。 */
export async function activateProject(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  options: {
    activationReadId: string
    expectedSpecRevision: number
    expectedProjectVersion: number
    idemPrefix?: string
  },
): Promise<G3ProjectFact> {
  const { data } = await runStep(ctx, client, {
    name: '激活',
    method: 'POST',
    path: `/api/v1/lingdoc/projects/${encodeURIComponent(projectId)}/activate`,
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, `${options.idemPrefix ?? ''}activate`),
    body: {
      activation_read_id: options.activationReadId,
      expected_spec_revision: options.expectedSpecRevision,
      expected_project_version: options.expectedProjectVersion,
      reviewed_project_version: options.expectedProjectVersion,
    },
    conclusion: '项目已激活（HTTP {status}）',
    detail: (body) => {
      const project = (body as { data?: Record<string, unknown> } | null)?.data
      if (!project) return undefined
      return {
        status: project.status,
        project_version: project.project_version,
        current_context_revision: project.current_context_revision,
        baseline_confirmation_id: project.baseline_confirmation_id,
      }
    },
  })

  return readProjectFact(envelopeData(data))
}

/** 读章节列表。 */
export async function listChapters(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
): Promise<G3ChapterFact[]> {
  const { data } = await runStep(ctx, client, {
    name: '读章节列表',
    method: 'GET',
    path: `/api/v1/lingdoc/projects/${encodeURIComponent(projectId)}/chapters`,
    token,
    conclusion: '章节列表已读回（HTTP {status}）',
    detail: (body) => {
      const list = (body as { data?: unknown[] } | null)?.data
      if (!Array.isArray(list)) return undefined
      return {
        chapter_count: list.length,
        section_ids: list.map((item) => (item as { section_id?: unknown })?.section_id),
      }
    },
  })

  const list = (envelopeData(data) ?? []) as Array<Record<string, unknown>>
  const chapters: G3ChapterFact[] = list.map((item) => ({
    id: String(item.id ?? ''),
    sectionId: String(item.section_id ?? ''),
    title: String(item.title ?? ''),
    currentVersionId: item.current_version_id ? String(item.current_version_id) : null,
    bodyEmpty: !String(item.body_markdown ?? '').trim(),
  }))
  ctx.chapters = chapters
  return chapters
}

/** 两个模板必需章节是否都已就位。 */
export function missingRequiredSections(chapters: readonly G3ChapterFact[]): string[] {
  const present = new Set(chapters.map((chapter) => chapter.sectionId))
  return G3_DEMO_REQUIRED_SECTIONS.filter((section) => !present.has(section))
}

/** 项目链的输入。 */
export interface G3ProjectChainOptions {
  /** 项目名。必须能看出是哪一次运行建的。 */
  projectName: string
  /** 规格字段。 */
  fields?: Record<string, string>
  /**
   * 幂等键前缀。
   *
   * 同一次运行里建**两个**项目时必须给不同的前缀：幂等键相同而请求不同，
   * 服务端会按 409 `idempotency_conflict` 拒掉。
   */
  idemPrefix?: string
}

/** 跑完「建项 → 写规格 → 登记立项阅读 → 激活 → 章节就绪」整条链。 */
export async function runProjectChain(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  options: G3ProjectChainOptions,
): Promise<{ project: G3ProjectFact; chapters: G3ChapterFact[] }> {
  const fields = options.fields ?? {
    research_subject: `G3 验收台演示对象（运行 ${ctx.runId}）`,
    research_goal: '用演示模板自建隔离项目，走到两个章节就绪',
  }

  const created = await createProject(ctx, client, token, {
    name: options.projectName,
    idemPrefix: options.idemPrefix,
  })
  const speced = await saveProjectSpec(ctx, client, token, created.id, {
    expectedSpecRevision: created.specRevision,
    fields,
    idemPrefix: options.idemPrefix,
  })
  const receiptId = await recordActivationRead(ctx, client, token, created.id, {
    expectedProjectVersion: speced.projectVersion,
    idemPrefix: options.idemPrefix,
  })
  const activated = await activateProject(ctx, client, token, created.id, {
    activationReadId: receiptId,
    expectedSpecRevision: speced.specRevision,
    expectedProjectVersion: speced.projectVersion,
    idemPrefix: options.idemPrefix,
  })
  const chapters = await listChapters(ctx, client, token, created.id)

  return { project: activated, chapters }
}
