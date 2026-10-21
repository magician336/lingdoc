import type { AcceptCandidateInput } from '@/api/lingdoc/candidateAdoption'

// 采纳对话框的幂等签名，与渲染分开（同 generationAttempt.ts / deliveryState.ts）。
//
// 服务端的判据是「同一个键 + 不同的规范化请求体 = 冲突」。所以签名必须**恰好**在请求体
// 变化时变化：漏了任何一项，两次不同的采纳会共用一个键，第二次拿到的是一份 409；多算了
// 不参与请求状态的东西，重试会换一把新键，同一次采纳被做两遍（这里就是整章替换两遍）。

/**
 * 把一次采纳的请求体折成一个可比较的字符串。
 *
 * 直接对**将要发出去的那个对象**序列化，而不是另抄一份字段清单：两份清单漂开的症状
 * 最难查——请求体变了、签名没变，用户看到的是「同一个操作键对应不同请求」，而两边都
 * 「有实现」。判据只有一处定义，就不可能漂。
 *
 * 确认载荷在请求体里，所以它**自动**在签名里：确认了不同的动作、不同的采纳目标或
 * 不同的范围，就是另一次采纳，必须换一把键。
 */
export function adoptionSignature(input: AcceptCandidateInput): string {
  return JSON.stringify(input)
}

/** 一次采纳尝试：签名 + 为它铸的那把键。 */
export interface AdoptionAttempt {
  signature: string
  key: string
}

/**
 * 该复用上一次的键，还是为这次请求新铸一把。
 *
 * 逐字相同签名才复用：`expected_chapter_version_id` 变了（别人刚存了一版）、
 * `replace_existing` 翻了（这一章从空变成有版本）、确认载荷变了（确认了另一个
 * 动作或另一个采纳目标），都是**另一次**请求，共用一把键只会让服务端把它判成冲突。
 * 反过来，用户点「重试」而输入没变时，必须仍用原来那把键，否则一次网络超时重试
 * 会把整章替换做两遍。
 *
 * `mint` 由调用方注入，测试因此不必碰 crypto。
 */
export function keyForAttempt(
  attempt: AdoptionAttempt | null | undefined,
  input: AcceptCandidateInput,
  mint: () => string,
): AdoptionAttempt {
  const signature = adoptionSignature(input)
  if (attempt?.signature === signature) return attempt
  return { signature, key: mint() }
}

/**
 * 工作副本正文的摘要。
 *
 * 算法是契约的一部分：**正文的 UTF-8 字节的 SHA-256，小写十六进制、64 个
 * 字符**。服务端算的是同一个值（`candidateadoption.workingCopyBodyHash`），
 * 两边算不出同一个值，这一声明就白声明了。
 *
 * 用 Web Crypto 而不是自己写一份 SHA-256：前者是浏览器与 Node 都有的标准
 * 实现，后者是第二份需要维护的密码学代码。
 */
export async function workingCopyBodyHash(body: string): Promise<string> {
  const bytes = new TextEncoder().encode(body)
  const digest = await crypto.subtle.digest('SHA-256', bytes)
  return Array.from(new Uint8Array(digest)).map(byte => byte.toString(16).padStart(2, '0')).join('')
}

/**
 * 铸一个快照标识。
 *
 * 它是「带快照的替换」的一次性标记：服务端把它与替换前的工作副本正文一起
 * 记进审计详情，人工据此可以恢复。
 */
export function newSnapshotId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return `candidate-adoption-snapshot-${crypto.randomUUID()}`
  }
  return `candidate-adoption-snapshot-${Date.now()}-${Math.random().toString(36).slice(2)}`
}
