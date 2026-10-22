/**
 * G3 验收台 · 真实链的总编排：身份 → head → 隔离项目 → 章节就绪。
 *
 * 本票（#238）跑的是**前置链**：注册 / 登录 → 记录后端 head → 用演示模板
 * 自建隔离项目 → 写规格 → 登记立项阅读 → 激活 → 两个章节就绪。
 *
 * 前置链跑通**不等于**任何一条 G3 判据通过：右栏矩阵里没被本轮覆盖的行
 * 仍然是 `TODO`。这条边界由 `rowEvidenceRules.ts` 守着，不靠人记。
 */

import type { G3HttpClient } from './httpClient'
import {
  loginAccount,
  readCurrentIdentity,
  registerAccount,
  type G3CurrentIdentity,
  type G3DemoAccount,
} from './demoIdentity'
import {
  missingRequiredSections,
  runProjectChain,
  type G3ProjectFact,
} from './demoProject'
import {
  createRunContext,
  probeBackendAlive,
  readBackendHead,
  type G3ChapterFact,
  type G3RunContext,
} from './runContext'

/** 总编排的输入。 */
export interface G3ChainOptions {
  /** 演示账号（运行时自建或复用本地已存账号）。 */
  account: G3DemoAccount
  /** 账号是否还没注册过。 */
  registerFirst: boolean
  /** 项目名。默认带运行标识。 */
  projectName?: string
  /** 运行标识。默认现生成。 */
  runId?: string
  /**
   * 登录成功后立刻回调。
   *
   * 浏览器侧的请求拦截器是从**既有认证存储**里读令牌的，所以令牌必须**当場**写进去，
   * 后面几步（读身份、读 head、建项目）才带得上凭据。回调把这一步交给宿主去做，
   * 执行器自己不碰存储。
   */
  onToken?: (token: string) => void
  /** 读到当前身份后回调。 */
  onIdentity?: (identity: G3CurrentIdentity) => void
}

/** 总编排的结果。 */
export interface G3ChainResult {
  ctx: G3RunContext
  token: string
  identity: G3CurrentIdentity
  project: G3ProjectFact
  chapters: G3ChapterFact[]
  /** 模板必需章节里还没就位的 section_id。 */
  missingSections: string[]
}

/** 跑完整条真实链。 */
export async function runChain(
  client: G3HttpClient,
  options: G3ChainOptions,
): Promise<G3ChainResult> {
  const runId = options.runId ?? ''
  const ctx = createRunContext({
    runId: runId || `g3r-${Date.now().toString(36)}`,
    accountEmail: options.account.email,
  })

  try {
    await probeBackendAlive(ctx, client)

    if (options.registerFirst) {
      await registerAccount(ctx, client, options.account)
    }

    const token = await loginAccount(ctx, client, options.account)
    options.onToken?.(token)
    const identity = await readCurrentIdentity(ctx, client, token)
    options.onIdentity?.(identity)
    await readBackendHead(ctx, client, token)

    const { project, chapters } = await runProjectChain(ctx, client, token, {
      projectName: options.projectName ?? `G3 验收台演示项目（运行 ${ctx.runId}）`,
    })

    return {
      ctx,
      token,
      identity,
      project,
      chapters,
      missingSections: missingRequiredSections(chapters),
    }
  } catch (error) {
    // 把已经跑过的步骤挂在错误上：失败也要留下真实调用记录，
    // 不能让人以为什么都没发生。
    if (error && typeof error === 'object') {
      ;(error as { runContext?: G3RunContext }).runContext = ctx
    }
    throw error
  }
}
