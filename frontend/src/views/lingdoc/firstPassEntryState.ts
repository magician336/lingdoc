import type { Project } from '@/api/lingdoc/workspace'
import type { ProjectInvitation } from '@/api/lingdoc/collaboration'
import type { TemplateCatalogVersion } from '@/api/lingdoc/templateGovernance'
import type { G2VersionRef } from '@/api/lingdoc/g2'

export function publishedChoices(versions: TemplateCatalogVersion[]) {
  const unique = new Map<string, TemplateCatalogVersion>()
  for (const version of versions) {
    if (version.state !== 'published') continue
    const key = `${version.template_id}@${version.version}`
    if (key !== 'template-demo@1') unique.set(key, version)
  }
  return [...unique.values()]
}

export function resolveTemplateChoice(key: string, versions: TemplateCatalogVersion[]) {
  if (key === 'template-demo@1') return { template_id: 'template-demo', template_version: '1' }
  const chosen = versions.find(v => v.state === 'published' && `${v.template_id}@${v.version}` === key)
  if (!chosen) throw new Error('所选模板版本已不可用，请刷新目录并重新选择；未改用演示模板。')
  return { template_id: chosen.template_id, template_version: chosen.version }
}

const roles: Record<string, string> = { owner: '课题所有者', admin: '课题管理员', member: '成员', author: '写作者', researcher: '调研者', reviewer: '评审者', observer: '观察者' }

// g5-invitation-workbench-20261014：邀请在收件箱中的可交互状态。
// 仅 status==='invited' 且未过期的邀请允许“接受”；其余只作历史展示，避免失效邀请继续显示可接受。
export type InvitationViewState = 'active' | 'expired' | 'accepted' | 'closed'
export function invitationViewState(invite: ProjectInvitation, now: Date = new Date()): InvitationViewState {
  const status = invite.status || ''
  if (status === 'accepted') return 'accepted'
  if (status !== 'invited') return 'closed'
  const expires = new Date(invite.expires_at)
  if (Number.isNaN(expires.getTime()) || expires.getTime() <= now.getTime()) return 'expired'
  return 'active'
}
export function invitationStatusLabel(invite: ProjectInvitation, now?: Date): string {
  switch (invitationViewState(invite, now)) {
    case 'active': return '待接受'
    case 'expired': return '已过期'
    case 'accepted': return '已接受'
    default: return '已失效'
  }
}

// 范围摘要：只写数量，不展示原始 ID、不臆造章节标题（未授权时不读取课题正文）。
// 精确原始 ID 由 invitationScopeDetail 单独提供，折叠在每条邀请的详情里供核对。
export function invitationAssignment(invite: ProjectInvitation) {
  const member = invite.assignment
  const lines = [roles[member.governance_role || 'member'] || member.governance_role || '成员']
  for (const role of member.function_roles || []) {
    const scope = member.function_scopes?.[role] || []
    const unit = role === 'author' || role === 'reviewer' ? ['章节', '章'] : role === 'researcher' ? ['资料', '项'] : ['对象', '项']
    lines.push(scope.length ? `${roles[role] || role}：指定${unit[0]}（${scope.length} ${unit[1]}）` : `${roles[role] || role}：尚未分配对象，不授予该职能的对象操作权限`)
  }
  return lines.join('；')
}
export function invitationScopeDetail(invite: ProjectInvitation): string[] {
  const member = invite.assignment
  const lines: string[] = []
  for (const role of member.function_roles || []) {
    const scope = member.function_scopes?.[role] || []
    if (scope.length) lines.push(`${roles[role] || role}：${scope.join('、')}`)
  }
  return lines
}

// Content saves change project_version; they do not change this identity and
// assignment fingerprint. Previously verified local editability can remain
// during that refresh. Server authorization still runs on every write.
export function permissionIdentity(project: Project, userId: string | undefined, session: number, tenantId?:number|null) {
  return JSON.stringify([tenantId,project.id, userId, session, project.status, project.members])
}

export function toggleEvidenceSelection(current: G2VersionRef[], incoming: G2VersionRef[]) {
  const next = new Map(current.map(ref => [ref.id, { ...ref }]))
  for (const ref of incoming) {
    if (next.get(ref.id)?.version === ref.version) next.delete(ref.id)
    else next.set(ref.id, { id: ref.id, version: ref.version })
  }
  return [...next.values()]
}
