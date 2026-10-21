/**
 * G3 验收台 · 单测用的假 HTTP 客户端。
 *
 * 只给测试用：它按「路径 → 响应」应答，并记录收到的每个请求，
 * 于是「调用顺序、幂等键、错误处理」都能在不起后端的情况下断言。
 *
 * 应答可以按**方法**与**令牌**细分（同一路径在不同身份下的答案本来就不同：
 * Owner 调 apply 成功、非 Owner 调 apply 得到 404）。
 * 匹配优先级从具体到宽泛：
 * `方法+路径+令牌` → `方法+路径` → `路径+令牌` → `路径`。
 *
 * 登记语义：
 * - `reply` / `replyFor` **覆盖**已登记的应答（后登记者胜）。
 * - `replySequence` / `replySequenceFor` 登记**一串**应答：按顺序逐个应答，
 *   最后一个会一直复用（用于「同一个端点在不同时间返回不同内容」的场景）。
 */

import type { G3HttpClient, G3HttpRequest, G3HttpResponse } from './httpClient'
import { isSuccessStatus } from './factHelpers'

/** 假客户端的一次应答。 */
export interface G3FakeReply {
  status: number
  body?: unknown
  /** 原始字节（给下载产物用）。 */
  bytes?: Uint8Array
}

/** 假客户端。 */
export interface G3FakeClient {
  client: G3HttpClient
  /** 收到的请求，按顺序。 */
  calls: G3HttpRequest[]
  /** 路径 → 应答（覆盖同路径的旧应答）。 */
  reply: (path: string, reply: G3FakeReply) => void
  /** 方法 + 路径（可再限令牌）→ 应答（覆盖同键的旧应答）。 */
  replyFor: (
    method: string,
    path: string,
    reply: G3FakeReply,
    condition?: { token?: string },
  ) => void
  /** 路径 → 一串应答（按顺序应答，最后一个一直复用）。 */
  replySequence: (path: string, replies: G3FakeReply[]) => void
  /** 方法 + 路径（可再限令牌）→ 一串应答。 */
  replySequenceFor: (
    method: string,
    path: string,
    replies: G3FakeReply[],
    condition?: { token?: string },
  ) => void
}

function keyOf(method: string | undefined, path: string | undefined, token: string | undefined): string {
  return `${(method ?? '').toUpperCase()} ${path ?? ''} @${token ?? ''}`
}

function candidatesOf(request: G3HttpRequest): string[] {
  const method = request.method.toUpperCase()
  const path = request.path
  const token = request.token ?? ''
  return [
    keyOf(method, path, token),
    keyOf(method, path, undefined),
    keyOf(undefined, path, token),
    keyOf(undefined, path, undefined),
  ]
}

/** 造一个假客户端。 */
export function createFakeClient(): G3FakeClient {
  const calls: G3HttpRequest[] = []
  const replies = new Map<string, G3FakeReply[]>()

  const client: G3HttpClient = async (request: G3HttpRequest): Promise<G3HttpResponse> => {
    calls.push(request)
    let reply: G3FakeReply | undefined
    for (const key of candidatesOf(request)) {
      const queue = replies.get(key)
      if (!queue || queue.length === 0) continue
      reply = queue.length > 1 ? queue.shift() : queue[0]
      break
    }
    const settled = reply ?? { status: 404, body: { error: { code: 'not_found' } } }
    return {
      status: settled.status,
      ok: isSuccessStatus(settled.status),
      body: settled.body ?? {},
      bytes: settled.bytes,
      durationMs: 1,
    }
  }

  const set = (key: string, list: G3FakeReply[]): void => {
    replies.set(key, list.length > 0 ? [...list] : [list[0]])
  }

  return {
    client,
    calls,
    reply: (path, reply) => {
      set(keyOf(undefined, path, undefined), [reply])
    },
    replyFor: (method, path, reply, condition) => {
      set(keyOf(method, path, condition?.token), [reply])
    },
    replySequence: (path, list) => {
      set(keyOf(undefined, path, undefined), list)
    },
    replySequenceFor: (method, path, list, condition) => {
      set(keyOf(method, path, condition?.token), list)
    },
  }
}
