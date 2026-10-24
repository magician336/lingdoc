import { auditActorRoleLabel, type AuditActorRoleI18n } from '../../i18n/auditActorRoleLabel'
import { TENANT_ROLE_VALUES, type TenantRoleValue } from '../../i18n/tenantRoleLabel'

export type PermissionRole = TenantRoleValue | ''
export type PermissionDecision = 'allowed' | 'denied' | 'unknown'

export interface PermissionEvidenceRow {
  id: string
  operation: string
  scope: string
  minimumRole: TenantRoleValue
  proof: string
}

/**
 * The tenant roles, ascending by privilege -- the order the role matrix shows
 * them in. The list itself is the vocabulary's (`tenantRoleLabel.ts`), so the
 * evidence desk cannot offer a role the product does not have.
 */
export const PERMISSION_ROLES: TenantRoleValue[] = [...TENANT_ROLE_VALUES].reverse()

export const PERMISSION_EVIDENCE_ROWS: PermissionEvidenceRow[] = [
  {
    id: 'read',
    operation: '查看项目与资料',
    scope: '租户成员 + 知识库共享',
    minimumRole: 'viewer',
    proof: '项目读取、资料来源读取都带当前租户身份。',
  },
  {
    id: 'edit',
    operation: '编辑研究条件与章节',
    scope: '项目协作权限',
    minimumRole: 'contributor',
    proof: '写入接口由服务端按当前租户角色重查。',
  },
  {
    id: 'members',
    operation: '管理成员与共享',
    scope: '租户成员管理',
    minimumRole: 'admin',
    proof: '角色变更、成员 capability 变更写入审计日志。',
  },
  {
    id: 'deliver',
    operation: '导出交付产物',
    scope: '来源授权 + 当前快照 + 交付权限',
    minimumRole: 'contributor',
    proof: '导出与下载分别鉴权，撤权后下载不会沿用旧结果。',
  },
  {
    id: 'transfer',
    operation: '转移空间所有权',
    scope: '租户所有权',
    minimumRole: 'owner',
    proof: '所有权转移与拒绝决策都沿用 WeKnora 角色模型。',
  },
]

const ROLE_LEVEL: Record<TenantRoleValue, number> = {
  viewer: 10,
  contributor: 20,
  admin: 30,
  owner: 40,
}

/**
 * The label of a tenant role.
 *
 * It comes from the shared resolver, so this desk reads the tenant role
 * vocabulary exactly the way the audit surfaces read it: one value, one word.
 * The desk used to carry a second wording of its own, which meant the
 * "current identity" column and the "recent authorization audit" rows below
 * it called the same role two different things (issue #576).
 */
export function permissionRoleLabel(i18n: AuditActorRoleI18n, role: string | null | undefined): string {
  return auditActorRoleLabel(i18n, role)
}

export function roleSatisfies(
  current: string | null | undefined,
  minimum: TenantRoleValue,
): boolean | null {
  if (!current || !(current in ROLE_LEVEL)) return null
  return ROLE_LEVEL[current as TenantRoleValue] >= ROLE_LEVEL[minimum]
}

export function permissionDecision(
  current: string | null | undefined,
  minimum: TenantRoleValue,
): PermissionDecision {
  const result = roleSatisfies(current, minimum)
  if (result === null) return 'unknown'
  return result ? 'allowed' : 'denied'
}

export function decisionLabel(decision: PermissionDecision): string {
  if (decision === 'allowed') return '角色满足'
  if (decision === 'denied') return '角色不足'
  return '等待身份'
}
