import type { ProjectMember, ProjectMemberInput, ProjectFunctionRole } from '@/api/lingdoc/workspace'

export const projectFunctionRoles: ProjectFunctionRole[] = ['author', 'researcher', 'reviewer', 'observer']

export interface ProjectMemberDraft extends ProjectMemberInput {
  function_roles: ProjectFunctionRole[]
  function_scopes: Record<string, string[]>
}

export function createProjectMemberDrafts(members: ProjectMember[]): ProjectMemberDraft[] {
  return members
    .filter(member => member.role !== 'owner')
    .map(member => ({
      user_id: member.user_id,
      role: 'collaborator',
      governance_role: member.governance_role === 'admin' ? 'admin' : 'member',
      function_roles: [...(member.function_roles ?? [])],
      function_scopes: Object.fromEntries(Object.entries(member.function_scopes ?? {}).map(([role, scopes]) => [role, [...scopes]])),
      status: member.status ?? 'active',
    }))
}

export function chapterScopesForSelection(scopes: string[] = [], chapterIds?: string[]): string[] {
  if (!chapterIds) return scopes.filter(scope => !scope.startsWith('asset:') && !scope.startsWith('delivery:'))
  const knownChapters = new Set(chapterIds)
  return scopes.filter(scope => knownChapters.has(scope))
}

export function setChapterScopes(scopes: string[] = [], selectedChapterIds: string[], chapterIds?: string[]): string[] {
  const editableChapters = new Set(chapterIds ?? scopes.filter(scope => !scope.startsWith('asset:') && !scope.startsWith('delivery:')))
  return [...new Set([
    ...scopes.filter(scope => !editableChapters.has(scope)),
    ...selectedChapterIds,
  ])]
}

export function assetIdsForSelection(scopes: string[] = []): string[] {
  return scopes.filter(scope => scope.startsWith('asset:')).map(scope => scope.slice('asset:'.length))
}

export function setAssetScopes(scopes: string[] = [], selectedAssetIds: string[]): string[] {
  return [...new Set([
    ...scopes.filter(scope => !scope.startsWith('asset:')),
    ...selectedAssetIds.map(id => `asset:${id}`),
  ])]
}

export function setProjectFunctionRole(member: ProjectMemberDraft, role: ProjectFunctionRole, enabled: boolean): void {
  const roles = new Set(member.function_roles)
  if (enabled) roles.add(role)
  else roles.delete(role)
  member.function_roles = projectFunctionRoles.filter(item => roles.has(item))
  if (!enabled) delete member.function_scopes[role]
  else member.function_scopes[role] ??= []
}

export function hasUnsupportedMemberState(member: ProjectMember): boolean {
  return member.role !== 'owner' && member.status !== undefined
    && member.status !== 'active' && member.status !== 'suspended'
}

export function hasOrphanedFunctionScopes(member: ProjectMember): boolean {
  const roles = new Set(member.function_roles ?? [])
  return Object.keys(member.function_scopes ?? {}).some(role => !roles.has(role as ProjectFunctionRole))
}

export function hasUnsupportedFunctionRoles(member: ProjectMember): boolean {
  return (member.function_roles ?? []).some(role => !projectFunctionRoles.includes(role))
}

export function effectiveCapabilitySummary(member: ProjectMember): string {
  if (member.status === 'suspended') return '项目侧已暂停：当前无项目访问权限'
  if (member.status === 'suspended_by_tenant') return '租户成员状态异常：待租户恢复并由项目负责人确认后才可恢复访问'
  if (member.status && member.status !== 'active') return `成员状态“${member.status}”尚未识别，当前能力无法确认`
  const governanceRole = member.governance_role ?? (member.role === 'owner' ? 'owner' : 'member')
  if (governanceRole === 'owner' || member.role === 'owner') return 'Owner：项目范围内完整管理与内容权限'

  const capabilities = ['可读取服务端允许访问的项目内容']
  if (governanceRole === 'admin') capabilities.push('可管理成员与项目配置（不含确认、应用变更或导出）')
  const roles = new Set(member.function_roles ?? [])
  const scopes = member.function_scopes ?? {}
  if (roles.has('author')) {
    const chapters = (scopes.author ?? []).filter(scope => !scope.includes(':'))
    capabilities.push(chapters.length ? `可撰写 ${chapters.length} 个指定章节` : 'Author 未配置章节范围，不能撰写章节')
  }
  if (roles.has('researcher')) {
    const assets = (scopes.researcher ?? []).filter(scope => scope.startsWith('asset:'))
    capabilities.push(assets.length ? `可使用 ${assets.length} 项指定资料` : 'Researcher 未配置资料范围，不能使用项目资料')
  }
  if (roles.has('reviewer')) {
    const chapters = (scopes.reviewer ?? []).filter(scope => !scope.startsWith('asset:') && !scope.startsWith('delivery:'))
    capabilities.push(chapters.length ? `可评审 ${chapters.length} 个指定章节` : 'Reviewer 未配置章节范围，不能执行章节评审')
  }
  if (roles.has('observer')) capabilities.push('Observer 职能自身仅提供只读能力')
  return capabilities.join('；')
}

export function serializeProjectMemberDrafts(drafts: ProjectMemberDraft[]): ProjectMemberInput[] {
  return drafts.map(member => ({
    ...member,
    function_roles: [...member.function_roles],
    function_scopes: Object.fromEntries(Object.entries(member.function_scopes).map(([role, scopes]) => [role, [...scopes]])),
  }))
}
