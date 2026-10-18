import { currentAnchorRange } from './editor/documentState'

// 待核审阅面板的纯状态助手（契约不变：仅 resolved / retained_warning + reason）。
// R1 退修：必须「显式选择处理方式 + 理由非空」两项都满足才算已处理；
// 只填理由、只点选项、或沿用创建默认值，一律视为未处理。
export type ReviewDisposition = 'resolved' | 'retained_warning'

export interface ReviewDraftLike {
  disposition: ReviewDisposition
  reason: string
  explicitlyChosen?: boolean
}

export type ReviewItemStatus = 'pending' | 'resolved' | 'retained'

export function reviewItemStatus(draft: ReviewDraftLike | undefined): ReviewItemStatus {
  if (!draft || draft.explicitlyChosen !== true || !draft.reason.trim()) return 'pending'
  return draft.disposition === 'retained_warning' ? 'retained' : 'resolved'
}

export function reviewStatusLabel(status: ReviewItemStatus): string {
  return ({ pending: '未处理', resolved: '已处理 · 已解决', retained: '已处理 · 保留提醒' })[status]
}

// Display uses the same exact UTF-16/quote check as locating; stored status alone is not current proof.
export interface ReviewAnchorDisplay {
  status?: string
  start_utf16?: number
  end_utf16?: number
  quote_markdown?: string
}
export type ReviewAnchorState = 'located' | 'stale' | 'none' | 'unchecked'

export function reviewAnchorState(anchor: ReviewAnchorDisplay | null | undefined, markdown?: string): ReviewAnchorState {
  if (anchor?.status === 'stale') return 'stale'
  if (anchor?.status !== 'located') return 'none'
  if (markdown === undefined || typeof anchor.start_utf16 !== 'number' || typeof anchor.end_utf16 !== 'number' || typeof anchor.quote_markdown !== 'string') return 'unchecked'
  return currentAnchorRange(markdown, { status: 'located', start_utf16: anchor.start_utf16, end_utf16: anchor.end_utf16, quote_markdown: anchor.quote_markdown }) ? 'located' : 'unchecked'
}

export function reviewAnchorHint(anchor: ReviewAnchorDisplay | null | undefined, markdown?: string): string {
  return ({ located: '已关联当前原文', stale: '关联已失效', none: '未关联片段', unchecked: '当前稿关联待核对' })[reviewAnchorState(anchor, markdown)]
}

export interface ReviewProgress {
  done: number
  total: number
}

export function reviewProgress<T>(items: T[], keyOf: (item: T) => string, drafts: Record<string, ReviewDraftLike>): ReviewProgress {
  let done = 0
  for (const item of items) {
    if (reviewItemStatus(drafts[keyOf(item)]) !== 'pending') done++
  }
  return { done, total: items.length }
}
