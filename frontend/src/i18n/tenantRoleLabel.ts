/**
 * The tenant role vocabulary has exactly one Chinese wording in the frontend.
 *
 * The vocabulary is the server's: `internal/types/tenant_member.go` declares
 * `types.TenantRole` with exactly four values -- owner, admin, contributor,
 * viewer -- and that file is the only owner of the list. The same four values
 * used to reach the screen through two different wordings: the audit surfaces
 * and the membership drawer said 所有者 / 管理员 / 编辑 / 访客, while the
 * permission evidence desk said 所有者 / 管理员 / 协作者 / 查看者. One value,
 * two meanings, one product (issue #576).
 *
 * This module is the only place in the frontend that produces a Chinese label
 * for those four values. It holds the vocabulary and the wording, and nothing
 * else -- it does not decide which of the two is authoritative for a given
 * screen. The resolver (`auditActorRoleLabel.ts`) reads the locale bundle
 * first and falls back here; the views read the resolver. The zh-CN locale
 * bundle spreads these labels into the two namespaces that carry them
 * (`tenantMember.role.*` and `system.globalSettings.audit.actorRole.*`), so
 * the wording cannot drift between them.
 *
 * Other role vocabularies exist and are NOT this one: the project governance
 * role and the project function roles (`ProjectMembers.vue`), the topic role
 * (`firstPassEntryState.ts`), and the knowledge-base sharing level. They are
 * different axes that happen to share some words; they keep their own words
 * and are never merged into this list.
 */

/**
 * The four tenant role values, in the server's order (most privileged first).
 */
export const TENANT_ROLE_VALUES = ['owner', 'admin', 'contributor', 'viewer'] as const

/**
 * One tenant role value.
 */
export type TenantRoleValue = (typeof TENANT_ROLE_VALUES)[number]

/**
 * The one Chinese wording of the tenant role vocabulary.
 *
 * `contributor` reads as 编辑 (the role that may edit), not as 协作者: the
 * audit surfaces and the membership drawer have always said 编辑 for it, and
 * the wording is what the rest of the product inherited. `viewer` reads as
 * 访客, not as 查看者, for the same reason.
 */
export const TENANT_ROLE_LABELS: Record<TenantRoleValue, string> = {
  owner: '所有者',
  admin: '管理员',
  contributor: '编辑',
  viewer: '访客',
}

/**
 * Whether a value belongs to the tenant role vocabulary.
 */
export function isTenantRoleValue(value: string): value is TenantRoleValue {
  return (TENANT_ROLE_VALUES as readonly string[]).includes(value)
}

/**
 * The Chinese label of a tenant role, or `undefined` when the value is not
 * one of the four.
 *
 * A value outside the vocabulary has no label here on purpose: saying
 * "unknown" is the caller's business, not this module's. The caller that
 * owns the reading context decides what an unreadable value means.
 */
export function tenantRoleLabel(role: string | null | undefined): string | undefined {
  if (typeof role !== 'string') return undefined
  const value = role.trim()
  return isTenantRoleValue(value) ? TENANT_ROLE_LABELS[value] : undefined
}
