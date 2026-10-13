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

export function serializeProjectMemberDrafts(drafts: ProjectMemberDraft[]): ProjectMemberInput[] {
  return drafts.map(member => ({
    ...member,
    function_roles: [...member.function_roles],
    function_scopes: Object.fromEntries(Object.entries(member.function_scopes).map(([role, scopes]) => [role, [...scopes]])),
  }))
}
