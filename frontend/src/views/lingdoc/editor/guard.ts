// 独立复核反例的模块级防线（合同"必须纠正的原型反例"）：
// 反例2 确认后编辑仍 confirmed/可导出；反例4 章A选段→切章B→刷新→候选ch=B但quote=A。

export interface EditorContext { projectId: string; chapterId: string; bodyVersion: number }
export interface Candidate { ctx: EditorContext; quote: string; createdAt: number }

export type CandidateValidity =
  | { ok: true }
  | { ok: false; reason: 'chapter-mismatch' | 'project-mismatch' | 'stale-version' | 'empty-selection' }

// 候选只在创建它的 课题+章+正文版本 下有效。
// "刷新"不是复活动作：任何上下文变化都把候选作废，必须重新选择文本重建候选。
export function validateCandidate(cand: Candidate | null, current: EditorContext): CandidateValidity {
  if (!cand) return { ok: false, reason: 'empty-selection' }
  if (cand.ctx.projectId !== current.projectId) return { ok: false, reason: 'project-mismatch' }
  if (cand.ctx.chapterId !== current.chapterId) return { ok: false, reason: 'chapter-mismatch' }
  if (cand.ctx.bodyVersion !== current.bodyVersion) return { ok: false, reason: 'stale-version' }
  if (!cand.quote.trim()) return { ok: false, reason: 'empty-selection' }
  return { ok: true }
}

export function invalidateOnContextChange(cand: Candidate | null, current: EditorContext): Candidate | null {
  return validateCandidate(cand, current).ok ? cand : null
}

/* ---- 确认状态诚实性 ---- */

export interface ConfirmationSnapshot { chapterVersionId: string; confirmedAtRevision: number }

// 章级确认只在"确认时点的正式版本"上有效；本地正文再编辑（bodyVersion 推进或草稿脏）即失效。
// 返回 null 表示仍有效；否则返回可读的失效原因（阻断当前稿确认/导出，旧快照仍可历史下载）。
export function confirmationInvalidReason(
  confirmed: ConfirmationSnapshot | null,
  local: { committedChapterVersionId: string | null; bodyVersion: number; hasUnsavedChanges: boolean },
): string | null {
  if (!confirmed) return null
  if (local.hasUnsavedChanges) return '正文有未保存修改：当前稿已不是已确认版本，确认/导出已阻断；请先保存并重新提交。'
  if (local.committedChapterVersionId !== confirmed.chapterVersionId) {
    return '正文已变更：已确认版本失效（旧快照可在历史版本中下载）。'
  }
  return null
}

// 风险4修订：确认基线取真实正式章版本，不拿确认记录自证。
// committedRevision = 最近一次"提交正式版本"的版本号；savedRevision = 工作副本已保存到的 revision。
// 已保存但未提交（saved > committed）同样阻断当前稿确认/导出。
export function draftBlockReason(state: {
  dirty: boolean
  conflict: boolean
  savedRevision: number
  committedRevision: number
}): string | null {
  if (state.conflict) return '保存发生版本冲突，当前输入已保留；请重新读取后处理。'
  if (state.dirty) return '当前稿有未保存修改，请先保存（Ctrl+S）。'
  if (state.savedRevision > state.committedRevision) return '已保存但未提交正式版本：请提交后再确认/导出当前稿。'
  return null
}
