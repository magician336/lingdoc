import type { AcceptCandidateInput, AdoptionConfirmation } from '@/api/lingdoc/candidateAdoption'

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

/**
 * 选段改写候选的事实：确认载荷要跟它对上的那几项。
 */
export interface RewriteConfirmationFacts {
  /** 产生这个候选的动作。旧路径的候选没有动作。 */
  writing_action?: string
  /** 候选正文的来源。 */
  origin?: 'human' | 'ai_generated' | 'ai_assisted_human'
  /** 这个候选的选区：部分采纳写的就是这一段。 */
  selection: { start_utf16: number; end_utf16: number }
}

/**
 * 选段改写的确认结果。
 *
 * 三种，不是两种：
 *
 * - `confirmed` —— 这次采纳带确认载荷。
 * - `legacy` —— 旧路径的候选，没有动作可确认，因此**不带**载荷。服务端把
 *   「有载荷但没有动作」判成非法请求，所以这一份必须什么都不带。
 * - `unconfirmable` —— 这一次**应该**确认，但缺了东西（来源、身份、时刻）。
 *   它和 `legacy` 混成一种的后果是：动作路径的候选因为缺来源而被静默地当成
 *   旧路径发出去，服务端拒，用户只看到一句「请求不合法」。
 */
export type RewriteConfirmationResult =
  | { kind: 'confirmed'; confirmation: AdoptionConfirmation }
  | { kind: 'legacy' }
  | { kind: 'unconfirmable'; reason: string }

/**
 * 组装选段改写的显式确认载荷。
 *
 * 动作路径的候选必须带它：服务端要确认这次采纳的是哪个动作的结果、写到哪里、
 * 写到哪一段、谁在什么时候确认的、候选是什么来源。缺任何一项，服务层拒——
 * 所以这不是界面上的一个勾选框，而是请求体的一部分。
 *
 * 两条规则在这里落地：
 *
 * - **旧路径的候选不带载荷**。旧路径没有动作可确认，带了反而被拒（服务端把
 *   「有载荷但没有动作」判成非法请求）。所以缺 `writing_action` 时返回
 *   `legacy`，由调用方**不发**载荷。
 * - **采纳目标固定为「工作副本·选段」**。选段端点只实现这一种写入方式，
 *   另两个目标服务端会拒。范围就是候选的选区：这个端点写的就是那一段。
 */
export function rewriteAdoptionConfirmation(
  candidate: RewriteConfirmationFacts,
  confirmedBy: string,
  confirmedAt: string,
): RewriteConfirmationResult {
  if (!candidate.writing_action) return { kind: 'legacy' }
  if (!candidate.origin) {
    return { kind: 'unconfirmable', reason: '这个候选没有记录来源，无法确认采纳；请用动作面板重新运行后再采纳。' }
  }
  if (!confirmedBy.trim()) {
    return { kind: 'unconfirmable', reason: '当前登录身份未识别，无法确认采纳。' }
  }
  if (!confirmedAt) {
    return { kind: 'unconfirmable', reason: '确认时刻缺失，无法确认采纳。' }
  }
  return {
    kind: 'confirmed',
    confirmation: {
      writing_action: candidate.writing_action,
      adoption_target: 'working_copy_partial',
      scope: {
        start_utf16: candidate.selection.start_utf16,
        end_utf16: candidate.selection.end_utf16,
      },
      confirmed_at: confirmedAt,
      confirmed_by: confirmedBy,
      origin: candidate.origin,
    },
  }
}
