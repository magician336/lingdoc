# 来源定位以仓库字符区间为准，契约字段只承载对外形状

**Status**: accepted (2026-09-23, T03)

契约 `02-接口与Mock约定.md §5` 与 `contracts/openapi.json` 把来源定位定义为一个自由字符串 `locator`（样例值 `"测试段落 1"`）加 `quoted_text_hash`。但 `quoted_text_hash` 只证明引文**没被篡改**，不证明引文**出自那份原文**——一份编造的 `quoted_text` 只要 hash 自洽就能通过，而「能指回原文」正是 T03 要证明的事。仓库侧 `types.SearchResult`（`internal/types/search.go:151-227`）其实已经提供 `StartAt`/`EndAt`（原文字符区间）、`ChunkIndex`、`KnowledgeID`、`ID` 以及失效钩子 `ContentRevision`/`ContentRewritten`。

因此决定：**内部判据用仓库的字符区间断言 `原文[StartAt:EndAt] == QuotedText`，只把结果映射进契约的 `locator`/`quoted_text_hash` 对外**。术语表 §2 对该形态已有定义——「来源锚点 `SourceAnchor`：能回到材料中具体页、段、表、图或**字符区间**的位置描述；应支持**重定位和失效检测**。不应混同为 WeKnora 的裸 `chunk_id`」。字符区间 + 失效钩子正是这段要求的实现，而只走 `locator` 字符串两样都做不到。

## Considered Options

- **只用契约 `locator` 字符串**：无法机器校验出处，只能靠人工肉眼比对，且回归时无从断言。
- **只存裸 `chunk_id`**：术语表 §2 明确禁止；chunk 内容会被 merge pipeline 展开重写，UUID 本身不携带坐标，失效后无从判断。
- **字符区间 + 失效标记**（选定）：可机器断言出处，可检测失效。

## Consequences

`ContentRevision` 与 `ContentRewritten` 在 `types.SearchResult` 上是 `json:"-"`，**序列化即丢失**。所以失效检测（`Status == stale`）必须在**同一进程内**完成，不能靠回读持久化的 JSON 补判。任何把 `Source` 落库再回读的用法都会丢掉失效信息——下游若要跨进程判断来源是否仍可定位，必须重新走一次检索，而不是读旧记录。

第二个后果更重（2026-09-23 查证）：**全量解析产物没有落库**。`types.Knowledge` 没有正文字段，`Chunk.SourceContent` 只是逐块产物，而现有代码把 `StartAt`/`EndAt` 当相对量使用（`merge_overlap.go:41` 算重叠、`chunk.go:630` 抵到父块、`merge.go:270` 称其为 editable 且自己会改写 `EndAt`）。因此"按坐标去原文取回比对"在生产路径上没有落点，本判据实际分两档：原始资料是纯文本时可做强断言，其余格式只能做相对自洽（`runeLen(Content) == EndAt-StartAt` ∧ `ContentRevision == 0` ∧ `!ContentRewritten`）。要提升到强档需要一个保存解析产物的位置——超出本决定的范围，登记给 T09。

另外，偏移单位是 **rune 而非 byte**（`merge_overlap.go:98` 的 `runeLen(Content) == EndAt-StartAt`）。按字节切会静默切错中文，这是强档断言存在的主要理由。
