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
 * `replace_existing` 翻了（这一章从空变成有版本），都是**另一次**请求，共用一把键只会
 * 让服务端把它判成冲突。反过来，用户点「重试」而输入没变时，必须仍用原来那把键，
 * 否则一次网络超时重试会把整章替换做两遍。
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
