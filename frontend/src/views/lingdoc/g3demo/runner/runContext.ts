/**
 * G3 验收台 · 运行上下文：运行标识、幂等键工厂、后端 head 与步骤记录器。
 *
 * 这一层是**后续所有真实链路票共用的地基**，所以它的几条约定写死在代码里：
 *
 * 1. **运行标识**：每次运行都有唯一的 `runId`。幂等键由它派生，因此两次运行
 *    永远不会撞键，同一次运行的重放又能命中同一键。
 * 2. **幂等键长度**：8–128 字符，超出即 400（`internal/lingdoc/workspace/http.go:537-544`）。
 *    键工厂保证长度合规；执行器在发请求前**再验一次**，不合规就拒绝发。
 * 3. **后端 head**：本次运行对应的后端 commit。head 变了，之前跑出来的结论就过期了，
 *    所以 head 必须随运行一起记录，不能事后补。
 * 4. **步骤记录**：每一步都留一条结构化记录（方法、路径、状态码、幂等键、耗时、结论），
 *    页面左栏直接渲染它，不另造一套展示数据。
 */

import {
  G3_IDEMPOTENCY_MAX_LENGTH,
  G3_IDEMPOTENCY_MIN_LENGTH,
  G3ChainError,
  describeHttpFailure,
  envelopeData,
  errorCodeOf,
  shouldSendIdempotencyKey,
  type G3HttpClient,
  type G3HttpMethod,
  type G3HttpResponse,
} from './httpClient'

/** 一步真实调用的结构化记录。 */
export interface G3StepRecord {
  /** 步骤序号，从 1 开始。 */
  index: number
  /** 步骤名。 */
  name: string
  /** HTTP 方法。 */
  method: string
  /** 完整路径。 */
  path: string
  /** 状态码。0 表示请求根本没发出去（网络不可达）。 */
  status: number
  /** 是否成功（2xx）。 */
  ok: boolean
  /** 实际发出的幂等键。没发就是 undefined。 */
  idempotencyKey?: string
  /** 耗时（毫秒）。 */
  durationMs: number
  /** 这一步的结论（中文）。 */
  conclusion: string
  /** 关键响应字段。 */
  details?: Record<string, unknown>
  /** 失败时的错误说明。 */
  error?: string
}

/** 本次运行对应的后端 head。 */
export interface G3BackendHead {
  /** 后端进程 commit（编译期注入的 CommitID）。 */
  commitId: string
  /** 编译期注入的构建时间。 */
  buildTime?: string
  /** 后端版本号。 */
  version?: string
  /** 探到 head 的时刻。 */
  probedAt: string
  /** 存活探针路径。 */
  probePath: string
  /** 存活探针状态码。 */
  probeStatus: number
}

/** 章节事实：激活后模板自动创建的两个章节。 */
export interface G3ChapterFact {
  id: string
  sectionId: string
  title: string
  currentVersionId: string | null
  /** 正文是否还是空的（空正文过不了交付闸门，但那是后续票的事）。 */
  bodyEmpty: boolean
}

/** 一次运行的上下文。 */
export interface G3RunContext {
  /** 本次运行标识。 */
  runId: string
  /** 起始时刻（ISO）。 */
  startedAt: string
  /** 本次运行使用的演示账号。 */
  accountEmail: string
  /** 步骤记录，按发生顺序。 */
  steps: G3StepRecord[]
  /** 本次运行对应的后端 head。 */
  head: G3BackendHead | null
  /** 本次运行新建的项目 id。 */
  projectId?: string
  /** 本次运行新建的项目名。 */
  projectName?: string
  /** 激活后拿到的章节。 */
  chapters: G3ChapterFact[]
  /** 结论是否已过期（后端 head 变了）。 */
  stale: boolean
  /** 过期原因。 */
  staleReason?: string
}

/** 生成运行标识。 */
export function createRunId(now: number = Date.now(), random: () => number = Math.random): string {
  const time = now.toString(36)
  const salt = Math.floor(random() * 0xffffffff)
    .toString(16)
    .padStart(8, '0')
  return `g3r-${time}-${salt}`
}

