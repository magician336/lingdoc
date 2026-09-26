import type { ExportArtifact, ValidationIssue } from '@/api/lingdoc/delivery'

// 交付面板的判定逻辑，与渲染分开。
//
// 这三件事各对应一条最小验收，而且都是「界面不该自己猜」的地方——判定写成纯函数，
// 才能在不搭 DOM 的情况下把它们钉住。

/**
 * 契约 §7 点名的三态。
 *
 * 「当前性未知」不是一个占位符，它是这组取值里信息量最大的一个：**尚未读取**与
 * **读取失败**都必须落到它，绝不允许因为产物是 verified 就说它「当前」。一份文件
 * 做出来了，与它是不是还代表工作区，是两件独立的事。
 */
export type Currency = '当前内容' | '历史版本' | '当前性未知'

/**
 * 依据快照的 is_current 判定当前性。
 *
 * 判据写成 `=== true` / `=== false` 而不是真假判断：字段缺席时 `!is_current` 会得出
 * 「历史版本」，那是一个**我们没有依据下的结论**。不确定就是不确定。
 *
 * 传 undefined 表示这次没读到（还没读，或读失败了）——调用方必须把读取失败显式降级成
 * undefined，而不是沿用上一次的结论。
 */
export function currencyOf(snapshot: { is_current: boolean } | undefined | null): Currency {
  if (snapshot?.is_current === true) return '当前内容'
  if (snapshot?.is_current === false) return '历史版本'
  return '当前性未知'
}

/**
 * 一份产物在界面上是什么样子。
 *
 * `downloadable` 要求 `file_sha256` 与 `download_path` **都在**：契约把它们标成
 * required + nullable，而一个指向不存在文件的下载地址比 null 更糟——界面会照着它给出
 * 一个点了就 422 的按钮。这就是「失败不提供假下载」在判定这一侧的全部含义。
 *
 * `unavailable` 是「没有可下载的文件，也没有一句能展示的失败说明」：还没做出来的
 * （queued）、自相矛盾的（verified 却缺文件）、以及认不出的状态都落在这里。
 */
export type ArtifactView =
  | { kind: 'none' }
  | { kind: 'downloadable'; downloadPath: string; fileSHA256: string }
  | { kind: 'failed'; code: string; message: string; retryable: boolean }
  | { kind: 'unavailable' }

export function viewOf(artifact: ExportArtifact | undefined | null): ArtifactView {
  if (!artifact) return { kind: 'none' }
  if (artifact.status === 'verified') {
    const { download_path: downloadPath, file_sha256: fileSHA256 } = artifact
    if (downloadPath && fileSHA256) return { kind: 'downloadable', downloadPath, fileSHA256 }
    return { kind: 'unavailable' }
  }
  if (artifact.status === 'failed') {
    // 失败却没带说明的产物：说不出「为什么」与「值不值得重试」，就不能把它渲染成一条
    // 可执行的恢复建议。宁可不给按钮，也不给一个猜出来的建议。
    const failure = artifact.error
    if (!failure) return { kind: 'unavailable' }
    return { kind: 'failed', code: failure.code, message: failure.message, retryable: failure.retryable }
  }
  return { kind: 'unavailable' }
}

/** 项目级发现的名字。它不是某个章节，而是整个项目。 */
const projectWideLabel = '整个项目'

/**
 * 把 issue.target_id 翻成用户认得的名字——「阻断原因可定位」就是靠它。
 *
 * target_id 可能是章节 ID、项目 ID，或 unresolved-project（一条项目级、却没有项目 ID
 * 可指的发现）。后两者对用户是同一件事：这个缺陷不属于任何一章。
 *
 * 认不出来就**原样显示 ID**，不猜。宁可让用户看到一个陌生标识，也不能把一个章节点成
 * 另一个章节——那会把定位引到错的地方，比不定位更坏。
 */
export function issueTargetLabel(
  issue: Pick<ValidationIssue, 'target_id'>,
  projectId: string,
  chapters: Array<{ id: string; title: string }>,
): string {
  const target = issue.target_id
  if (target === 'unresolved-project' || target === projectId) return projectWideLabel
  const chapter = chapters.find((item) => item.id === target)
  // 标题为空时落回 ID：空字符串在界面上是一个没写字的标签，读起来像界面坏了。
  if (chapter && chapter.title.trim()) return chapter.title
  return target
}
