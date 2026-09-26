/**
 * 正文里的来源标记——提取与体检，与界面分开。
 *
 * 标记是**正文自己的**东西，不是另存的一个字段：服务端要求 `[[source:…]]` 出现的集合
 * 与提交的 `source_ids` 严格相等（workspacecore/service.go 的 SaveChapter），对不上
 * 就是 400。所以界面这一侧唯一站得住的做法，是按**同一条**规则从正文里把引用读出来，
 * 而不是在旁边维护一份、再指望两边不漂。
 *
 * 同一条规则也用来提前拦住写坏的标记：服务端数的是 `[[source:` 的出现次数与完整匹配数，
 * 两者不等就判 400。在这里先问一遍，用户拿到的是一句说清了哪里不对的话，而不是一次
 * 「请求字段不符合约定」。
 */

/**
 * 与 workspacecore 的 `sourceMarker` 逐字相同。
 *
 * 逐字相同不是巧合，是要求：两边一旦漂开，这里算出来的引用集合就会与服务端认的那一份
 * 对不上，而症状是保存按钮亮着、点下去却回一个 400——最难查的一类错，因为两边都「有实现」。
 */
const SOURCE_MARKER = /\[\[source:([A-Za-z0-9_-]+)\]\]/g
const MARKER_PREFIX = '[[source:'

/** 写坏的标记给用户看的话。比一次 400 多说了两件事：哪里不对，怎么改。 */
export const MALFORMED_CITATION_MESSAGE =
  '正文里的来源标记没有写完整（形如 [[source:来源标识]]），保存前请先修正。'

export type ChapterCitations =
  | { kind: 'ok'; sourceIds: string[] }
  | { kind: 'malformed' }

/**
 * 从正文里读出这次要提交的引用。
 *
 * 排序去重与服务端同一条，而且不只是为了「好看」：它决定落库的 JSON 逐字节长什么样，
 * 也决定幂等重放的指纹比对认不认得出「这是同一份正文」。顺序若跟着用户输入的先后走，
 * 同一组引用会算出不同的请求体，重放就会被判成「同一个键对应不同请求」。
 */
export function chapterCitations(body: string): ChapterCitations {
  const sourceIds: string[] = []
  let matched = 0
  for (const match of body.matchAll(SOURCE_MARKER)) {
    matched += 1
    sourceIds.push(match[1])
  }
  if (countOccurrences(body, MARKER_PREFIX) !== matched) return { kind: 'malformed' }
  return { kind: 'ok', sourceIds: [...new Set(sourceIds)].sort() }
}

/**
 * 出现次数，非重叠前移——与 Go 的 `strings.Count` 同一条定义。
 * 用正则去数前缀是错的：`[[source:` 里没有任何正则元字符之外还要担心的地方，
 * 但同一条规则用两种数法，迟早会在某个边界上不一致。
 */
function countOccurrences(haystack: string, needle: string): number {
  let count = 0
  let index = haystack.indexOf(needle)
  while (index >= 0) {
    count += 1
    index = haystack.indexOf(needle, index + needle.length)
  }
  return count
}