/** 建立运行上下文。 */
export function createRunContext(options: {
  runId: string
  accountEmail: string
  startedAt?: string
}): G3RunContext {
  return {
    runId: options.runId,
    startedAt: options.startedAt ?? new Date().toISOString(),
    accountEmail: options.accountEmail,
    steps: [],
    head: null,
    chapters: [],
    stale: false,
  }
}

/**
 * 幂等键工厂：键里**一定**含本次运行标识。
 *
 * 长度合规由这里保证（截断标签部分，不动运行标识），并且事后用
 * `isValidIdempotencyKey` 复验一次——不合规宁可抛错，也不发一个会被 400 的键。
 */
export function makeIdempotencyKey(runId: string, label: string): string {
  const prefix = `g3-${runId}-`
  const room = G3_IDEMPOTENCY_MAX_LENGTH - prefix.length
  const safeLabel = label.replace(/[^A-Za-z0-9._-]/g, '-').slice(0, Math.max(room, 0))
  const key = `${prefix}${safeLabel}`
  if (key.trim().length < G3_IDEMPOTENCY_MIN_LENGTH) {
    throw new G3ChainError('幂等键工厂', '-', 0, 'invalid_idempotency_key', `生成的幂等键过短：${key}`)
  }
  return key
}

/** 一步调用的描述。 */
export interface G3StepSpec {
  /** 步骤名。 */
  name: string
  /** HTTP 方法。 */
  method: G3HttpMethod
  /** 完整路径。 */
  path: string
  /** 请求体。 */
  body?: unknown
  /** Bearer 令牌。 */
  token?: string
  /** 候选幂等键。发不发由幂等键策略决定。 */
  idempotencyKey?: string
  /** 明确不带凭据（只给存活探针用）。 */
  anonymous?: boolean
  /** 成功结论模板，可用 `{status}` 占位。 */
  conclusion?: string
  /** 允许失败：失败也记录，但不抛（例如存活探针的 401 是预期信号）。 */
  allowFailure?: boolean
  /** 从响应体里提取关键字段。 */
  detail?: (body: unknown) => Record<string, unknown> | undefined
}

/**
 * 执行一步真实调用并留下结构化记录。
 *
 * 顺序是刻意的：**先判幂等键策略，再发请求**。写路由缺键就直接拒绝发出去，
 * 因为一个缺键的写请求要么被 400 拒掉，要么更糟——写进去了但不可重放。
 */
export async function runStep(
  ctx: G3RunContext,
  client: G3HttpClient,
  spec: G3StepSpec,
): Promise<{ response: G3HttpResponse; data: unknown }> {
  const wantsKey = shouldSendIdempotencyKey(spec.method, spec.path)
  let outgoingKey: string | undefined
  if (wantsKey) {
    if (!spec.idempotencyKey) {
      throw new G3ChainError(
        spec.name,
        spec.path,
        0,
        'missing_idempotency_key',
        `写路由缺少幂等键，拒绝发出请求：${spec.method} ${spec.path}`,
      )
    }
    if (spec.idempotencyKey.trim().length < G3_IDEMPOTENCY_MIN_LENGTH
      || spec.idempotencyKey.trim().length > G3_IDEMPOTENCY_MAX_LENGTH) {
      throw new G3ChainError(
        spec.name,
        spec.path,
        0,
        'invalid_idempotency_key',
        `幂等键长度不合规（需 ${G3_IDEMPOTENCY_MIN_LENGTH}–${G3_IDEMPOTENCY_MAX_LENGTH} 字符）：${spec.idempotencyKey}`,
      )
    }
    outgoingKey = spec.idempotencyKey
  }

  let response: G3HttpResponse
  try {
    response = await client({
      method: spec.method,
      path: spec.path,
      body: spec.body,
      token: spec.token,
      idempotencyKey: outgoingKey,
      anonymous: spec.anonymous,
    })
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    recordStep(ctx, {
      name: spec.name,
      method: spec.method,
      path: spec.path,
      status: 0,
      ok: false,
      idempotencyKey: outgoingKey,
      durationMs: 0,
      conclusion: '请求没有发出去：网络不可达',
      error: message,
    })
    throw new G3ChainError(spec.name, spec.path, 0, 'network_error', `网络不可达：${message}`)
  }

  const failure = response.ok ? '' : describeHttpFailure(response.status, response.body)
  // 允许失败的步骤（例如存活探针的 401）用自己写的结论说明，
  // 别把「未带凭据的 401」写成「认证失败」——那会误导人。
  const usesOwnConclusion = !response.ok && Boolean(spec.allowFailure && spec.conclusion)
  const conclusion = response.ok
    ? (spec.conclusion ?? `成功（HTTP ${response.status}）`).replace('{status}', String(response.status))
    : usesOwnConclusion
      ? (spec.conclusion as string).replace('{status}', String(response.status))
      : failure

  recordStep(ctx, {
    name: spec.name,
    method: spec.method,
    path: spec.path,
    status: response.status,
    ok: response.ok,
    idempotencyKey: outgoingKey,
    durationMs: response.durationMs,
    conclusion,
    details: spec.detail ? spec.detail(response.body) : undefined,
    error: response.ok || usesOwnConclusion ? undefined : failure,
  })

  if (!response.ok && !spec.allowFailure) {
    throw new G3ChainError(
      spec.name,
      spec.path,
      response.status,
      errorCodeOf(response.body),
      failure,
    )
  }

  return { response, data: envelopeData(response.body) }
}

