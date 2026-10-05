export type PermissionRole = 'viewer' | 'contributor' | 'admin' | 'owner' | ''
export type PermissionDecision = 'allowed' | 'denied' | 'unknown'

export interface PermissionEvidenceRow {
  id: string
  operation: string
  scope: string
  minimumRole: Exclude<PermissionRole, ''>
  proof: string
}

export const PERMISSION_ROLES: Array<Exclude<PermissionRole, ''>> = [
  'viewer',
  'contributor',
  'admin',
  'owner',
]

export const PERMISSION_ROLE_LABELS: Record<Exclude<PermissionRole, ''>, string> = {
  viewer: '查看者',
  contributor: '协作者',
  admin: '管理员',
  owner: '所有者',
}

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

const ROLE_LEVEL: Record<Exclude<PermissionRole, ''>, number> = {
  viewer: 10,
  contributor: 20,
  admin: 30,
  owner: 40,
}

export function permissionRoleLabel(role: string | null | undefined): string {
  if (role && role in PERMISSION_ROLE_LABELS) {
    return PERMISSION_ROLE_LABELS[role as Exclude<PermissionRole, ''>]
  }
  return role || '未加载'
}

export function roleSatisfies(
  current: string | null | undefined,
  minimum: Exclude<PermissionRole, ''>,
): boolean | null {
  if (!current || !(current in ROLE_LEVEL)) return null
  return ROLE_LEVEL[current as Exclude<PermissionRole, ''>] >= ROLE_LEVEL[minimum]
}

export function permissionDecision(
  current: string | null | undefined,
  minimum: Exclude<PermissionRole, ''>,
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
