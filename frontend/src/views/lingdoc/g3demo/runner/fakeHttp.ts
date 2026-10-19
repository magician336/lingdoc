/**
 * G3 验收台 · 单测用的假 HTTP 客户端。
 *
 * 只给测试用：它按「路径 → 响应」应答，并记录收到的每个请求，
 * 于是「调用顺序、幂等键、错误处理」都能在不起后端的情况下断言。
 */

import type { G3HttpClient, G3HttpRequest, G3HttpResponse } from './httpClient'

/** 假客户端的一次应答。 */
export interface G3FakeReply {
  status: number
  body?: unknown
}

/** 假客户端。 */
export interface G3FakeClient {
  client: G3HttpClient
  /** 收到的请求，按顺序。 */
  calls: G3HttpRequest[]
  /** 路径 → 应答。 */
  reply: (path: string, reply: G3FakeReply) => void
}

/** 造一个假客户端。 */
export function createFakeClient(): G3FakeClient {
  const calls: G3HttpRequest[] = []
  const replies = new Map<string, G3FakeReply>()

  const client: G3HttpClient = async (request: G3HttpRequest): Promise<G3HttpResponse> => {
    calls.push(request)
    const reply = replies.get(request.path) ?? { status: 404, body: { error: { code: 'not_found' } } }
    return {
      status: reply.status,
      ok: reply.status >= 200 && reply.status < 300,
      body: reply.body ?? {},
      durationMs: 1,
    }
  }

  return {
    client,
    calls,
    reply: (path, reply) => {
      replies.set(path, reply)
    },
  }
}