/** 追加一条步骤记录。 */
export function recordStep(ctx: G3RunContext, step: Omit<G3StepRecord, 'index'>): G3StepRecord {
  const record: G3StepRecord = { ...step, index: ctx.steps.length + 1 }
  ctx.steps.push(record)
  return record
}

/**
 * 存活探针：`GET /api/v1/version` **不带凭据**。
 *
 * 鉴权中间件在路由之前返回 401（`internal/middleware/auth.go:36-63`），
 * 所以「401」在这里是**服务活着**的正常信号，不是失败。
 */
export async function probeBackendAlive(
  ctx: G3RunContext,
  client: G3HttpClient,
): Promise<number> {
  const probe = await runStep(ctx, client, {
    name: '后端存活探针',
    method: 'GET',
    path: '/api/v1/version',
    anonymous: true,
    allowFailure: true,
    conclusion: '存活探针返回 {status}（未带凭据的 401 表示服务存活）',
  })
  return probe.response.status
}

/**
 * 读后端 head：`GET /api/v1/system/info` 带令牌，取编译期注入的 `commit_id`。
 *
 * head 才是结论的归属依据——证据必须挂在确定的代码版本上。
 */
export async function readBackendHead(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
): Promise<G3BackendHead | null> {
  const alive = ctx.steps.find((step) => step.name === '后端存活探针')
  const info = await runStep(ctx, client, {
    name: '读取后端 head',
    method: 'GET',
    path: '/api/v1/system/info',
    token,
    allowFailure: true,
    conclusion: '读取后端 head 成功（HTTP {status}）',
  })

  const data = (info.data ?? {}) as Record<string, unknown>
  const commitId = typeof data.commit_id === 'string' && data.commit_id ? data.commit_id : ''
  if (!commitId) {
    return null
  }

  const head: G3BackendHead = {
    commitId,
    buildTime: typeof data.build_time === 'string' ? data.build_time : undefined,
    version: typeof data.version === 'string' ? data.version : undefined,
    probedAt: new Date().toISOString(),
    probePath: '/api/v1/version',
    probeStatus: alive?.status ?? 0,
  }
  ctx.head = head
  return head
}

/** 两个 head 是否不同。任一未知时不判定为不同——没证据不等于有变化。 */
export function headsDiffer(previous: G3BackendHead | null, current: G3BackendHead | null): boolean {
  if (!previous || !current) return false
  return previous.commitId !== current.commitId
}

/** 把一次运行的结论标为过期。 */
export function markRunStale(ctx: G3RunContext, reason: string): void {
  ctx.stale = true
  ctx.staleReason = reason
}
