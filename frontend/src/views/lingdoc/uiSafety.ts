export interface ReviewDraft {
  disposition: 'resolved' | 'retained_warning'
  reason: string
}

export function reviewDraftsAreDirty(drafts: Record<string, ReviewDraft>, saved: Record<string, ReviewDraft>): boolean {
  return Object.entries(drafts).some(([key, draft]) => {
    const baseline = saved[key] ?? { disposition: 'resolved', reason: '' }
    return draft.disposition !== baseline.disposition || draft.reason !== baseline.reason
  })
}

export interface ConditionsChangeInputs {
  projectId: string
  reason: string
  chapterIds: string[]
}

// Match the submitted meaning; checkbox order and surrounding reason whitespace do not add changes.
export function conditionsChangeSnapshot(projectId: string, reason: string, chapterIds: string[]): ConditionsChangeInputs {
  return { projectId, reason: reason.trim(), chapterIds: [...chapterIds].sort() }
}

export function conditionsChangeInputsAreDirty(draft: ConditionsChangeInputs, acknowledged: ConditionsChangeInputs): boolean {
  return draft.projectId !== acknowledged.projectId || draft.reason !== acknowledged.reason ||
    JSON.stringify(draft.chapterIds) !== JSON.stringify(acknowledged.chapterIds)
}

export function hasUnsavedInputs(state: {
  chapterChanged: boolean; specChanged: boolean; templateChanged: boolean
  saving: boolean; reviewChanged: boolean; conditionsChanged?: boolean
}): boolean {
  return state.chapterChanged || state.specChanged || state.templateChanged || state.saving || state.reviewChanged || state.conditionsChanged === true
}

export function canLeaveWorkspace(dirty: boolean, confirm: (message: string) => boolean): boolean {
  return !dirty || confirm('当前有尚未保存的输入或保存中的请求，离开会丢失未保存内容。确定离开吗？')
}

export function installUnloadGuard(target: Pick<Window, 'addEventListener' | 'removeEventListener'>, dirty: () => boolean): () => void {
  const handler: EventListener = (event) => {
    if (!dirty()) return
    event.preventDefault()
    // Browsers choose their own warning text. Do not attempt async saves here.
    ;(event as BeforeUnloadEvent).returnValue = ''
  }
  target.addEventListener('beforeunload', handler)
  return () => target.removeEventListener('beforeunload', handler)
}

export function confirmationBlockReasons(state: {
  busy: boolean; saving: boolean; unsaved: boolean; needsCommit: boolean; missingReasons: number
}): string[] {
  const reasons: string[] = []
  if (state.saving) reasons.push('工作副本正在保存，请等待保存完成')
  else if (state.busy) reasons.push('当前操作尚未完成，请稍候')
  if (state.unsaved) reasons.push('请先保存当前修改')
  if (state.needsCommit) reasons.push('请先将工作副本提交为正式版本')
  if (state.missingReasons > 0) reasons.push(`还有 ${state.missingReasons} 条待核事项未填写处理理由`)
  return reasons
}
