// 已选证据上下文（SelectedEvidenceContext.vue）的纯状态逻辑。
// 只读匹配与代次守卫集中在这里，组件保持薄接线；不写字库、不调用模型。
import type { EvidenceCard, VersionRef, ResearchClaim } from '@/api/lingdoc/agentApi'

export interface SelectedEvidenceRow {
  ref: VersionRef
  // ready：可完整展示；其余三种都是“待重新核对/当前无法核验”，不回退、不自动升级。
  state: 'ready' | 'version-mismatch' | 'not-confirmed' | 'claim-missing'
  card?: EvidenceCard
  claim?: ResearchClaim
}

// 同一 id@version 只保留一行，避免重复 refs 渲染出重复卡片。
export const dedupeRefs = (refs: VersionRef[]): VersionRef[] => {
  const seen = new Set<string>()
  return refs.filter(ref => {
    const key = `${ref.id}@${ref.version}`
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
}

// 只按 id+version 精确匹配：服务端不存在所选版本时标 version-mismatch，
// 绝不自动改用同名卡的其他版本，也不显示未选中的卡。
export const matchSelectedRows = (refs: VersionRef[], cards: EvidenceCard[], claims: ResearchClaim[]): SelectedEvidenceRow[] =>
  dedupeRefs(refs).map(ref => {
    const card = cards.find(item => item.id === ref.id && item.version === ref.version)
    if (!card) return { ref, state: 'version-mismatch' as const }
    if (card.status !== 'CONFIRMED') return { ref, state: 'not-confirmed' as const, card }
    const claimRef = card.claim_ref
    const claim = claimRef ? claims.find(item => item.id === claimRef.id && item.version === claimRef.version) : undefined
    if (claimRef && !claim) return { ref, state: 'claim-missing' as const, card }
    return { ref, state: 'ready' as const, card, claim }
  })

// 背景关系绝不标成“支持”；未知类型如实显示原值。
const RELATIONS: Record<string, string> = { supports: '支持', refutes: '反驳', qualifies: '限定条件', background: '背景' }
export const relationLabel = (supportType: string): string => RELATIONS[supportType] || supportType

const CARD_STATUS_LABELS: Record<string, string> = {
  CANDIDATE: '候选证据', CONFIRMED: '已人工确认适用范围', NEEDS_REVIEW: '需复核',
  INSUFFICIENT: '证据不足', STALE: '基线已变', SUPERSEDED: '已替代',
}
export const cardStatusLabel = (status: string): string => CARD_STATUS_LABELS[status] || status

export const rowProblemText = (row: SelectedEvidenceRow): string => {
  if (row.state === 'version-mismatch') return `待重新核对：服务端当前没有所选版本（v${row.ref.version}）的证据卡，不会自动改用其他版本。`
  if (row.state === 'not-confirmed' && row.card) return `待重新核对：证据卡当前为「${cardStatusLabel(row.card.status)}」，尚未人工确认适用范围。`
  if (row.state === 'claim-missing') return '当前无法核验：关联主张在所选版本不可用，不会自动升级选中版本。'
  return '当前无法核验：读取失败。'
}

// 读取代次：capture 取当前代，next 使旧响应失效，accepts 判定是否仍有效。
// 用于丢弃跨课题/跨身份/旧基线的晚响应，防止旧数据回填。
export const createRequestEpoch = () => {
  let generation = 0
  return {
    next: () => { generation += 1 },
    capture: () => generation,
    accepts: (token: number) => token === generation,
  }
}
export type RequestEpoch = ReturnType<typeof createRequestEpoch>

// 读取失败时区分“来源访问受限”（不可核验）与一般错误；不伪造数据。
export const isAccessDenied = (cause: unknown): boolean => {
  const problem = cause as { code?: string; status?: number; $httpStatus?: number } | null
  return problem?.code === 'source_access_denied' || problem?.$httpStatus === 403 || problem?.status === 403
}
